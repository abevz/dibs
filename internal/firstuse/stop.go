package firstuse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abevz/dibs/internal/api"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
)

// StopDaemon stops a local dibsd whose pid file and live health agree with
// the selected socket/database. It refuses to signal an unverified process.
func StopDaemon(ctx context.Context, cfg config.Config) error {
	c := client.New(cfg.SocketPath)
	h, err := c.Health(ctx)
	if err != nil {
		if _, statErr := os.Stat(cfg.SocketPath); os.IsNotExist(statErr) {
			return nil
		}
		return fmt.Errorf("daemon socket at %s exists but health is unavailable: %w; inspect the daemon log", cfg.SocketPath, err)
	}
	if err := checkStopHealth(h, cfg); err != nil {
		return err
	}
	data, err := os.ReadFile(cfg.SocketPath + ".pid")
	if err != nil {
		return fmt.Errorf("daemon was not started by dibs; stop it with launchctl bootout, systemctl --user stop dibsd, or its foreground terminal")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return fmt.Errorf("invalid daemon pid file; refusing to signal")
	}
	name, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil || filepath.Base(strings.TrimSpace(string(name))) != "dibsd" {
		return fmt.Errorf("pid %d is not a verified dibsd process; refusing to signal", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	return waitForStopped(ctx, cfg)
}

// waitForStopped waits for both the listener and the database ownership lock.
// RunDaemon removes its socket before dibsd closes the database and lock.
func waitForStopped(ctx context.Context, cfg config.Config) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(cfg.SocketPath); os.IsNotExist(err) {
			lock, lockErr := api.AcquireDatabaseLock(cfg.DBPath)
			if lockErr == nil {
				return lock.Close()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for dibsd to release its socket and database lock")
		case <-tick.C:
		}
	}
}

func checkStopHealth(h core.Health, cfg config.Config) error {
	if filepath.Clean(h.DBPath) != filepath.Clean(cfg.DBPath) {
		return fmt.Errorf("daemon at %s does not match the configured database", cfg.SocketPath)
	}
	return nil
}
