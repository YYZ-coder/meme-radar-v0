// Command radar is the single binary for all server roles.
// Run with --help to see available flags.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/obs"
	"github.com/meme-radar/radar/internal/store"
)

// version is set at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

func main() {
	// Startup panics are logged with stack and exit non-zero; runtime work is guarded by
	// obs.Go/Loop/Supervise and the HTTP middleware, so it never reaches here.
	defer func() {
		if r := recover(); r != nil {
			_ = obs.Recovered(context.Background(), "main", r)
			os.Exit(2)
		}
	}()
	cfgPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	_, closeLog, err := obs.Setup(obs.LogOptions{
		Level: cfg.Log.Level, File: cfg.Log.File, Service: "radar", Role: cfg.Log.Role, Version: version,
	})
	if err != nil {
		slog.Error("failed to set up logging", "err", err)
		os.Exit(1)
	}
	defer func() { _ = closeLog() }()

	db, err := store.Open(cfg.Database.URL)
	if err != nil {
		slog.Error("failed to connect to database", obs.KeyCode, obs.RuntimeDBUnavailable, "err", err, "url_set", cfg.Database.URL != "")
		os.Exit(1)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}

	srv := httpapi.NewServer(cfg.HTTP.Addr, db)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("radar started", "addr", cfg.HTTP.Addr)
	if err := srv.Run(ctx); err != nil {
		slog.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
	slog.Info("radar stopped")
}
