package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	HTTPAddr        string
	SOCKSAddr       string
	MainAddr        string
	MainDomain      string
	DataDir         string
	ProxyProtocol   bool
	AdminUsername   string
	AdminPassword   string
	RateLimitMax    int
	RateLimitWindow time.Duration
	BlockCategories []string
	DoHProviders    []string
	Timezone        string
}

type Client struct {
	Server         string
	Token          string
	TLSInsecure    bool
	AllowedTargets []string
	DeniedTargets  []string
	DoHProviders   []string
}

func LoadServer() (Server, error) {
	s := Server{
		HTTPAddr:        ":" + env("PROXYWI_PROXY_HTTP_PORT", "8080"),
		SOCKSAddr:       ":" + env("PROXYWI_PROXY_SOCKET_PORT", "1080"),
		MainAddr:        ":" + env("PROXYWI_PORT", "3000"),
		MainDomain:      env("PROXYWI_DOMAIN", ""),
		DataDir:         env("PROXYWI_DATA_DIR", "./data"),
		ProxyProtocol:   boolEnv("PROXYWI_PROXY_PROTOCOL"),
		AdminUsername:   os.Getenv("ADMIN_USERNAME"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		RateLimitMax:    intEnv("PROXYWI_RATE_LIMIT_MAX", 5),
		RateLimitWindow: durationEnv("PROXYWI_RATE_LIMIT_WINDOW", time.Second),
		BlockCategories: splitEnv(os.Getenv("PROXYWI_BLOCK_CATEGORIES")),
		DoHProviders:    splitEnv(env("PROXYWI_DOH_PROVIDERS", "https://dns.nextdns.io,https://freedns.controld.com/p0")),
		Timezone:        env("TZ", "UTC"),
	}
	return s, nil
}


func LoadClient() (Client, error) {
	c := Client{
		Server:         os.Getenv("PROXYWI_SERVER"),
		Token:          os.Getenv("PROXYWI_TOKEN"),
		TLSInsecure:    boolEnv("PROXYWI_TLS_INSECURE"),
		AllowedTargets: splitEnv(os.Getenv("PROXYWI_ALLOWED_TARGETS")),
		DeniedTargets:  splitEnv(os.Getenv("PROXYWI_DENIED_TARGETS")),
		DoHProviders:  splitEnv(env("PROXYWI_DOH_PROVIDERS", "https://dns.nextdns.io,https://freedns.controld.com/p0")),
	}
	if c.Server == "" {
		return c, fmt.Errorf("PROXYWI_SERVER is required")
	}
	if c.Token == "" {
		return c, fmt.Errorf("PROXYWI_TOKEN is required")
	}
	if !strings.HasPrefix(c.Server, "ws://") && !strings.HasPrefix(c.Server, "wss://") {
		return c, fmt.Errorf("PROXYWI_SERVER must start with ws:// or wss://")
	}
	return c, nil
}

func splitEnv(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func boolEnv(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func durationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
