package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var dummyHash []byte

func init() {
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unlikely-password-never-matches"), bcrypt.DefaultCost)
}

type AuthGate struct {
	Store   *storage.Store
	Log     *slog.Logger
	Limiter *RateLimiter
}

func DefaultAuthGate(store *storage.Store, log *slog.Logger) *AuthGate {
	return &AuthGate{Store: store, Log: log, Limiter: NewRateLimiter()}
}

func (g *AuthGate) VerifyCredentials(ctx context.Context, username, password string) (*storage.User, bool) {
	u, err := g.Store.UserByUsername(ctx, username)
	if err != nil {
		g.Log.Error("user lookup", "err", err)
	}
	if u == nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return u, false
	}
	return u, true
}

func (g *AuthGate) AllowProxyRequest(user string) bool {
	if g.Limiter == nil {
		return true
	}
	return g.Limiter.Allow("proxy:"+user, 5, time.Second)
}

type authError string

func (e authError) Error() string { return string(e) }

const (
	ErrBadCreds = authError("invalid credentials")
)
