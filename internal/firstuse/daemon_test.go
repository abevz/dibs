package firstuse

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
)

func TestEnsureDaemonUsesHealthyMatchingSocket(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DBPath: filepath.Join(dir, "data.db"), SocketPath: filepath.Join(dir, "daemon.sock")}
	serverDB := cfg.DBPath
	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "db_path": serverDB})
	})
	server := &http.Server{Handler: mux}
	defer server.Close()
	go server.Serve(listener)
	if err := EnsureDaemon(context.Background(), cfg, ""); err != nil {
		t.Fatal(err)
	}
	cfg.DBPath = filepath.Join(dir, "other.db")
	if err := EnsureDaemon(context.Background(), cfg, ""); err == nil || !strings.Contains(err.Error(), "another database") {
		t.Fatalf("mismatched database error = %v", err)
	}
}

func TestStopDaemonWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DBPath: filepath.Join(dir, "data.db"), SocketPath: filepath.Join(dir, "absent.sock")}
	if err := StopDaemon(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.SocketPath + ".pid"); !os.IsNotExist(err) {
		t.Fatalf("unexpected pid file: %v", err)
	}
}

func TestStopDaemonDoesNotMistakeUnhealthySocketForStopped(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DBPath: filepath.Join(dir, "data.db"), SocketPath: filepath.Join(dir, "daemon.sock")}
	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})}
	defer server.Close()
	go server.Serve(listener)
	if err := StopDaemon(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "health is unavailable") {
		t.Fatalf("stop on unhealthy socket = %v", err)
	}
}

func TestDegradedMatchingDaemonCanBeStopped(t *testing.T) {
	cfg := config.Config{DBPath: "/tmp/dibs-test.db", SocketPath: "/tmp/dibs-test.sock"}
	if err := checkStopHealth(core.Health{Status: "degraded", DBPath: cfg.DBPath}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := checkStopHealth(core.Health{Status: "degraded", DBPath: "/tmp/other.db"}, cfg); err == nil {
		t.Fatal("mismatched database accepted")
	}
}
