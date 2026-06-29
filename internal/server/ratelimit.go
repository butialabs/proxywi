package server

import (
	"sync"
	"time"
)

type bucket struct {
	window time.Time
	count  int
}

type RateLimiter struct {
	mu     sync.RWMutex
	buckets map[string]*bucket
}

func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{buckets: map[string]*bucket{}}
	go rl.cleanup(5 * time.Minute)
	return rl
}

func (r *RateLimiter) Allow(key string, max int, window time.Duration) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.buckets[key]
	current := now.Truncate(window)
	if !ok || b.window != current {
		r.buckets[key] = &bucket{window: current, count: 1}
		return true
	}
	if b.count >= max {
		return false
	}
	b.count++
	return true
}

func (r *RateLimiter) cleanup(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		now := time.Now()
		for k, b := range r.buckets {
			if now.Sub(b.window) > 10*time.Minute {
				delete(r.buckets, k)
			}
		}
		r.mu.Unlock()
}
}
