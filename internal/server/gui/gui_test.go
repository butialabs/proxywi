package gui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/butialabs/proxywi/internal/server"
	"github.com/butialabs/proxywi/internal/storage"
)


func TestAuthRateLimit_AllowsThenBlocks(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

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
