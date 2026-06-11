package server

import (
	"context"
	"crypto/rand"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

var dummyHash []byte

func init() {
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unlikely-password-never-matches"), bcrypt.DefaultCost)
}

type AuthGate struct {
	Store *storage.Store
	Log   *slog.Logger

	PerIPRate       rate.Limit
	PerIPBurst      int
	MinFailureDelay time.Duration
	MaxFailureDelay time.Duration

	BanStep1Count int
	BanStep1      time.Duration
	BanStep2Count int
	BanStep2      time.Duration
	BanStep3Count int
	BanStep3      time.Duration
	Window1       time.Duration
	Window2       time.Duration
	Window3       time.Duration

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func DefaultAuthGate(store *storage.Store, log *slog.Logger) *AuthGate {
	return &AuthGate{
		Store:           store,
		Log:             log,
		PerIPRate:       rate.Every(6 * time.Second),
		PerIPBurst:      5,
		MinFailureDelay: 500 * time.Millisecond,
		MaxFailureDelay: 1500 * time.Millisecond,
		BanStep1Count:   5,
		BanStep1:        5 * time.Minute,
		BanStep2Count:   15,
		BanStep2:        time.Hour,
		BanStep3Count:   50,
		BanStep3:        24 * time.Hour,
		Window1:         10 * time.Minute,
		Window2:         time.Hour,
		Window3:         24 * time.Hour,
		limiters:        map[string]*rate.Limiter{},
	}
}

func (g *AuthGate) limiterFor(ip string) *rate.Limiter {
	g.mu.Lock()
	defer g.mu.Unlock()
	lim, ok := g.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(g.PerIPRate, g.PerIPBurst)
		g.limiters[ip] = lim
	}
	return lim
}

func (g *AuthGate) CheckPreAuth(ctx context.Context, sourceIP string) error {
	ban, err := g.Store.ActiveBan(ctx, sourceIP)
	if err == nil && ban != nil {
		return ErrBanned
	}
	if !g.limiterFor(sourceIP).Allow() {
		return ErrRateLimited
	}
	return nil
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

func (g *AuthGate) VerifySourceAllowed(u *storage.User, sourceIP string) bool {
	if len(u.AllowedSourceCIDRs) == 0 {
		return true
	}
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return false
	}
	for _, c := range u.AllowedSourceCIDRs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (g *AuthGate) RegisterFailure(ctx context.Context, sourceIP, username, protocol string) {
	if err := g.Store.RecordAuthFailure(ctx, sourceIP, username, protocol); err != nil {
		g.Log.Warn("record auth failure", "err", err)
	}

	now := time.Now()
	if n, _ := g.Store.CountAuthFailuresSince(ctx, sourceIP, now.Add(-g.Window3)); n >= g.BanStep3Count {
		_ = g.Store.UpsertBan(ctx, sourceIP, now.Add(g.BanStep3), "auto: threshold 3", n)
	} else if n, _ := g.Store.CountAuthFailuresSince(ctx, sourceIP, now.Add(-g.Window2)); n >= g.BanStep2Count {
		_ = g.Store.UpsertBan(ctx, sourceIP, now.Add(g.BanStep2), "auto: threshold 2", n)
	} else if n, _ := g.Store.CountAuthFailuresSince(ctx, sourceIP, now.Add(-g.Window1)); n >= g.BanStep1Count {
		_ = g.Store.UpsertBan(ctx, sourceIP, now.Add(g.BanStep1), "auto: threshold 1", n)
	}

	g.sleepJitter()
}

func (g *AuthGate) sleepJitter() {
	minMS := int64(g.MinFailureDelay / time.Millisecond)
	maxMS := int64(g.MaxFailureDelay / time.Millisecond)
	if maxMS <= minMS {
		time.Sleep(g.MinFailureDelay)
		return
	}
	r, err := rand.Int(rand.Reader, big.NewInt(maxMS-minMS))
	var d time.Duration
	if err != nil {
		d = g.MinFailureDelay
	} else {
		d = time.Duration(minMS+r.Int64()) * time.Millisecond
	}
	time.Sleep(d)
}

type authError string

func (e authError) Error() string { return string(e) }

const (
	ErrBanned      = authError("source ip banned")
	ErrRateLimited = authError("rate limited")
	ErrBadCreds    = authError("invalid credentials")
	ErrSourceDenied = authError("source ip not allowed for user")
)
