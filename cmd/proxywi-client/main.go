// Command proxywi-client is the agent that connects to a Proxywi server and forwards proxy traffic.
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
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, log); err != nil {
		log.Error("client exited", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.LoadClient()
	if err != nil {
		return err
	}
	serverURL := strings.TrimRight(cfg.Server, "/")
	agent := &client.Agent{
		ServerURL:   serverURL,
		Token:       cfg.Token,
		Name:        cfg.Name,
		TLSInsecure: cfg.TLSInsecure,
		Log:         log,
	}

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
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
