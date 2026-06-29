package server

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter()
	key := "user:alice"
	for i := 0; i < 5; i++ {
		if !rl.Allow(key, 5, time.Second) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter()
	key := "user:bob"
	for i := 0; i < 5; i++ {
		rl.Allow(key, 5, time.Second)
	}
	if rl.Allow(key, 5, time.Second) {
		t.Fatal("6th request within the same second should be blocked")
	}
}

func TestRateLimiter_ResetsAfterWindow(t *testing.T) {
	rl := NewRateLimiter()
	key := "user:carol"
	for i := 0; i < 5; i++ {
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
	rl := NewRateLimiter()
	key := "auth:10.0.0.1"
	for i := 0; i < 10; i++ {
		if !rl.Allow(key, 10, time.Minute) {
			t.Fatalf("auth request %d should be allowed", i+1)
		}
	}
	if rl.Allow(key, 10, time.Minute) {
		t.Fatal("11th auth request within a minute should be blocked")
	}
}
