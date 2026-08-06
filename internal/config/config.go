package config

import (
	"errors"
	"fmt"
	"log/slog"
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
	SOCKSMaxConns   int
	BlockCategories []string
	DoHProviders    []string
	Timezone        string
	LogLevel        slog.Level
}

type Client struct {
	Server         string
	Token          string
	TLSInsecure    bool
	AllowedTargets []string
	DeniedTargets  []string
	DoHProviders   []string
	LogLevel       slog.Level
}

const defaultDoHProviders = "https://dns.nextdns.io,https://freedns.controld.com/p0"

type loader struct{ errs []error }

func (l *loader) fail(key, value string, err error) {
	l.errs = append(l.errs, fmt.Errorf("%s=%q: %w", key, value, err))
}

func (l *loader) err() error { return errors.Join(l.errs...) }

func (l *loader) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (l *loader) boolean(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	l.fail(key, v, errors.New("want one of 1/0, true/false, yes/no, on/off"))
	return false
}

func (l *loader) integer(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.fail(key, v, errors.New("want a whole number"))
		return def
	}
	if n <= 0 {
		l.fail(key, v, errors.New("want a positive number"))
		return def
	}
	return n
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(key, v, errors.New(`want a duration such as "500ms", "10s" or "2m"`))
		return def
	}
	if d <= 0 {
		l.fail(key, v, errors.New("want a positive duration"))
		return def
	}
	return d
}

func (l *loader) port(key, def string) string {
	v := l.str(key, def)
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		l.fail(key, v, errors.New("want a port between 1 and 65535"))
		return ":" + def
	}
	return ":" + v
}

func (l *loader) logLevel(key string, def slog.Level) slog.Level {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		l.fail(key, v, errors.New("want one of debug, info, warn, error"))
		return def
	}
	return lvl
}

func LoadServer() (Server, error) {
	var l loader
	s := Server{
		HTTPAddr:        l.port("PROXYWI_PROXY_HTTP_PORT", "8080"),
		SOCKSAddr:       l.port("PROXYWI_PROXY_SOCKET_PORT", "1080"),
		MainAddr:        l.port("PROXYWI_PORT", "3000"),
		MainDomain:      l.str("PROXYWI_DOMAIN", ""),
		DataDir:         l.str("PROXYWI_DATA_DIR", "./data"),
		ProxyProtocol:   l.boolean("PROXYWI_PROXY_PROTOCOL"),
		AdminUsername:   os.Getenv("ADMIN_USERNAME"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		RateLimitMax:    l.integer("PROXYWI_RATE_LIMIT_MAX", 5),
		RateLimitWindow: l.duration("PROXYWI_RATE_LIMIT_WINDOW", time.Second),
		SOCKSMaxConns:   l.integer("PROXYWI_SOCKS_MAX_CONNS", 1000),
		BlockCategories: splitEnv(os.Getenv("PROXYWI_BLOCK_CATEGORIES")),
		DoHProviders:    splitEnv(l.str("PROXYWI_DOH_PROVIDERS", defaultDoHProviders)),
		Timezone:        l.str("TZ", "UTC"),
		LogLevel:        l.logLevel("PROXYWI_LOG_LEVEL", slog.LevelInfo),
	}
	return s, l.err()
}

func LoadClient() (Client, error) {
	var l loader
	c := Client{
		Server:         os.Getenv("PROXYWI_SERVER"),
		Token:          os.Getenv("PROXYWI_TOKEN"),
		TLSInsecure:    l.boolean("PROXYWI_TLS_INSECURE"),
		AllowedTargets: splitEnv(os.Getenv("PROXYWI_ALLOWED_TARGETS")),
		DeniedTargets:  splitEnv(os.Getenv("PROXYWI_DENIED_TARGETS")),
		DoHProviders:   splitEnv(l.str("PROXYWI_DOH_PROVIDERS", defaultDoHProviders)),
		LogLevel:       l.logLevel("PROXYWI_LOG_LEVEL", slog.LevelInfo),
	}
	if c.Server == "" {
		l.errs = append(l.errs, errors.New("PROXYWI_SERVER is required"))
	} else if !strings.HasPrefix(c.Server, "ws://") && !strings.HasPrefix(c.Server, "wss://") {
		l.fail("PROXYWI_SERVER", c.Server, errors.New("want a ws:// or wss:// URL"))
	}
	if c.Token == "" {
		l.errs = append(l.errs, errors.New("PROXYWI_TOKEN is required"))
	}
	return c, l.err()
}

func splitEnv(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
