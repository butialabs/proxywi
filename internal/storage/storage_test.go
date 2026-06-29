package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s, func() { _ = s.Close() }
}

func TestCreateClient_AndList(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := s.CreateClient(ctx, "test-client", "hash", "tokenprefix")
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

	id, err := s.CreateClient(ctx, "agent", "hash", "tokenprefix")
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

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
