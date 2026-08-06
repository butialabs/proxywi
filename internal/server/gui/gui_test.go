package gui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/butialabs/proxywi/internal/server"
	"github.com/butialabs/proxywi/internal/storage"
	"github.com/go-chi/chi/v5"
)

func TestAuthRateLimit_AllowsThenBlocks(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	g := &GUI{Store: store, Limiter: server.NewRateLimiter(t.Context())}
	h := g.authRateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := range 10 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rr.Code)
		}
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestPostDelete_ReportsFailureInsteadOfRedirecting(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		handler func(*GUI) http.HandlerFunc
	}{
		{"client", "/clients/1/delete", func(g *GUI) http.HandlerFunc { return g.postDeleteClient }},
		{"access", "/access/1/delete", func(g *GUI) http.HandlerFunc { return g.postDeleteAccess }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			g := &GUI{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

			if err := store.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "1")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rr := httptest.NewRecorder()
			tc.handler(g)(rr, req)

			if rr.Code == http.StatusFound {
				t.Fatal("failed delete redirected as if it had succeeded")
			}
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("got %d, want %d", rr.Code, http.StatusInternalServerError)
			}
		})
	}
}
