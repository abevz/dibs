package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestClaimAndRunShowLeaseHolderAndPreserveExitCode(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	socketPath := filepath.Join(testSocketDir(t), "lease.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	home := t.TempDir()
	dbPath := filepath.Join(home, "db")
	const message = "demo-1 is already leased by codex until 2026-09-26T16:00:00Z (PID 1234@arch)"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "db_path": dbPath})
		case "/v1/issues/demo-1/claim":
			w.Header().Set("X-Dibs-Error-Code", core.ErrLeaseHeld)
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code": core.ErrLeaseHeld, "message": message,
				"details": map[string]any{
					"short_id": "demo-1", "holder": "codex", "lease_expires_at": "2026-09-26T16:00:00Z",
					"lease_pid": 1234, "lease_host": "arch",
					"lease_token": "secret-token", "issue_id": "internal-uuid",
				},
			}})
		default:
			http.NotFound(w, r)
		}
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	for _, tc := range []struct {
		name string
		args []string
		json bool
	}{
		{name: "human claim", args: []string{"issue", "claim", "demo-1", "--holder", "claude"}},
		{name: "JSON claim", args: []string{"--json", "issue", "claim", "demo-1", "--holder", "claude"}, json: true},
		{name: "human run", args: []string{"issue", "run", "demo-1", "--actor", "claude", "--", "/bin/true"}},
		{name: "JSON run", args: []string{"--json", "issue", "run", "demo-1", "--actor", "claude", "--", "/bin/true"}, json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			cmd.Dir = home
			cmd.Env = append(os.Environ(), "HOME="+home, "DIBS_DB="+dbPath, "DIBS_SOCKET="+socketPath)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
				t.Fatalf("exit = %v, stderr = %q; want code 3", err, stderr.String())
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "secret-token") || strings.Contains(stderr.String(), "internal-uuid") {
				t.Fatalf("unexpected output: stdout %q stderr %q", stdout.String(), stderr.String())
			}
			if tc.json {
				var envelope core.APIErrorResponse
				if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
					t.Fatalf("JSON error = %q: %v", stderr.String(), err)
				}
				if envelope.Error.Code != core.ErrLeaseHeld || envelope.Error.Details == nil || envelope.Error.Details.ShortID != "demo-1" || envelope.Error.Details.LeasePID != 1234 {
					t.Fatalf("JSON error = %+v", envelope.Error)
				}
			} else if !strings.Contains(stderr.String(), message) {
				t.Fatalf("human error = %q, want %q", stderr.String(), message)
			}
		})
	}
}
