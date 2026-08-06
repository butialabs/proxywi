package server

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter(t.Context())
	key := "user:alice"
	for i := range 5 {
		if !rl.Allow(key, 5, time.Second) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(t.Context())
	key := "user:bob"
	for range 5 {
		rl.Allow(key, 5, time.Second)
	}
	if rl.Allow(key, 5, time.Second) {
		t.Fatal("6th request within the same second should be blocked")
	}
}

func TestRateLimiter_ResetsAfterWindow(t *testing.T) {
	rl := NewRateLimiter(t.Context())
	key := "user:carol"
	for range 5 {
		rl.Allow(key, 5, 50*time.Millisecond)
	}
	if rl.Allow(key, 5, 50*time.Millisecond) {
		t.Fatal("6th request should be blocked")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow(key, 5, 50*time.Millisecond) {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestRateLimiter_AuthWindowTenPerMinute(t *testing.T) {
	rl := NewRateLimiter(t.Context())
	key := "auth:10.0.0.1"
	for i := range 10 {
		if !rl.Allow(key, 10, time.Minute) {
			t.Fatalf("auth request %d should be allowed", i+1)
		}
	}
	if rl.Allow(key, 10, time.Minute) {
		t.Fatal("11th auth request within a minute should be blocked")
	}
}

func TestRateLimiter_NoDoubleBurstAcrossWindowBoundary(t *testing.T) {
	rl := NewRateLimiter(t.Context())
	const key = "user:dave"
	const limit = 5
	const window = time.Second

	boundary := time.Now().Add(window).Truncate(window)
	time.Sleep(time.Until(boundary.Add(-30 * time.Millisecond)))

	for i := range limit {
		if !rl.Allow(key, limit, window) {
			t.Fatalf("request %d before the boundary should be allowed", i+1)
		}
	}

	time.Sleep(60 * time.Millisecond)

	if rl.Allow(key, limit, window) {
		t.Fatal("allowance refilled on the window boundary; a 2x burst is possible")
	}
}
