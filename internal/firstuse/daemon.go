package firstuse

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
)

// EnsureDaemon starts the installed companion binary when the configured socket
// is unavailable. dibsd's database lock decides which concurrent starter wins.
func EnsureDaemon(ctx context.Context, cfg config.Config, daemonPath string) error {
	c := client.New(cfg.SocketPath)
	if h, err := c.Health(ctx); err == nil {
		if h.Status != "ok" || filepath.Clean(h.DBPath) != filepath.Clean(cfg.DBPath) {
			return fmt.Errorf("daemon at %s is unhealthy or uses another database; run dibs doctor", cfg.SocketPath)
		}
		return nil
	}
	// A reachable socket may belong to a compatible older service or a
	// command-specific test server. Only an absent/unreachable socket triggers
	// a new process; the command itself reports API errors if it cannot proceed.
	if conn, err := net.DialTimeout("unix", cfg.SocketPath, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil
	}
	if daemonPath == "" {
		return fmt.Errorf("dibsd binary is missing; reinstall dibs or put dibsd next to dibs")
	}
	logPath := cfg.SocketPath + ".startup.log"
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create daemon log directory: %w", err)
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon startup log: %w", err)
	}
	cmd := exec.Command(daemonPath)
	cmd.Env = append(os.Environ(), "DIBS_INTERNAL_AUTOSTART=1")
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return fmt.Errorf("start dibsd: %w", err)
	}
	log.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var exitErr error
	for {
		if h, err := c.Health(ctx); err == nil && h.Status == "ok" {
			if filepath.Clean(h.DBPath) != filepath.Clean(cfg.DBPath) {
				return fmt.Errorf("daemon at %s uses another database; run dibs doctor", cfg.SocketPath)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			exitErr = err
			// Another concurrent starter may have acquired the singleton lock.
			// Continue briefly for its socket to become healthy.
			exited = nil
		case <-timer.C:
			message := startupLogTail(logPath)
			if exitErr != nil {
				return fmt.Errorf("dibsd failed to start: %v; %s (log: %s)", exitErr, message, logPath)
			}
			return fmt.Errorf("timed out waiting for dibsd at %s; %s (log: %s)", cfg.SocketPath, message, logPath)
		case <-tick.C:
		}
	}
}

func startupLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "inspect the daemon log"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		return "inspect the daemon log"
	}
	last := lines[len(lines)-1]
	if len(last) > 500 {
		last = last[len(last)-500:]
	}
	return last
}

func FindDaemon() string {
	self, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(self), "dibsd")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}
