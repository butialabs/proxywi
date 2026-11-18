package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
	"golang.org/x/crypto/bcrypt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type sessionCacheEntry struct {
	adminID   int64
	expiresAt int64
	cachedAt  int64
}

type tokenCacheEntry struct {
	clientID  int64
	expiresAt int64
	cachedAt  int64
}

const cacheSessionTTL = 60

type ClientHashes struct {
	Bcrypt  string
	SHA256  string
	TokenID string
}

func SHA256Token(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

type Store struct {
	db           *sql.DB
	sessionCache sync.Map
	tokenCache   sync.Map
	hashCacheMu  sync.RWMutex
	hashCache    map[int64]ClientHashes
	hashCacheOK  bool
	hashCacheGen uint64
}

func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "proxywi.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(268435456)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
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

	curVer, dirty, verErr := m.Version()
	if !errors.Is(verErr, migrate.ErrNilVersion) {
		if verErr != nil {
			return fmt.Errorf("check migration version: %w", verErr)
		}
		floor := migrationFloor(src, curVer)
		if dirty || floor < curVer {
			if err := m.Force(int(floor)); err != nil {
				return fmt.Errorf("force migration: %w", err)
			}
		}
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func migrationFloor(src interface {
	First() (uint, error)
	Next(uint) (uint, error)
}, target uint) uint {
	cur, err := src.First()
	if err != nil || cur > target {
		return 0
	}
	for {
		next, err := src.Next(cur)
		if err != nil || next > target {
			return cur
		}
		cur = next
	}
}

func (s *Store) Close() error { return s.db.Close() }

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
	a.CreatedAt = time.Unix(ts, 0).UTC()
	return &a, nil
}

func (s *Store) AdminUsernameByID(ctx context.Context, id int64) (string, error) {
	var username string
	err := s.db.QueryRowContext(ctx, `SELECT username FROM admins WHERE id = ?`, id).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return username, nil
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

var ErrAlreadyConfigured = errors.New("already configured")

func (s *Store) CreateFirstAdmin(ctx context.Context, username, email, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

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
	now := time.Now().Unix()
	if v, ok := s.sessionCache.Load(id); ok {
		e := v.(sessionCacheEntry)
		if now < e.expiresAt && now-e.cachedAt < cacheSessionTTL {
			return e.adminID, true, nil
		}
		s.sessionCache.Delete(id)
	}
	var adminID, exp int64
	err := s.db.QueryRowContext(ctx, `SELECT admin_id, expires_at FROM admin_sessions WHERE id = ?`, id).Scan(&adminID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if now > exp {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id = ?`, id)
		return 0, false, nil
	}
	s.sessionCache.Store(id, sessionCacheEntry{adminID: adminID, expiresAt: exp, cachedAt: now})
	return adminID, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	s.sessionCache.Delete(id)
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
	now := time.Now().Unix()
	if v, ok := s.tokenCache.Load(id); ok {
		e := v.(tokenCacheEntry)
		if now < e.expiresAt && now-e.cachedAt < cacheSessionTTL {
			return e.clientID, true, nil
		}
		s.tokenCache.Delete(id)
	}
	var clientID, exp int64
	err := s.db.QueryRowContext(ctx, `SELECT client_id, expires_at FROM token_sessions WHERE id = ?`, id).Scan(&clientID, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if now > exp {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM token_sessions WHERE id = ?`, id)
		return 0, false, nil
	}
	s.tokenCache.Store(id, tokenCacheEntry{clientID: clientID, expiresAt: exp, cachedAt: now})
	return clientID, true, nil
}

func (s *Store) DeleteTokenSession(ctx context.Context, id string) error {
	s.tokenCache.Delete(id)
	_, err := s.db.ExecContext(ctx, `DELETE FROM token_sessions WHERE id = ?`, id)
	return err
}

type Client struct {
	ID              int64
	Name            string
	TokenHash       string
	TokenHashSHA256 string
	TokenID         string
	AgentVersion    string
	LastSeen        time.Time
	CreatedAt       time.Time
	PingMS          *int64
	PingedAt        *time.Time
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

func (s *Store) CreateClient(ctx context.Context, name, tokenHash, tokenHashSHA256, tokenID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO clients (name, token_hash, token_hash_sha256, token_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		name, tokenHash, tokenHashSHA256, tokenID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	s.invalidateHashCache()
	return res.LastInsertId()
}

var clientNameRe = regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`)

func (s *Store) clientNameExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

func (s *Store) GenerateUniqueClientName(ctx context.Context) (string, error) {
	for range 50 {
		name := petname.Generate(3, "-")
		exists, err := s.clientNameExists(ctx, name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
	}
	return "", errors.New("could not generate a unique client name")
}

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
			_ = rows.Close()
			return err
		}
		if !clientNameRe.MatchString(name) {
			legacyIDs = append(legacyIDs, id)
		}
	}
	_ = rows.Close()
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, token_hash, token_id, COALESCE(agent_version,''), last_seen_at, created_at, ping_ms, pinged_at FROM clients ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Client
	for rows.Next() {
		var c Client
		var last, created int64
		var pingMS sql.NullInt64
		var pingedAt sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenID, &c.AgentVersion, &last, &created, &pingMS, &pingedAt); err != nil {
			return nil, err
		}
		c.LastSeen = time.Unix(last, 0).UTC()
		c.CreatedAt = time.Unix(created, 0).UTC()
		if pingMS.Valid {
			c.PingMS = &pingMS.Int64
		}
		if pingedAt.Valid {
			t := time.Unix(pingedAt.Int64, 0).UTC()
			c.PingedAt = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ClientByID(ctx context.Context, id int64) (*Client, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, token_hash, COALESCE(token_hash_sha256,''), token_id, COALESCE(agent_version,''), last_seen_at, created_at, ping_ms, pinged_at FROM clients WHERE id = ?`, id)
	var c Client
	var last, created int64
	var pingMS sql.NullInt64
	var pingedAt sql.NullInt64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenHashSHA256, &c.TokenID, &c.AgentVersion, &last, &created, &pingMS, &pingedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.LastSeen = time.Unix(last, 0).UTC()
	c.CreatedAt = time.Unix(created, 0).UTC()
	if pingMS.Valid {
		c.PingMS = &pingMS.Int64
	}
	if pingedAt.Valid {
		t := time.Unix(pingedAt.Int64, 0).UTC()
		c.PingedAt = &t
	}
	return &c, nil
}

func (s *Store) ClientByTokenID(ctx context.Context, tokenID string) (*Client, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, token_hash, COALESCE(token_hash_sha256,''), token_id, COALESCE(agent_version,''), last_seen_at, created_at FROM clients WHERE token_id = ?`, tokenID)
	var c Client
	var last, created int64
	if err := row.Scan(&c.ID, &c.Name, &c.TokenHash, &c.TokenHashSHA256, &c.TokenID, &c.AgentVersion, &last, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.LastSeen = time.Unix(last, 0).UTC()
	c.CreatedAt = time.Unix(created, 0).UTC()
	return &c, nil
}

func (s *Store) ClientByToken(ctx context.Context, token string) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	sha := SHA256Token(token)

	if tokenID := TokenIDFromToken(token); tokenID != "" {
		cl, err := s.ClientByTokenID(ctx, tokenID)
		if err != nil {
			return nil, err
		}
		if cl != nil {
			if cl.TokenHashSHA256 != "" {
				if subtle.ConstantTimeCompare([]byte(cl.TokenHashSHA256), []byte(sha)) == 1 {
					return cl, nil
				}
			} else if bcrypt.CompareHashAndPassword([]byte(cl.TokenHash), []byte(token)) == nil {
				s.backfillTokenSHA256(ctx, cl.ID, sha)
				return cl, nil
			}
			// Fall through: a legacy row without a token_id could still match.
		}
	}

	hashes, err := s.AllClientTokenHashes(ctx)
	if err != nil {
		return nil, err
	}
	for id, h := range hashes {
		if h.SHA256 != "" && subtle.ConstantTimeCompare([]byte(h.SHA256), []byte(sha)) == 1 {
			return s.ClientByID(ctx, id)
		}
	}
	for id, h := range hashes {
		if h.SHA256 != "" || h.TokenID != "" {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(h.Bcrypt), []byte(token)) == nil {
			cl, err := s.ClientByID(ctx, id)
			if err != nil {
				return nil, err
			}
			s.backfillTokenSHA256(ctx, id, sha)
			return cl, nil
		}
	}
	return nil, nil
}

func (s *Store) backfillTokenSHA256(ctx context.Context, clientID int64, sha string) {
	if err := s.SetClientTokenSHA256(ctx, clientID, sha); err != nil {
		slog.Warn("backfill client token sha256", "client_id", clientID, "err", err)
	}
}

func (s *Store) AllClientTokenHashes(ctx context.Context) (map[int64]ClientHashes, error) {
	s.hashCacheMu.RLock()
	if s.hashCacheOK {
		out := maps.Clone(s.hashCache)
		s.hashCacheMu.RUnlock()
		return out, nil
	}
	gen := s.hashCacheGen
	s.hashCacheMu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT id, token_hash, COALESCE(token_hash_sha256,''), COALESCE(token_id,'') FROM clients`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]ClientHashes{}
	for rows.Next() {
		var id int64
		var h ClientHashes
		if err := rows.Scan(&id, &h.Bcrypt, &h.SHA256, &h.TokenID); err != nil {
			return nil, err
		}
		out[id] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.hashCacheMu.Lock()
	if s.hashCacheGen == gen {
		s.hashCache = out
		s.hashCacheOK = true
	}
	s.hashCacheMu.Unlock()
	return out, nil
}

func (s *Store) invalidateHashCache() {
	s.hashCacheMu.Lock()
	s.hashCacheOK = false
	s.hashCache = nil
	s.hashCacheGen++
	s.hashCacheMu.Unlock()
}

func (s *Store) MarkClientSeen(ctx context.Context, id int64, agentVersion string) error {
	if agentVersion != "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE clients SET last_seen_at = ?, agent_version = ? WHERE id = ?`,
			time.Now().Unix(), agentVersion, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET last_seen_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

func (s *Store) UpdateClientName(ctx context.Context, id int64, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET name = ? WHERE id = ?`, name, id)
	return err
}

func (s *Store) UpdateClientToken(ctx context.Context, id int64, tokenHash, tokenHashSHA256, tokenID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET token_hash = ?, token_hash_sha256 = ?, token_id = ? WHERE id = ?`, tokenHash, tokenHashSHA256, tokenID, id)
	if err == nil {
		s.invalidateHashCache()
	}
	return err
}

func (s *Store) SetClientTokenSHA256(ctx context.Context, id int64, tokenHashSHA256 string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE clients SET token_hash_sha256 = ? WHERE id = ?`, tokenHashSHA256, id)
	if err == nil {
		s.invalidateHashCache()
	}
	return err
}

func (s *Store) DeleteClient(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id)
	if err == nil {
		s.invalidateHashCache()
	}
	return err
}

func (s *Store) UpdateClientPing(ctx context.Context, id int64, pingMS int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE clients SET ping_ms = ?, pinged_at = ? WHERE id = ?`,
		pingMS, time.Now().Unix(), id)
	return err
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, passwordHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	u.CreatedAt = time.Unix(created, 0).UTC()
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, password_hash, created_at FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []User
	for rows.Next() {
		var u User
		var created int64
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id int64, newUsername, newPasswordHash string) error {
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
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	q := `UPDATE users SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

type UserUsage struct {
	Count    int
	LastUsed time.Time
}

func (s *Store) UserUsageStats(ctx context.Context) (map[int64]UserUsage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, COUNT(*), MAX(ts) FROM proxy_events WHERE user_id IS NOT NULL GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]UserUsage{}
	for rows.Next() {
		var uid int64
		var cnt int
		var last int64
		if err := rows.Scan(&uid, &cnt, &last); err != nil {
			return nil, err
		}
		out[uid] = UserUsage{Count: cnt, LastUsed: time.Unix(last, 0).UTC()}
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
	}
	q += ` GROUP BY b ORDER BY b`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func (s *Store) PurgeOldData(ctx context.Context, cutoff time.Time) (int64, error) {
	ts := cutoff.Unix()
	now := time.Now().Unix()
	var total int64
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM metric_samples WHERE bucket_ts < ?`, ts},
		{`DELETE FROM domain_stats_daily WHERE day < ?`, dayStart(cutoff)},
		{`DELETE FROM auth_failures  WHERE ts        < ?`, ts},
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
	s.sweepSessionCaches(now)
	return total, nil
}

func (s *Store) sweepSessionCaches(now int64) {
	stale := func(expiresAt, cachedAt int64) bool {
		return now >= expiresAt || now-cachedAt >= cacheSessionTTL
	}
	s.sessionCache.Range(func(k, v any) bool {
		if e, ok := v.(sessionCacheEntry); !ok || stale(e.expiresAt, e.cachedAt) {
			s.sessionCache.Delete(k)
		}
		return true
	})
	s.tokenCache.Range(func(k, v any) bool {
		if e, ok := v.(tokenCacheEntry); !ok || stale(e.expiresAt, e.cachedAt) {
			s.tokenCache.Delete(k)
		}
		return true
	})
}

type ProxyEvent struct {
	ID            int64
	TS            time.Time
	UserID        int64
	Username      string
	ClientID      int64
	ClientName    string
	TargetHost    string
	Protocol      string
	Outcome       string
	OutcomeReason string
	BytesIn       int64
	BytesOut      int64
	DurationMS    int64
	ConnectMS     int64
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO proxy_events (ts, user_id, client_id, target_host, protocol, outcome, outcome_reason, bytes_in, bytes_out, duration_ms, connect_ms)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.Unix(), userPtr, clientPtr, ev.TargetHost, ev.Protocol, ev.Outcome, ev.OutcomeReason, ev.BytesIn, ev.BytesOut, ev.DurationMS, ev.ConnectMS)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if ev.Outcome == "ok" {
		var connectHit int64
		if ev.ConnectMS > 0 {
			connectHit = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO domain_stats_daily (day, client_id, domain, requests, bytes_in, bytes_out, sum_duration_ms, sum_connect_ms, connect_count)
             VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)
             ON CONFLICT(day, client_id, domain) DO UPDATE SET
                requests        = requests + 1,
                bytes_in        = bytes_in + excluded.bytes_in,
                bytes_out       = bytes_out + excluded.bytes_out,
                sum_duration_ms = sum_duration_ms + excluded.sum_duration_ms,
                sum_connect_ms  = sum_connect_ms + excluded.sum_connect_ms,
                connect_count   = connect_count + excluded.connect_count`,
			dayStart(ts), ev.ClientID, domainOf(ev.TargetHost), ev.BytesIn, ev.BytesOut, ev.DurationMS, ev.ConnectMS, connectHit); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func dayStart(t time.Time) int64 {
	u := t.Unix()
	return u - u%86400
}

func domainOf(host string) string {
	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}
	return host
}

func (s *Store) ListProxyEvents(ctx context.Context, since time.Time, limit int) ([]ProxyEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return s.ListProxyEventsFiltered(ctx, ProxyEventFilter{Since: since, Sort: "time", Desc: true}, limit, 0)
}

type ProxyEventFilter struct {
	Since    time.Time
	UserID   int64
	ClientID int64
	Search   string
	Sort string
	Desc bool
}

var proxyEventSortColumns = map[string]string{
	"time":     "e.ts",
	"outcome":  "e.outcome",
	"protocol": "e.protocol",
	"user":     "COALESCE(u.username,'')",
	"client":   "COALESCE(c.name,'')",
	"target":   "e.target_host",
	"in":       "e.bytes_in",
	"out":      "e.bytes_out",
	"connect":  "e.connect_ms",
	"duration": "e.duration_ms",
}

// ProxyEventSortKeys lists the sort keys accepted by ListProxyEventsFiltered.
var ProxyEventSortKeys = []string{"time", "outcome", "protocol", "user", "client", "target", "in", "out", "connect", "duration"}

func (f ProxyEventFilter) orderBy() string {
	col, ok := proxyEventSortColumns[f.Sort]
	if !ok {
		col = "e.ts"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	return col + " " + dir + ", e.id " + dir
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
                e.target_host, e.protocol, e.outcome, e.outcome_reason,
                e.bytes_in, e.bytes_out, e.duration_ms, e.connect_ms
         FROM proxy_events e
         LEFT JOIN users   u ON u.id = e.user_id
         LEFT JOIN clients c ON c.id = e.client_id
         WHERE `+where+`
         ORDER BY `+f.orderBy()+`
         LIMIT ? OFFSET ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProxyEvent
	for rows.Next() {
		var p ProxyEvent
		var ts int64
		if err := rows.Scan(&p.ID, &ts, &p.UserID, &p.Username, &p.ClientID, &p.ClientName,
			&p.TargetHost, &p.Protocol, &p.Outcome, &p.OutcomeReason,
			&p.BytesIn, &p.BytesOut, &p.DurationMS, &p.ConnectMS); err != nil {
			return nil, err
		}
		p.TS = time.Unix(ts, 0).UTC()
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

func (s *Store) PurgeAllProxyEvents(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM proxy_events`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM domain_stats_daily`); err != nil {
		return n, err
	}
	return n, nil
}

type DomainStat struct {
	Domain        string
	Requests      int64
	BytesIn       int64
	BytesOut      int64
	AvgDurationMS int64
	AvgConnectMS  int64
}

var DomainStatSortKeys = []string{"domain", "requests", "in", "out", "connect", "duration"}

var domainStatSortColumns = map[string]string{
	"domain":   "domain",
	"requests": "requests",
	"in":       "bytes_in",
	"out":      "bytes_out",
	"connect":  "avg_connect",
	"duration": "avg_duration",
}

type DomainStatsQuery struct {
	ClientID int64
	Since    time.Time
	Sort     string // one of DomainStatSortKeys; defaults to "requests"
	Desc     bool
	Limit    int
	Offset   int
}

func (q DomainStatsQuery) where() (string, []any) {
	if q.ClientID != 0 {
		return `client_id = ? AND day >= ?`, []any{q.ClientID, dayStart(q.Since)}
	}
	return `day >= ?`, []any{dayStart(q.Since)}
}

func (s *Store) CountDomainStats(ctx context.Context, q DomainStatsQuery) (int, error) {
	where, args := q.where()
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT domain) FROM domain_stats_daily WHERE `+where, args...).Scan(&n)
	return n, err
}

func (s *Store) DomainStats(ctx context.Context, q DomainStatsQuery) ([]DomainStat, error) {
	col, ok := domainStatSortColumns[q.Sort]
	if !ok {
		col = "requests"
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	where, args := q.where()
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT domain,
                SUM(requests)  AS requests,
                SUM(bytes_in)  AS bytes_in,
                SUM(bytes_out) AS bytes_out,
                CAST(COALESCE(SUM(sum_duration_ms) / NULLIF(SUM(requests), 0), 0) AS INTEGER)     AS avg_duration,
                CAST(COALESCE(SUM(sum_connect_ms)  / NULLIF(SUM(connect_count), 0), 0) AS INTEGER) AS avg_connect
         FROM domain_stats_daily
         WHERE `+where+`
         GROUP BY domain
         ORDER BY `+col+` `+dir+`, domain ASC
         LIMIT ? OFFSET ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DomainStat
	for rows.Next() {
		var d DomainStat
		if err := rows.Scan(&d.Domain, &d.Requests, &d.BytesIn, &d.BytesOut, &d.AvgDurationMS, &d.AvgConnectMS); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpsertBlockedDomains(ctx context.Context, domains []string, category string) error {
	const chunkSize = 1000
	for i := 0; i < len(domains); i += chunkSize {
		end := min(i+chunkSize, len(domains))
		chunk := domains[i:end]
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO blocked_domains (domain, category) VALUES (?, ?)`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, domain := range chunk {
			if _, err := stmt.ExecContext(ctx, domain, category); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return err
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteBlockedDomainsByCategory(ctx context.Context, category string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blocked_domains WHERE category = ?`, category)
	return err
}

func (s *Store) LoadBlockedDomains(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT domain FROM blocked_domains`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]bool)
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = true
	}
	return out, rows.Err()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
