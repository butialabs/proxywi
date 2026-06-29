package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "proxywi.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrations driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("migrator: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

type Admin struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

func (s *Store) AdminByUsername(ctx context.Context, username string) (*Admin, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, email, password_hash, created_at FROM admins WHERE username = ?`, username)
	var a Admin
	var ts int64
	if err := row.Scan(&a.ID, &a.Username, &a.Email, &a.PasswordHash, &ts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	a.CreatedAt = time.Unix(ts, 0)
	return &a, nil
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

var ErrAlreadyConfigured = errors.New("already configured")

func (s *Store) CreateAdmin(ctx context.Context, username, email, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO admins (username, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		username, email, passwordHash, time.Now().Unix())
	return err
}

// CreateFirstAdmin creates the first admin atomically. If any admin already
// exists, it returns ErrAlreadyConfigured.
func (s *Store) CreateFirstAdmin(ctx context.Context, username, email, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadyConfigured
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO admins (username, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		username, email, passwordHash, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, email, password_hash, created_at FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		var a Admin
		var ts int64
		if err := rows.Scan(&a.ID, &a.Username, &a.Email, &a.PasswordHash, &ts); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(ts, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAdmin(ctx context.Context, id int64, newUsername, newEmail, newPasswordHash string) error {
	sets := []string{}
	args := []any{}
	if newUsername != "" {
		sets = append(sets, "username = ?")
		args = append(args, newUsername)
	}
	if newEmail != "" {
		sets = append(sets, "email = ?")
		args = append(args, newEmail)
	}
	if newPasswordHash != "" {
		sets = append(sets, "password_hash = ?")
		args = append(args, newPasswordHash)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	q := `UPDATE admins SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) CreateSession(ctx context.Context, adminID int64, ttl time.Duration) (string, error) {
	id := randomHex(32)
	exp := time.Now().Add(ttl).Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_sessions (id, admin_id, expires_at) VALUES (?, ?, ?)`, id, adminID, exp)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) LookupSession(ctx context.Context, id string) (int64, bool, error) {
	var adminID int64
	var exp int64
	err := s.db.QueryRowContext(ctx, `SELECT admin_id, expires_at FROM admin_sessions WHERE id = ?`, id).Scan(&adminID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if time.Now().Unix() > exp {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id = ?`, id)
		return 0, false, nil
	}
	return adminID, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id = ?`, id)
	return err
}

func (s *Store) CreateTokenSession(ctx context.Context, clientID int64, ttl time.Duration) (string, error) {
	id := randomHex(24)
	exp := time.Now().Add(ttl).Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO token_sessions (id, client_id, expires_at) VALUES (?, ?, ?)`, id, clientID, exp)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) LookupTokenSession(ctx context.Context, id string) (int64, bool, error) {
	var clientID, exp int64
	err := s.db.QueryRowContext(ctx, `SELECT client_id, expires_at FROM token_sessions WHERE id = ?`, id).Scan(&clientID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if time.Now().Unix() > exp {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM token_sessions WHERE id = ?`, id)
		return 0, false, nil
	}
	return clientID, true, nil
}

func (s *Store) DeleteTokenSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM token_sessions WHERE id = ?`, id)
	return err
}

type Client struct {
	ID        int64
	Name      string
	TokenHash string
	TokenID   string
	LastSeen  time.Time
	CreatedAt time.Time
}

func TokenIDFromToken(token string) string {
	if len(token) >= 16 {
		return token[:16]
	}
	if token == "" {
		return ""
	}
	return token
}

func (s *Store) CreateClient(ctx context.Context, name, tokenHash, tokenID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO clients (name, token_hash, token_id, created_at) VALUES (?, ?, ?, ?)`,
		name, tokenHash, tokenID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// clientNameRe matches the auto-generated adjective-adjective-noun format.
var clientNameRe = regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`)

func (s *Store) clientNameExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

// GenerateUniqueClientName returns a fresh 3-word pet name not yet used by any client.
func (s *Store) GenerateUniqueClientName(ctx context.Context) (string, error) {
	for i := 0; i < 50; i++ {
		name := petname.Generate(3, "-")
		exists, err := s.clientNameExists(ctx, name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique client name")
}

// NormalizeLegacyClientNames renames any client whose name predates the
// generated adjective-adjective-noun format. Idempotent.
func (s *Store) NormalizeLegacyClientNames(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM clients`)
	if err != nil {
		return err
	}
	var legacyIDs []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		if !clientNameRe.MatchString(name) {
			legacyIDs = append(legacyIDs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range legacyIDs {
		name, err := s.GenerateUniqueClientName(ctx)
		if err != nil {
			return err
		}
		if err := s.UpdateClientName(ctx, id, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, token_hash, token_id, last_seen_at, created_at FROM clients ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		var last, created int64
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenID, &last, &created); err != nil {
			return nil, err
		}
		c.LastSeen = time.Unix(last, 0)
		c.CreatedAt = time.Unix(created, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ClientByID(ctx context.Context, id int64) (*Client, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, token_hash, token_id, last_seen_at, created_at FROM clients WHERE id = ?`, id)
	var c Client
	var last, created int64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenID, &last, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.LastSeen = time.Unix(last, 0)
	c.CreatedAt = time.Unix(created, 0)
	return &c, nil
}

func (s *Store) ClientByTokenID(ctx context.Context, tokenID string) (*Client, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, token_hash, token_id, last_seen_at, created_at FROM clients WHERE token_id = ?`, tokenID)
	var c Client
	var last, created int64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenID, &last, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.LastSeen = time.Unix(last, 0)
	c.CreatedAt = time.Unix(created, 0)
	return &c, nil
}

func (s *Store) AllClientTokenHashes(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, token_hash FROM clients`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var h string
		if err := rows.Scan(&id, &h); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

func (s *Store) MarkClientSeen(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET last_seen_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

func (s *Store) UpdateClientName(ctx context.Context, id int64, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET name = ? WHERE id = ?`, name, id)
	return err
}

func (s *Store) UpdateClientToken(ctx context.Context, id int64, tokenHash, tokenID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET token_hash = ?, token_id = ? WHERE id = ?`, tokenHash, tokenID, id)
	return err
}

func (s *Store) DeleteClient(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id)
	return err
}

type User struct {
	ID                 int64
	Username           string
	PasswordHash       string
	AllowedSourceCIDRs []string
	AllowedClientIDs   []int64
	CreatedAt          time.Time
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, allowedCIDRs []string, allowedClientIDs []int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, allowed_source_cidrs, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, strings.Join(allowedCIDRs, ","), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.ReplaceUserClients(ctx, id, allowedClientIDs); err != nil {
		return id, err
	}
	return id, nil
}

func (s *Store) ReplaceUserClients(ctx context.Context, userID int64, clientIDs []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_clients WHERE user_id = ?`, userID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, cid := range clientIDs {
		if cid <= 0 || seen[cid] {
			continue
		}
		seen[cid] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_clients (user_id, client_id) VALUES (?, ?)`, userID, cid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, allowed_source_cidrs, created_at FROM users WHERE username = ?`, username)
	u, err := scanUser(row)
	if err != nil || u == nil {
		return u, err
	}
	if err := s.loadUserClientIDs(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, allowed_source_cidrs, created_at FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil || u == nil {
		return u, err
	}
	if err := s.loadUserClientIDs(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) loadUserClientIDs(ctx context.Context, u *User) error {
	rows, err := s.db.QueryContext(ctx, `SELECT client_id FROM user_clients WHERE user_id = ? ORDER BY client_id`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	u.AllowedClientIDs = ids
	return rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	var cidrs string
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &cidrs, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if cidrs != "" {
		u.AllowedSourceCIDRs = strings.Split(cidrs, ",")
	}
	u.CreatedAt = time.Unix(created, 0)
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, password_hash, allowed_source_cidrs, created_at FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	idx := map[int64]int{}
	for rows.Next() {
		var u User
		var cidrs string
		var created int64
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &cidrs, &created); err != nil {
			return nil, err
		}
		if cidrs != "" {
			u.AllowedSourceCIDRs = strings.Split(cidrs, ",")
		}
		u.CreatedAt = time.Unix(created, 0)
		idx[u.ID] = len(out)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	assocRows, err := s.db.QueryContext(ctx, `SELECT user_id, client_id FROM user_clients ORDER BY client_id`)
	if err != nil {
		return nil, err
	}
	defer assocRows.Close()
	for assocRows.Next() {
		var uid, cid int64
		if err := assocRows.Scan(&uid, &cid); err != nil {
			return nil, err
		}
		if i, ok := idx[uid]; ok {
			out[i].AllowedClientIDs = append(out[i].AllowedClientIDs, cid)
		}
	}
	return out, assocRows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id int64, newUsername, newPasswordHash string, newAllowedCIDRs []string, replaceCIDRs bool, newAllowedClientIDs []int64, replaceClientIDs bool) error {
	sets := []string{}
	args := []any{}
	if newUsername != "" {
		sets = append(sets, "username = ?")
		args = append(args, newUsername)
	}
	if newPasswordHash != "" {
		sets = append(sets, "password_hash = ?")
		args = append(args, newPasswordHash)
	}
	if replaceCIDRs {
		sets = append(sets, "allowed_source_cidrs = ?")
		args = append(args, strings.Join(newAllowedCIDRs, ","))
	}
	if len(sets) > 0 {
		args = append(args, id)
		q := `UPDATE users SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	if replaceClientIDs {
		if err := s.ReplaceUserClients(ctx, id, newAllowedClientIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// UserUsage summarizes proxy activity for a proxy-access user.
type UserUsage struct {
	Count    int
	LastUsed time.Time
}

// UserUsageStats returns per-user request counts and last-used time, derived
// from proxy_events.
func (s *Store) UserUsageStats(ctx context.Context) (map[int64]UserUsage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, COUNT(*), MAX(ts) FROM proxy_events WHERE user_id IS NOT NULL GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UserUsage{}
	for rows.Next() {
		var uid int64
		var cnt int
		var last int64
		if err := rows.Scan(&uid, &cnt, &last); err != nil {
			return nil, err
		}
		out[uid] = UserUsage{Count: cnt, LastUsed: time.Unix(last, 0)}
	}
	return out, rows.Err()
}

func (s *Store) AddMetricSample(ctx context.Context, clientID int64, bucket time.Time, bytesIn, bytesOut int64, activeConns int) error {
	ts := bucket.Truncate(time.Minute).Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO metric_samples (client_id, bucket_ts, bytes_in, bytes_out, active_conns)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(client_id, bucket_ts) DO UPDATE SET
           bytes_in = bytes_in + excluded.bytes_in,
           bytes_out = bytes_out + excluded.bytes_out,
           active_conns = excluded.active_conns`,
		clientID, ts, bytesIn, bytesOut, activeConns)
	return err
}

type MetricPoint struct {
	BucketTS    int64
	BytesIn     int64
	BytesOut    int64
	ActiveConns int64
}

type MetricsFilter struct {
	Since         time.Time
	Until         time.Time
	ClientID      int64
	ClientIDs     []int64
	BucketSeconds int64
}

func (s *Store) Metrics(ctx context.Context, f MetricsFilter) ([]MetricPoint, error) {
	if f.BucketSeconds <= 0 {
		f.BucketSeconds = 60
	}
	untilTS := time.Now().Unix()
	if !f.Until.IsZero() {
		untilTS = f.Until.Unix()
	}
	args := []any{f.BucketSeconds, f.BucketSeconds, f.Since.Unix(), untilTS}
	q := `SELECT (bucket_ts / ?) * ? AS b,
                 SUM(bytes_in), SUM(bytes_out), MAX(active_conns)
          FROM metric_samples
          WHERE bucket_ts >= ? AND bucket_ts < ?`
	if f.ClientID > 0 {
		q += ` AND client_id = ?`
		args = append(args, f.ClientID)
	} else if len(f.ClientIDs) > 0 {
		q += ` AND client_id IN (` + placeholders(len(f.ClientIDs)) + `)`
		for _, id := range f.ClientIDs {
			args = append(args, id)
		}
	}
	q += ` GROUP BY b ORDER BY b`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.BucketTS, &p.BytesIn, &p.BytesOut, &p.ActiveConns); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RecentMetrics(ctx context.Context, since time.Time) ([]MetricPoint, error) {
	return s.Metrics(ctx, MetricsFilter{Since: since})
}

func (s *Store) PurgeOldData(ctx context.Context, cutoff time.Time) (int64, error) {
	ts := cutoff.Unix()
	now := time.Now().Unix()
	var total int64
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM metric_samples WHERE bucket_ts < ?`, ts},
		{`DELETE FROM auth_failures  WHERE ts        < ?`, ts},
		{`DELETE FROM ip_bans        WHERE banned_until < ?`, now},
		{`DELETE FROM admin_sessions WHERE expires_at   < ?`, now},
		{`DELETE FROM token_sessions WHERE expires_at   < ?`, now},
	} {
		res, err := s.db.ExecContext(ctx, q.sql, q.arg)
		if err != nil {
			return total, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += n
		}
	}
	return total, nil
}

type ProxyEvent struct {
	ID         int64
	TS         time.Time
	UserID     int64
	Username   string
	ClientID   int64
	ClientName string
	TargetHost string
	SourceIP   string
	Protocol   string
	Outcome    string
	BytesIn    int64
	BytesOut   int64
	DurationMS int64
}

func (s *Store) InsertProxyEvent(ctx context.Context, ev ProxyEvent) (int64, error) {
	var userPtr, clientPtr any
	if ev.UserID != 0 {
		userPtr = ev.UserID
	}
	if ev.ClientID != 0 {
		clientPtr = ev.ClientID
	}
	if ev.Outcome == "" {
		ev.Outcome = "ok"
	}
	ts := ev.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO proxy_events (ts, user_id, client_id, target_host, source_ip, protocol, outcome, bytes_in, bytes_out, duration_ms)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.Unix(), userPtr, clientPtr, ev.TargetHost, ev.SourceIP, ev.Protocol, ev.Outcome, ev.BytesIn, ev.BytesOut, ev.DurationMS)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListProxyEvents(ctx context.Context, since time.Time, limit int) ([]ProxyEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return s.ListProxyEventsFiltered(ctx, ProxyEventFilter{Since: since}, limit, 0)
}

func (s *Store) ListProxyEventsPage(ctx context.Context, since time.Time, limit, offset int) ([]ProxyEvent, error) {
	return s.ListProxyEventsFiltered(ctx, ProxyEventFilter{Since: since}, limit, offset)
}

func (s *Store) CountProxyEvents(ctx context.Context, since time.Time) (int, error) {
	return s.CountProxyEventsFiltered(ctx, ProxyEventFilter{Since: since})
}

type ProxyEventFilter struct {
	Since    time.Time
	UserID   int64
	ClientID int64
	Search   string
}

func (f ProxyEventFilter) where() (string, []any) {
	conds := []string{"e.ts >= ?"}
	args := []any{f.Since.Unix()}
	if f.UserID > 0 {
		conds = append(conds, "e.user_id = ?")
		args = append(args, f.UserID)
	}
	if f.ClientID > 0 {
		conds = append(conds, "e.client_id = ?")
		args = append(args, f.ClientID)
	}
	if f.Search != "" {
		conds = append(conds, "e.target_host LIKE ?")
		like := "%" + f.Search + "%"
		args = append(args, like)
	}
	return strings.Join(conds, " AND "), args
}

func (s *Store) CountProxyEventsFiltered(ctx context.Context, f ProxyEventFilter) (int, error) {
	where, args := f.where()
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM proxy_events e WHERE `+where, args...).Scan(&n)
	return n, err
}

func (s *Store) ListProxyEventsFiltered(ctx context.Context, f ProxyEventFilter, limit, offset int) ([]ProxyEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where, args := f.where()
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.ts, COALESCE(e.user_id,0), COALESCE(u.username,''),
                COALESCE(e.client_id,0), COALESCE(c.name,''),
                e.target_host, e.source_ip, e.protocol, e.outcome,
                e.bytes_in, e.bytes_out, e.duration_ms
         FROM proxy_events e
         LEFT JOIN users   u ON u.id = e.user_id
         LEFT JOIN clients c ON c.id = e.client_id
         WHERE `+where+`
         ORDER BY e.ts DESC, e.id DESC
         LIMIT ? OFFSET ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProxyEvent
	for rows.Next() {
		var p ProxyEvent
		var ts int64
		if err := rows.Scan(&p.ID, &ts, &p.UserID, &p.Username, &p.ClientID, &p.ClientName,
			&p.TargetHost, &p.SourceIP, &p.Protocol, &p.Outcome,
			&p.BytesIn, &p.BytesOut, &p.DurationMS); err != nil {
			return nil, err
		}
		p.TS = time.Unix(ts, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PurgeProxyEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM proxy_events WHERE ts < ?`, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) RecordAuthFailure(ctx context.Context, sourceIP, username, protocol string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_failures (ts, source_ip, username_attempted, protocol) VALUES (?, ?, ?, ?)`,
		time.Now().Unix(), sourceIP, username, protocol)
	return err
}

func (s *Store) CountAuthFailuresSince(ctx context.Context, sourceIP string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_failures WHERE source_ip = ? AND ts >= ?`, sourceIP, since.Unix()).Scan(&n)
	return n, err
}

type Ban struct {
	SourceIP     string
	BannedUntil  time.Time
	Reason       string
	FailureCount int
}

func (s *Store) UpsertBan(ctx context.Context, sourceIP string, until time.Time, reason string, failures int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ip_bans (source_ip, banned_until, reason, failure_count) VALUES (?, ?, ?, ?)
         ON CONFLICT(source_ip) DO UPDATE SET banned_until = excluded.banned_until, reason = excluded.reason, failure_count = excluded.failure_count`,
		sourceIP, until.Unix(), reason, failures)
	return err
}

func (s *Store) ActiveBan(ctx context.Context, sourceIP string) (*Ban, error) {
	row := s.db.QueryRowContext(ctx, `SELECT source_ip, banned_until, reason, failure_count FROM ip_bans WHERE source_ip = ? AND banned_until > ?`,
		sourceIP, time.Now().Unix())
	var b Ban
	var until int64
	if err := row.Scan(&b.SourceIP, &until, &b.Reason, &b.FailureCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	b.BannedUntil = time.Unix(until, 0)
	return &b, nil
}

func (s *Store) ListBans(ctx context.Context, activeOnly bool) ([]Ban, error) {
	q := `SELECT source_ip, banned_until, reason, failure_count FROM ip_bans`
	args := []any{}
	if activeOnly {
		q += ` WHERE banned_until > ?`
		args = append(args, time.Now().Unix())
	}
	q += ` ORDER BY banned_until DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ban
	for rows.Next() {
		var b Ban
		var until int64
		if err := rows.Scan(&b.SourceIP, &until, &b.Reason, &b.FailureCount); err != nil {
			return nil, err
		}
		b.BannedUntil = time.Unix(until, 0)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) UnbanIP(ctx context.Context, sourceIP string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ip_bans WHERE source_ip = ?`, sourceIP)
	return err
}

type OriginStat struct {
	Origin   string
	Total    int
	Blocked  int
	LastSeen time.Time
}

func (s *Store) OriginStats(ctx context.Context, since time.Time, limit int) ([]OriginStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_ip,
		        COUNT(*) AS total,
		        SUM(CASE WHEN outcome = 'denied' THEN 1 ELSE 0 END) AS blocked,
		        MAX(ts) AS last_ts
		 FROM proxy_events
		 WHERE ts >= ? AND source_ip <> ''
		 GROUP BY source_ip
		 ORDER BY total DESC
		 LIMIT ?`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OriginStat
	for rows.Next() {
		var st OriginStat
		var last int64
		if err := rows.Scan(&st.Origin, &st.Total, &st.Blocked, &last); err != nil {
			return nil, err
		}
		st.LastSeen = time.Unix(last, 0)
		out = append(out, st)
	}
	return out, rows.Err()
}

type AllowedIP struct {
	IP        string
	Reason    string
	CreatedAt time.Time
}

func (s *Store) AddAllowedIP(ctx context.Context, ip, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ip_allowlist (ip, reason, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(ip) DO UPDATE SET reason = excluded.reason`,
		ip, reason, time.Now().Unix())
	return err
}

func (s *Store) RemoveAllowedIP(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ip_allowlist WHERE ip = ?`, ip)
	return err
}

func (s *Store) ListAllowedIPs(ctx context.Context) ([]AllowedIP, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip, reason, created_at FROM ip_allowlist ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AllowedIP
	for rows.Next() {
		var a AllowedIP
		var ts int64
		if err := rows.Scan(&a.IP, &a.Reason, &ts); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(ts, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) IsIPAllowed(ctx context.Context, ip string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ip_allowlist WHERE ip = ?`, ip).Scan(&count)
	return count > 0, err
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
