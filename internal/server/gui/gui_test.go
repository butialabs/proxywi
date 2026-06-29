package gui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/butialabs/proxywi/internal/server"
	"github.com/butialabs/proxywi/internal/storage"
)

func TestAnonymizeTarget(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"google.com:443", "goo***.*om:443"},
		{"api.example.com:80", "*pi.exa****.*om:80"},
		{"a.b", "*.*"},
		{"localhost", "loc******"},
		{"[::1]:443", "[****]:443"},
		{"192.168.1.1:443", "*92.*68.*.*:443"},
	}
	for _, c := range cases {
		got := anonymizeTarget(c.in)
		if got != c.want {
			t.Errorf("anonymizeTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAuthRateLimit_AllowsThenBlocks(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	g := &GUI{Store: store, Limiter: server.NewRateLimiter()}
	h := g.authRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rr.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}
