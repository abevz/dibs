package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"path/filepath"

	"github.com/abevz/dibs/internal/api"
	"github.com/abevz/dibs/internal/compat"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/migrations"
)

func main() {
	compat.WarnLegacyBinary("af-coordinatord", "dibsd")
	// Restrict newly created DB/WAL/SHM/lock files. The Unix socket is
	// deliberately widened to 0660 after listen according to the local trust
	// contract.
	syscall.Umask(0o077)
	cfg := config.Default()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))

	// Ensure database directory exists before opening.
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o700); err != nil {
		logger.Error("failed to create database directory", "error", err)
		os.Exit(1)
	}
	if cfg.UsesDefaultDBPath() {
		if err := os.Chmod(filepath.Dir(cfg.DBPath), 0o700); err != nil {
			logger.Error("failed to restrict database directory", "error", err)
			os.Exit(1)
		}
	}
	dbLock, err := api.AcquireDatabaseLock(cfg.DBPath)
	if err != nil {
		logger.Error("failed to acquire database ownership", "error", err)
		os.Exit(1)
	}
	defer dbLock.Close()

	// Open database and run migrations.
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := sqlite.VerifyKnownMigrations(context.Background(), db, migrations.FS); err != nil {
		logger.Error("failed to verify migration state", "error", err)
		os.Exit(1)
	}

	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	cfg.SingletonLockHeld = true
	cfg.MigrationsVerifiedAtStartup = true
	cfg.IntegrityVerifiedAtStartup = true

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st := sqlite.NewStore(db)
	if err := api.RunDaemon(ctx, logger, cfg, st); err != nil {
		logger.Error("daemon failed", "error", err)
		os.Exit(1)
	}
}
