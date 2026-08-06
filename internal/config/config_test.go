package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadServer_ReportsMalformedValues(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"PROXYWI_RATE_LIMIT_MAX", "abc"},
		{"PROXYWI_RATE_LIMIT_MAX", "0"},
		{"PROXYWI_RATE_LIMIT_MAX", "-3"},
		{"PROXYWI_RATE_LIMIT_WINDOW", "soon"},
		{"PROXYWI_RATE_LIMIT_WINDOW", "-1s"},
		{"PROXYWI_SOCKS_MAX_CONNS", "lots"},
		{"PROXYWI_PORT", "not-a-port"},
		{"PROXYWI_PORT", "70000"},
		{"PROXYWI_PROXY_HTTP_PORT", "0"},
		{"PROXYWI_LOG_LEVEL", "verbose"},
		{"PROXYWI_PROXY_PROTOCOL", "maybe"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := LoadServer()
			if err == nil {
				t.Fatal("malformed value accepted silently")
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error %q does not name %s", err, tc.key)
			}
		})
	}
}

func TestLoadServer_ReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("PROXYWI_RATE_LIMIT_MAX", "abc")
	t.Setenv("PROXYWI_LOG_LEVEL", "verbose")
	t.Setenv("PROXYWI_PORT", "99999")

	_, err := LoadServer()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"PROXYWI_RATE_LIMIT_MAX", "PROXYWI_LOG_LEVEL", "PROXYWI_PORT"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error is missing %s:\n%v", key, err)
		}
	}
}

func TestLoadServer_Defaults(t *testing.T) {
	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("clean environment should load: %v", err)
	}
	if cfg.MainAddr != ":3000" || cfg.HTTPAddr != ":8080" || cfg.SOCKSAddr != ":1080" {
		t.Fatalf("unexpected addresses: %q %q %q", cfg.MainAddr, cfg.HTTPAddr, cfg.SOCKSAddr)
	}
	if cfg.RateLimitMax != 5 || cfg.RateLimitWindow != time.Second {
		t.Fatalf("unexpected rate limit: %d/%v", cfg.RateLimitMax, cfg.RateLimitWindow)
	}
	if cfg.SOCKSMaxConns != 1000 {
		t.Fatalf("SOCKSMaxConns = %d, want 1000", cfg.SOCKSMaxConns)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if len(cfg.DoHProviders) != 2 {
		t.Fatalf("DoHProviders = %v, want 2 entries", cfg.DoHProviders)
	}
}

func TestLoadServer_AcceptsValidOverrides(t *testing.T) {
	t.Setenv("PROXYWI_PORT", "8443")
	t.Setenv("PROXYWI_RATE_LIMIT_MAX", "50")
	t.Setenv("PROXYWI_RATE_LIMIT_WINDOW", "250ms")
	t.Setenv("PROXYWI_SOCKS_MAX_CONNS", "4096")
	t.Setenv("PROXYWI_LOG_LEVEL", "debug")
	t.Setenv("PROXYWI_PROXY_PROTOCOL", "yes")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	if cfg.MainAddr != ":8443" {
		t.Fatalf("MainAddr = %q", cfg.MainAddr)
	}
	if cfg.RateLimitMax != 50 || cfg.RateLimitWindow != 250*time.Millisecond {
		t.Fatalf("rate limit = %d/%v", cfg.RateLimitMax, cfg.RateLimitWindow)
	}
	if cfg.SOCKSMaxConns != 4096 {
		t.Fatalf("SOCKSMaxConns = %d", cfg.SOCKSMaxConns)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if !cfg.ProxyProtocol {
		t.Fatal("ProxyProtocol should be true")
	}
}

func TestLoadClient_RequiresServerAndToken(t *testing.T) {
	t.Setenv("PROXYWI_SERVER", "")
	t.Setenv("PROXYWI_TOKEN", "")
	_, err := LoadClient()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"PROXYWI_SERVER", "PROXYWI_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error is missing %s:\n%v", want, err)
		}
	}
}

func TestLoadClient_RejectsNonWebSocketURL(t *testing.T) {
	t.Setenv("PROXYWI_SERVER", "https://proxy.example.com")
	t.Setenv("PROXYWI_TOKEN", "tok")
	if _, err := LoadClient(); err == nil {
		t.Fatal("https:// URL should be rejected")
	}
}

func TestLoadClient_Valid(t *testing.T) {
	t.Setenv("PROXYWI_SERVER", "wss://proxy.example.com")
	t.Setenv("PROXYWI_TOKEN", "tok")
	t.Setenv("PROXYWI_ALLOWED_TARGETS", " 10.0.0.0/8 , ,example.com ")

	cfg, err := LoadClient()
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	// splitEnv trims and drops empties.
	if len(cfg.AllowedTargets) != 2 || cfg.AllowedTargets[0] != "10.0.0.0/8" || cfg.AllowedTargets[1] != "example.com" {
		t.Fatalf("AllowedTargets = %#v", cfg.AllowedTargets)
	}
}
