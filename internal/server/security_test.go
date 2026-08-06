package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func newTestGate(t *testing.T) *AuthGate {
	t.Helper()
	store, err := storage.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return DefaultAuthGate(t.Context(), store, slog.New(slog.NewTextHandler(io.Discard, nil)), 1000, time.Second)
}

func addUser(t *testing.T, g *AuthGate, username, password string) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id, err := g.Store.CreateUser(context.Background(), username, string(hash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func TestVerifyCredentials_AcceptsAndRejects(t *testing.T) {
	g := newTestGate(t)
	addUser(t, g, "alice", "correct-horse")

	if _, ok := g.VerifyCredentials(t.Context(), "alice", "correct-horse"); !ok {
		t.Fatal("valid credentials rejected")
	}
	if _, ok := g.VerifyCredentials(t.Context(), "alice", "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := g.VerifyCredentials(t.Context(), "nobody", "whatever"); ok {
		t.Fatal("unknown user accepted")
	}
}

func TestVerifyCredentials_SecondCallSkipsBcrypt(t *testing.T) {
	g := newTestGate(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := g.Store.CreateUser(t.Context(), "bob", string(hash)); err != nil {
		t.Fatalf("create user: %v", err)
	}

	start := time.Now()
	if _, ok := g.VerifyCredentials(t.Context(), "bob", "s3cret"); !ok {
		t.Fatal("first verify failed")
	}
	cold := time.Since(start)

	start = time.Now()
	if _, ok := g.VerifyCredentials(t.Context(), "bob", "s3cret"); !ok {
		t.Fatal("second verify failed")
	}
	warm := time.Since(start)

	if warm > cold/4 {
		t.Fatalf("second verify took %v against a cold %v; bcrypt does not look cached", warm, cold)
	}
	t.Logf("cold=%v warm=%v", cold, warm)
}

func TestVerifyCredentials_PasswordChangeTakesEffectImmediately(t *testing.T) {
	g := newTestGate(t)
	id := addUser(t, g, "carol", "old-password")

	if _, ok := g.VerifyCredentials(t.Context(), "carol", "old-password"); !ok {
		t.Fatal("valid credentials rejected")
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := g.Store.UpdateUser(t.Context(), id, "", string(newHash)); err != nil {
		t.Fatalf("update user: %v", err)
	}

	if _, ok := g.VerifyCredentials(t.Context(), "carol", "old-password"); ok {
		t.Fatal("old password still accepted after the password was changed")
	}
	if _, ok := g.VerifyCredentials(t.Context(), "carol", "new-password"); !ok {
		t.Fatal("new password rejected")
	}
}

func TestVerifyCredentials_DeletionTakesEffectImmediately(t *testing.T) {
	g := newTestGate(t)
	id := addUser(t, g, "dave", "hunter2")

	if _, ok := g.VerifyCredentials(t.Context(), "dave", "hunter2"); !ok {
		t.Fatal("valid credentials rejected")
	}
	if err := g.Store.DeleteUser(t.Context(), id); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, ok := g.VerifyCredentials(t.Context(), "dave", "hunter2"); ok {
		t.Fatal("deleted user still authenticates from cache")
	}
}

func TestVerifyCredentials_WrongPasswordNeverCached(t *testing.T) {
	g := newTestGate(t)
	addUser(t, g, "erin", "right")

	if _, ok := g.VerifyCredentials(t.Context(), "erin", "right"); !ok {
		t.Fatal("valid credentials rejected")
	}
	for range 5 {
		if _, ok := g.VerifyCredentials(t.Context(), "erin", "wrong"); ok {
			t.Fatal("wrong password accepted")
		}
	}
}

func TestVerifyCredentials_SharedPasswordDoesNotCrossUsers(t *testing.T) {
	g := newTestGate(t)
	addUser(t, g, "frank", "same-password")
	addUser(t, g, "grace", "same-password")

	if _, ok := g.VerifyCredentials(t.Context(), "frank", "same-password"); !ok {
		t.Fatal("frank rejected")
	}
	u, ok := g.VerifyCredentials(t.Context(), "grace", "same-password")
	if !ok {
		t.Fatal("grace rejected")
	}
	if u.Username != "grace" {
		t.Fatalf("returned user = %q, want grace", u.Username)
	}
}

func BenchmarkVerifyCredentials_Cached(b *testing.B) {
	store, err := storage.Open(b.Context(), b.TempDir())
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	g := DefaultAuthGate(ctx, store, slog.New(slog.NewTextHandler(io.Discard, nil)), 1_000_000, time.Second)
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err != nil {
		b.Fatalf("hash: %v", err)
	}
	if _, err := store.CreateUser(ctx, "bench", string(hash)); err != nil {
		b.Fatalf("create user: %v", err)
	}
	g.VerifyCredentials(ctx, "bench", "s3cret")

	b.ReportAllocs()
	for b.Loop() {
		if _, ok := g.VerifyCredentials(ctx, "bench", "s3cret"); !ok {
			b.Fatal("verify failed")
		}
	}
}
