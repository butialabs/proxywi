package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"log/slog"
	"sync"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

var dummyHash = []byte("$2a$10$ZrXPhDXoVeA6s2WZeZDPL.EqW7ZjHuJic08/3AK/5a7vYztLiwjMK")

const credCacheTTL = 10 * time.Minute

type AuthGate struct {
	Store           *storage.Store
	Log             *slog.Logger
	Limiter         *RateLimiter
	RateLimitMax    int
	RateLimitWindow time.Duration

	credSaltOnce sync.Once
	credSalt     [32]byte
	credCache    sync.Map // string -> time.Time (when the pair was verified)
}

func DefaultAuthGate(ctx context.Context, store *storage.Store, log *slog.Logger, limit int, window time.Duration) *AuthGate {
	g := &AuthGate{
		Store:           store,
		Log:             log,
		Limiter:         NewRateLimiter(ctx),
		RateLimitMax:    limit,
		RateLimitWindow: window,
	}
	go g.sweepCredCache(ctx, credCacheTTL)
	return g
}

func (g *AuthGate) credKey(passwordHash, password string) string {
	g.credSaltOnce.Do(func() {
		_, _ = rand.Read(g.credSalt[:])
	})
	m := hmac.New(sha256.New, g.credSalt[:])
	m.Write([]byte(passwordHash))
	m.Write([]byte{0})
	m.Write([]byte(password))
	return string(m.Sum(nil))
}

func (g *AuthGate) recentlyVerified(key string) bool {
	v, ok := g.credCache.Load(key)
	if !ok {
		return false
	}
	at, ok := v.(time.Time)
	if !ok || time.Since(at) >= credCacheTTL {
		g.credCache.Delete(key)
		return false
	}
	return true
}

func (g *AuthGate) sweepCredCache(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.credCache.Range(func(k, v any) bool {
				if at, ok := v.(time.Time); !ok || time.Since(at) >= credCacheTTL {
					g.credCache.Delete(k)
				}
				return true
			})
		}
	}
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

	key := g.credKey(u.PasswordHash, password)
	if g.recentlyVerified(key) {
		return u, true
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return u, false
	}
	g.credCache.Store(key, time.Now())
	return u, true
}

// AllowProxyRequest reports whether user is within its request allowance.
func (g *AuthGate) AllowProxyRequest(user string) bool {
	if g.Limiter == nil {
		return true
	}
	return g.Limiter.Allow("proxy:"+user, g.RateLimitMax, g.RateLimitWindow)
}
