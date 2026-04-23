package config

import (
	"fmt"
	"os"
	"strings"
)

type Server struct {
	ListenAddr  string
	GUIAddr     string
	GUIDomain   string
	ProxyDomain string
	DataDir     string
	TLSMode     string
	TLSCertFile string
	TLSKeyFile  string
	TLSCacheDir string
	ACMEEmail   string
}

type Client struct {
	Server string
	Token  string
	Name   string
}

func LoadServer() (Server, error) {
	s := Server{
		ListenAddr:  env("PROXYWI_LISTEN_ADDR", ":7443"),
		GUIAddr:     env("PROXYWI_GUI_ADDR", ":3000"),
		GUIDomain:   env("PROXYWI_GUI_DOMAIN", "proxywi.xyz"),
		ProxyDomain: env("PROXYWI_PROXY_DOMAIN", "pomar.proxywi.xyz"),
		DataDir:     env("PROXYWI_DATA_DIR", "./data"),
		TLSMode:     env("PROXYWI_TLS_MODE", "off"),
		TLSCertFile: os.Getenv("PROXYWI_TLS_CERT_FILE"),
		TLSKeyFile:  os.Getenv("PROXYWI_TLS_KEY_FILE"),
		TLSCacheDir: env("PROXYWI_TLS_CACHE_DIR", "./data/acme"),
		ACMEEmail:   os.Getenv("PROXYWI_ACME_EMAIL"),
	}
	valid := map[string]bool{"off": true, "autocert": true, "manual": true}
	if !valid[s.TLSMode] {
		return s, fmt.Errorf("PROXYWI_TLS_MODE=%q invalid (off|autocert|manual)", s.TLSMode)
	}
	if s.TLSMode == "manual" && (s.TLSCertFile == "" || s.TLSKeyFile == "") {
		return s, fmt.Errorf("PROXYWI_TLS_MODE=manual requires PROXYWI_TLS_CERT_FILE and PROXYWI_TLS_KEY_FILE")
	}
	return s, nil
}

func LoadClient() (Client, error) {
	c := Client{
		Server: os.Getenv("PROXYWI_SERVER"),
		Token:  os.Getenv("PROXYWI_TOKEN"),
		Name:   env("PROXYWI_CLIENT_NAME", hostname()),
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

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unnamed-client"
	}
	return h
}
