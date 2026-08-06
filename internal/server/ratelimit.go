package server

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*keyLimiter
}

type keyLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewRateLimiter(ctx context.Context) *RateLimiter {
	rl := &RateLimiter{limiters: make(map[string]*keyLimiter)}
	go rl.evictIdle(ctx, 5*time.Minute)
	return rl
}

func (r *RateLimiter) Allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 || window <= 0 {
		return true
	}
	now := time.Now()

	r.mu.Lock()
	kl, ok := r.limiters[key]
	if !ok {
		kl = &keyLimiter{limiter: rate.NewLimiter(rate.Limit(float64(limit)/window.Seconds()), limit)}
		r.limiters[key] = kl
	}
	kl.lastSeen = now
	limiter := kl.limiter
	r.mu.Unlock()

	return limiter.AllowN(now, 1)
}

func (r *RateLimiter) evictIdle(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-10 * time.Minute)
			r.mu.Lock()
			for k, kl := range r.limiters {
				if kl.lastSeen.Before(cutoff) {
					delete(r.limiters, k)
				}
			}
			r.mu.Unlock()
		}
	}
}
