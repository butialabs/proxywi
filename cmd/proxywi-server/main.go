// Command proxywi-server runs the Proxywi server: GUI, HTTPS proxy, and the WSS control plane
// all multiplexed on a single TLS listener.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/butialabs/proxywi/internal/config"
	"github.com/butialabs/proxywi/internal/server"
	"github.com/butialabs/proxywi/internal/server/gui"
	"github.com/butialabs/proxywi/internal/storage"
	"github.com/butialabs/proxywi/internal/tunnel"
	"golang.org/x/crypto/acme/autocert"
)

// runRetentionSweep trims aggregates (30 days, every 6h) and the request log (24h, every 15m)
const (
	retentionWindow    = 30 * 24 * time.Hour
	logRetentionWindow = 24 * time.Hour
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "admin-set":
			if err := runAdminSet(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			usage()
			return
		case "server", "run":
		default:
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, log); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `proxywi-server — Proxywi proxy pool, central service

usage:
  proxywi-server                run the full server (GUI + HTTPS proxy + WSS control)
  proxywi-server admin-set ...  rotate admin credentials (see --help)

configuration is read from environment variables — see README.md
`)
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	if n, err := store.CountAdmins(ctx); err == nil && n == 0 {
		log.Info("no admin configured yet — first GUI request will prompt for setup", "gui_domain", cfg.GUIDomain)
	}

	reg := server.NewRegistry()
	gate := server.DefaultAuthGate(store, log)
	hub := server.NewHub()

	control := &server.Control{
		Store:    store,
		Registry: reg,
		Log:      log,
		Hub:      hub,
		OnEvent: func(clientID int64, msg tunnel.MetaMessage) {
			if msg.Type == "metrics" {
				if err := store.AddMetricSample(ctx, clientID, time.Now(), msg.BytesIn, msg.BytesOut, msg.ActiveConns); err != nil {
					log.Warn("persist metrics", "err", err)
				}
			}
		},
	}

	httpProxy := &server.HTTPProxy{
		Registry: reg, Gate: gate, Store: store, Log: log, Hub: hub,
	}

	guiApp, err := gui.New(store, reg, hub, cfg, log)
	if err != nil {
		return err
	}

	controlHandler := control.Handler()

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if server.IsProxyRequest(r) {
			httpProxy.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/ws/control":
			controlHandler.ServeHTTP(w, r)
			return
		case "/healthz":
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}

	guiSrv := &http.Server{
		Addr:              cfg.GUIAddr,
		Handler:           guiApp.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	tlsCfg, acmeHTTP, err := buildTLSConfig(cfg, log)
	if err != nil {
		return err
	}
	srv.TLSConfig = tlsCfg

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- serve(srv, cfg, log)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("gui listening", "addr", cfg.GUIAddr)
		err := guiSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var acmeSrv *http.Server
	if acmeHTTP != nil {
		acmeSrv = &http.Server{
			Addr:              ":80",
			Handler:           acmeHTTP,
			ReadHeaderTimeout: 10 * time.Second,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("acme http-01 listening", "addr", ":80")
			if err := acmeSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	wg.Add(1)
	go func() { defer wg.Done(); runRetentionSweep(ctx, store, log) }()

	log.Info("proxywi server up",
		"listen", cfg.ListenAddr, "tls", cfg.TLSMode, "gui", cfg.GUIAddr,
		"gui_domain", cfg.GUIDomain, "proxy_domain", cfg.ProxyDomain,
		"retention_days", int(retentionWindow/(24*time.Hour)))

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("subsystem failed", "err", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = guiSrv.Shutdown(shutdownCtx)
	if acmeSrv != nil {
		_ = acmeSrv.Shutdown(shutdownCtx)
	}
	wg.Wait()
	return nil
}

func serve(srv *http.Server, cfg config.Server, log *slog.Logger) error {
	label := cfg.TLSMode
	log.Info("listening", "addr", cfg.ListenAddr, "tls", label)

	switch cfg.TLSMode {
	case "off":
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case "manual", "autocert":
		ln, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
		}
		tlsLn := tls.NewListener(ln, srv.TLSConfig)
		err = srv.Serve(tlsLn)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return fmt.Errorf("unknown TLS mode %q", cfg.TLSMode)
}

func buildTLSConfig(cfg config.Server, log *slog.Logger) (*tls.Config, http.Handler, error) {
	switch cfg.TLSMode {
	case "off":
		return nil, nil, nil
	case "manual":
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("load tls keypair: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
		}, nil, nil
	case "autocert":
		hosts := []string{cfg.GUIDomain, cfg.ProxyDomain}
		mgr := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(hosts...),
			Cache:      autocert.DirCache(cfg.TLSCacheDir),
			Email:      cfg.ACMEEmail,
		}
		log.Info("autocert enabled", "hosts", hosts, "cache", cfg.TLSCacheDir)
		return mgr.TLSConfig(), mgr.HTTPHandler(nil), nil
	}
	return nil, nil, fmt.Errorf("unknown TLS mode %q", cfg.TLSMode)
}

func runRetentionSweep(ctx context.Context, store *storage.Store, log *slog.Logger) {
	longSweep := func() {
		cutoff := time.Now().Add(-retentionWindow)
		n, err := store.PurgeOldData(ctx, cutoff)
		if err != nil {
			log.Warn("retention sweep (long)", "err", err)
			return
		}
		if n > 0 {
			log.Info("retention sweep (long) purged rows", "deleted", n, "older_than", cutoff.Format(time.RFC3339))
		}
	}
	logSweep := func() {
		cutoff := time.Now().Add(-logRetentionWindow)
		n, err := store.PurgeProxyEventsOlderThan(ctx, cutoff)
		if err != nil {
			log.Warn("retention sweep (log)", "err", err)
			return
		}
		if n > 0 {
			log.Info("retention sweep (log) purged rows", "deleted", n, "older_than", cutoff.Format(time.RFC3339))
		}
	}

	longSweep()
	logSweep()

	longTicker := time.NewTicker(6 * time.Hour)
	defer longTicker.Stop()
	logTicker := time.NewTicker(15 * time.Minute)
	defer logTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-longTicker.C:
			longSweep()
		case <-logTicker.C:
			logSweep()
		}
	}
}
