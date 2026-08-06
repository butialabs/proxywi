package storage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func openTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s, func() { _ = s.Close() }
}

func TestCreateClient_AndList(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := s.CreateClient(ctx, "test-client", "hash", "", "tokenprefix")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	list, err := s.ListClients(ctx)
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 client, got %d", len(list))
	}
	if list[0].Name != "test-client" {
		t.Fatalf("unexpected name: %q", list[0].Name)
	}
}

func TestMarkClientSeen_PersistsAgentVersion(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := s.CreateClient(ctx, "agent", "hash", "", "tokenprefix")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	if err := s.MarkClientSeen(ctx, id, "2.0.0"); err != nil {
		t.Fatalf("mark seen: %v", err)
	}

	c, err := s.ClientByID(ctx, id)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	if c.AgentVersion != "2.0.0" {
		t.Fatalf("expected agent version 2.0.0, got %q", c.AgentVersion)
	}
	if c.LastSeen.IsZero() {
		t.Fatal("expected last_seen to be set")
	}
}

func TestInsertProxyEvent_NoSourceIP(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ev := ProxyEvent{
		TS:         time.Now(),
		Username:   "u",
		TargetHost: "example.com:80",
		Protocol:   "http",
		Outcome:    "ok",
	}
	id, err := s.InsertProxyEvent(ctx, ev)
	if err != nil {
		t.Fatalf("insert proxy event: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive event id, got %d", id)
	}

	var sourceIP any
	err = s.db.QueryRowContext(ctx, `SELECT source_ip FROM proxy_events WHERE id = ?`, id).Scan(&sourceIP)
	if err == nil {
		t.Fatal("expected error selecting removed source_ip column")
	}
}

func TestCreateUser_WithoutCIDRs(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := s.CreateUser(ctx, "alice", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u, err := s.UserByID(ctx, id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.Username != "alice" {
		t.Fatalf("unexpected username: %q", u.Username)
	}

	var cidrs any
	err = s.db.QueryRowContext(ctx, `SELECT allowed_source_cidrs FROM users WHERE id = ?`, id).Scan(&cidrs)
	if err == nil {
		t.Fatal("expected error selecting removed allowed_source_cidrs column")
	}

	var uid any
	err = s.db.QueryRowContext(ctx, `SELECT user_id FROM user_clients WHERE user_id = ?`, id).Scan(&uid)
	if err == nil {
		t.Fatal("expected error selecting removed user_clients table")
	}
}

func TestPurgeOldData_DoesNotDependOnDroppedTables(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, err := s.PurgeOldData(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("purge old data: %v", err)
	}
}

func TestInsertProxyEvent_OutcomeReason(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ev := ProxyEvent{
		TargetHost:    "example.com:443",
		Protocol:      "http",
		Outcome:       "denied",
		OutcomeReason: "auth",
	}
	_, err := s.InsertProxyEvent(ctx, ev)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	events, err := s.ListProxyEvents(ctx, time.Unix(0, 0), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].OutcomeReason != "auth" {
		t.Fatalf("expected reason %q, got %q", "auth", events[0].OutcomeReason)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func TestAllClientTokenHashes_RevocationRacingRefillIsNotCached(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const rounds = 60
	for i := range rounds {
		id, err := s.CreateClient(ctx, fmt.Sprintf("victim-%d", i), "bcrypt-hash", fmt.Sprintf("sha-%d", i), fmt.Sprintf("tokenid-%d", i))
		if err != nil {
			t.Fatalf("create client: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.AllClientTokenHashes(ctx)
		}()
		go func() {
			defer wg.Done()
			_ = s.DeleteClient(ctx, id)
		}()
		wg.Wait()

		hashes, err := s.AllClientTokenHashes(ctx)
		if err != nil {
			t.Fatalf("read hashes: %v", err)
		}
		if _, present := hashes[id]; present {
			t.Fatalf("round %d: deleted client %d still served from the hash cache", i, id)
		}
	}
}

func TestAllClientTokenHashes_ExposesTokenID(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := s.CreateClient(ctx, "with-token-id", "bcrypt-hash", "", "abcdef0123456789")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	hashes, err := s.AllClientTokenHashes(ctx)
	if err != nil {
		t.Fatalf("read hashes: %v", err)
	}
	if got := hashes[id].TokenID; got != "abcdef0123456789" {
		t.Fatalf("TokenID = %q, want %q", got, "abcdef0123456789")
	}
}

func TestClientByToken_ResolvesEveryHashShape(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	modern := "aaaabbbbccccddddaaaabbbbccccdddd"
	modernID, err := s.CreateClient(ctx, "modern", "", SHA256Token(modern), TokenIDFromToken(modern))
	if err != nil {
		t.Fatalf("create modern: %v", err)
	}

	legacy := "11112222333344441111222233334444"
	legacyHash, err := bcrypt.GenerateFromPassword([]byte(legacy), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	legacyID, err := s.CreateClient(ctx, "legacy", string(legacyHash), "", "")
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
		want  int64
	}{
		{"sha256 fast path", modern, modernID},
		{"legacy bcrypt fallback", legacy, legacyID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, err := s.ClientByToken(ctx, tc.token)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if cl == nil || cl.ID != tc.want {
				t.Fatalf("got %v, want client %d", cl, tc.want)
			}
		})
	}

	for _, tc := range []struct{ name, token string }{
		{"empty", ""},
		{"unknown", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, err := s.ClientByToken(ctx, tc.token)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if cl != nil {
				t.Fatalf("expected no match, got client %d", cl.ID)
			}
		})
	}
}

func TestClientByToken_FallsThroughOnTokenIDPrefixCollision(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const prefix = "0123456789abcdef"
	if _, err := s.CreateClient(ctx, "prefix-squatter", "", SHA256Token(prefix+"AAAAAAAAAAAAAAAA"), prefix); err != nil {
		t.Fatalf("create squatter: %v", err)
	}

	wanted := prefix + "BBBBBBBBBBBBBBBB"
	hash, err := bcrypt.GenerateFromPassword([]byte(wanted), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Legacy row: no token_id, so only the fallback scan can find it.
	legacyID, err := s.CreateClient(ctx, "legacy-same-prefix", string(hash), "", "")
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}

	cl, err := s.ClientByToken(ctx, wanted)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cl == nil || cl.ID != legacyID {
		t.Fatalf("got %v, want client %d; the prefix collision swallowed the match", cl, legacyID)
	}
}

func TestClientByToken_BackfillsSHA256AfterBcryptMatch(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	token := "cccc4444dddd5555cccc4444dddd5555"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id, err := s.CreateClient(ctx, "to-migrate", string(hash), "", "")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	if _, err := s.ClientByToken(ctx, token); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	cl, err := s.ClientByID(ctx, id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cl.TokenHashSHA256 != SHA256Token(token) {
		t.Fatalf("sha256 not backfilled: got %q", cl.TokenHashSHA256)
	}
}
