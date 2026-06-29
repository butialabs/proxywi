package config

import (
	"fmt"
	"os"
	"strings"
)

type Server struct {
	HTTPAddr      string
	SOCKSAddr     string
	MainAddr      string
	MainDomain    string
	ProxyDomain   string
	DataDir       string
	ProxyProtocol bool
	AdminUsername string
	AdminPassword string
}

type Client struct {
	Server         string
	Token          string
	TLSInsecure    bool
	AllowedTargets []string
	DeniedTargets  []string
}

func LoadServer() (Server, error) {
	s := Server{
		HTTPAddr:      ":" + env("PROXYWI_PROXY_HTTP_PORT", "8080"),
		SOCKSAddr:     ":" + env("PROXYWI_PROXY_SOCKET_PORT", "1080"),
		MainAddr:      ":" + env("PROXYWI_MAIN_PORT", "3000"),
		MainDomain:    env("PROXYWI_MAIN_DOMAIN", "proxywi.xyz"),
		ProxyDomain:   env("PROXYWI_PROXY_DOMAIN", "pomar.proxywi.xyz"),
		DataDir:       env("PROXYWI_DATA_DIR", "./data"),
		ProxyProtocol: boolEnv("PROXYWI_PROXY_PROTOCOL"),
		AdminUsername: os.Getenv("ADMIN_USERNAME"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
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
