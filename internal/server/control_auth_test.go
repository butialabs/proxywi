package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func newAuthTestControl(t *testing.T) *Control {
	t.Helper()
	store, err := storage.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Control{
		Store: store,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestAuthenticateToken_LegacyBcryptOnlyStillWorks(t *testing.T) {
	c := newAuthTestControl(t)
	ctx := context.Background()

	const token = "0123456789abcdef0123456789abcdef"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	want, err := c.Store.CreateClient(ctx, "legacy-old-agent", string(hash), "", "")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	got, cl, err := c.authenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("legacy token rejected: %v", err)
	}
	if got != want {
		t.Fatalf("client id = %d, want %d", got, want)
	}
	if cl == nil || cl.Name != "legacy-old-agent" {
		t.Fatalf("unexpected client %+v", cl)
	}
}

func TestAuthenticateToken_BcryptWithTokenIDUsesFastPath(t *testing.T) {
	c := newAuthTestControl(t)
	ctx := context.Background()

	const token = "fedcba9876543210fedcba9876543210"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	want, err := c.Store.CreateClient(ctx, "agent-with-token-id", string(hash), "", storage.TokenIDFromToken(token))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	got, _, err := c.authenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("token rejected: %v", err)
	}
	if got != want {
		t.Fatalf("client id = %d, want %d", got, want)
	}
}

func TestAuthenticateToken_SHA256FastPath(t *testing.T) {
	c := newAuthTestControl(t)
	ctx := context.Background()

	const token = "aaaabbbbccccddddaaaabbbbccccdddd"
	want, err := c.Store.CreateClient(ctx, "modern-agent", "", storage.SHA256Token(token), storage.TokenIDFromToken(token))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	got, _, err := c.authenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("token rejected: %v", err)
	}
	if got != want {
		t.Fatalf("client id = %d, want %d", got, want)
	}
}

func TestAuthenticateToken_InvalidTokenRejected(t *testing.T) {
	c := newAuthTestControl(t)
	ctx := context.Background()

	const token = "11112222333344441111222233334444"
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := c.Store.CreateClient(ctx, "some-registered-agent", string(hash), "", ""); err != nil {
		t.Fatalf("create client: %v", err)
	}

	if _, _, err := c.authenticateToken(ctx, "not-the-right-token-at-all-0000000"); err == nil {
		t.Fatal("invalid token was accepted")
	}
}

func TestAuthenticateToken_InvalidTokenDoesNotScaleWithClientCount(t *testing.T) {
	c := newAuthTestControl(t)
	ctx := context.Background()

	for i := range 12 {
		token := string(rune('a'+i)) + "000000000000000" + string(rune('a'+i))
		hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if _, err := c.Store.CreateClient(ctx, "agent-"+string(rune('a'+i)), string(hash), "", storage.TokenIDFromToken(token)); err != nil {
			t.Fatalf("create client: %v", err)
		}
	}

	hashes, err := c.Store.AllClientTokenHashes(ctx)
	if err != nil {
		t.Fatalf("hashes: %v", err)
	}
	for id, h := range hashes {
		if h.TokenID == "" {
			t.Fatalf("client %d unexpectedly has no token_id; test premise broken", id)
		}
	}

	if _, _, err := c.authenticateToken(ctx, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); err == nil {
		t.Fatal("invalid token was accepted")
	}
}
