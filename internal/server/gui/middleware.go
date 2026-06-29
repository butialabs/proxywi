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
		SameSite: http.SameSiteLaxMode,
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
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
}

func (g *GUI) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}
