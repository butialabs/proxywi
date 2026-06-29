package main

import (
	"context"
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
	"github.com/pires/go-proxyproto"
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
	fmt.Fprintf(os.Stderr, `proxywi-server

usage:
  proxywi-server                run the full server (GUI + HTTP proxy + SOCKS5 proxy + WSS control)
  proxywi-server admin-set ...  rotate admin credentials (see --help)
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

	cfg.IPHashSecret, err = config.LoadOrCreateIPHashSecret(cfg.DataDir)
	if err != nil {
		return err
	}

	if err := store.NormalizeLegacyClientNames(ctx); err != nil {
		log.Warn("normalize client names", "err", err)
	}

	if n, err := store.CountAdmins(ctx); err == nil && n == 0 {
		log.Info("no admin configured yet, first GUI request will prompt for setup", "main_domain", cfg.MainDomain)
	}

	reg := server.NewRegistry()
	gate := server.DefaultAuthGate(store, log, cfg.IPHashSecret)
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
	socksProxy := &server.SOCKSProxy{
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
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	guiRouter := guiApp.Router()
	guiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/control" {
			controlHandler.ServeHTTP(w, r)
			return
		}
		guiRouter.ServeHTTP(w, r)
	})

	guiSrv := &http.Server{
		Addr:              cfg.MainAddr,
		Handler:           guiHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	httpLn, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen http %s: %w", cfg.HTTPAddr, err)
	}
	socksLn, err := net.Listen("tcp", cfg.SOCKSAddr)
	if err != nil {
		return fmt.Errorf("listen socks %s: %w", cfg.SOCKSAddr, err)
	}
	if cfg.ProxyProtocol {
		log.Info("PROXY protocol enabled on proxy listeners", "http", cfg.HTTPAddr, "socks", cfg.SOCKSAddr)
		httpLn = &proxyproto.Listener{Listener: httpLn}
		socksLn = &proxyproto.Listener{Listener: socksLn}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- serveListener(srv, httpLn, log, "http-proxy")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- serveHTTP(guiSrv, cfg.MainAddr, log, "gui+control")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("listening", "addr", cfg.SOCKSAddr, "role", "socks-proxy")
		if err := socksProxy.Serve(socksLn); err != nil && !errors.Is(err, net.ErrClosed) {
			errCh <- err
		}
	}()

	wg.Add(1)
	go func() { defer wg.Done(); runRetentionSweep(ctx, store, log) }()

	log.Info("proxywi server up",
		"http", cfg.HTTPAddr, "socks", cfg.SOCKSAddr, "main", cfg.MainAddr,
		"main_domain", cfg.MainDomain, "proxy_domain", cfg.ProxyDomain,
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
	_ = socksLn.Close()
	wg.Wait()
	return nil
}

func serveHTTP(srv *http.Server, addr string, log *slog.Logger, role string) error {
	log.Info("listening", "addr", addr, "role", role)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveListener(srv *http.Server, ln net.Listener, log *slog.Logger, role string) error {
	log.Info("listening", "addr", ln.Addr().String(), "role", role)
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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
