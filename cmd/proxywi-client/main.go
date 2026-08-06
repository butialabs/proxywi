package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/butialabs/proxywi/internal/client"
	"github.com/butialabs/proxywi/internal/config"
	"github.com/butialabs/proxywi/internal/version"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("client exited", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context) error {
	cfg, err := config.LoadClient()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("proxywi client starting", "version", version.Version, "log_level", cfg.LogLevel)

	serverURL := strings.TrimRight(cfg.Server, "/")
	agent := &client.Agent{
		ServerURL:      serverURL,
		Token:          cfg.Token,
		TLSInsecure:    cfg.TLSInsecure,
		AllowedTargets: cfg.AllowedTargets,
		DeniedTargets:  cfg.DeniedTargets,
		DoHProviders:   cfg.DoHProviders,
		AgentVersion:   version.Version,
		Log:            log,
	}

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		start := time.Now()
		if err := agent.Run(ctx); err != nil {
			log.Warn("tunnel dropped", "err", err)
		} else {
			log.Info("tunnel closed cleanly")
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}
