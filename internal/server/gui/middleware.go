package gui

import (
	"context"
	"net/http"
	"time"
)

type ctxKey int

const (
	adminIDKey  ctxKey = 1
	clientIDKey ctxKey = 2
)

const sessionCookie = "proxywi_session"
const tokenSessionCookie = "proxywi_token_session"
const flashCookie = "proxywi_flash"
const csrfCookie = "proxywi_csrf"

func (g *GUI) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		adminID, ok, err := g.Store.LookupSession(r.Context(), c.Value)
		if err != nil || !ok {
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), adminIDKey, adminID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (g *GUI) requireTokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(tokenSessionCookie)
		if err != nil {
			http.Redirect(w, r, "/token-login", http.StatusFound)
			return
		}
		clientID, ok, err := g.Store.LookupTokenSession(r.Context(), c.Value)
		if err != nil || !ok {
			http.SetCookie(w, &http.Cookie{Name: tokenSessionCookie, Value: "", Path: "/", MaxAge: -1})
			http.Redirect(w, r, "/token-login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), clientIDKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (g *GUI) setTokenSession(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     tokenSessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
}

func (g *GUI) clearTokenSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: tokenSessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func (g *GUI) setSession(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
}

func (g *GUI) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func (g *GUI) setFlashToken(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    token,
		Path:     "/clients",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
}

func (g *GUI) getFlashToken(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/clients", MaxAge: -1})
	return c.Value
}

func (g *GUI) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(csrfCookie)
	if err == nil && c.Value != "" {
		return c.Value
	}
	token := randHex(16)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
	return token
}

func (g *GUI) csrfToken(r *http.Request) string {
	c, err := r.Cookie(csrfCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (g *GUI) validateCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	expected := g.csrfToken(r)
	if expected == "" {
		return false
	}
	_ = r.ParseForm()
	if r.Form.Get("_csrf") != expected {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return false
	}
	return true
}

func (g *GUI) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.validateCSRF(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
