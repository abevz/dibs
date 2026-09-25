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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/testsocket"
)

// TestIssueRunFlagParsingErrors covers the validation paths that don't need
// a daemon: they're handled entirely by runIssue's own arg parsing before
// any client call, mirroring the pattern used for the other lifecycle
// commands' usage tests.
func TestIssueRunFlagParsingErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing separator",
			args:    []string{"afc-1", "echo", "hi"},
			wantErr: "missing -- separator",
		},
		{
			name:    "no command after separator",
			args:    []string{"afc-1", "--"},
			wantErr: "no command given after --",
		},
		{
			name:    "missing issue id",
			args:    []string{"--"},
			wantErr: "Usage: dibs issue run",
		},
		{
			name:    "bad close-resolution",
			args:    []string{"afc-1", "--close-resolution", "maybe", "--", "true"},
			wantErr: "--close-resolution must be done or cancelled",
		},
		{
			name:    "non-positive ttl",
			args:    []string{"afc-1", "--ttl", "0", "--", "true"},
			wantErr: "--ttl must be positive",
		},
		{
			name:    "unknown flag",
			args:    []string{"afc-1", "--bogus", "x", "--", "true"},
			wantErr: "unknown flag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"run"}, tt.args...)
			err := runIssue(t.Context(), nil, args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestIssueRunHelpFlagShortCircuits(t *testing.T) {
	if err := runIssue(t.Context(), nil, []string{"run", "-h"}); err != nil {
		t.Errorf("runIssue(run, -h) = %v, want nil", err)
	}
}

// mockCoordinator is a minimal health and lifecycle API server used to drive
// `dibs issue run` end to end through a real subprocess,
// since claim/heartbeat/exec/close all happen inside a single compiled
// binary invocation -- there's no lighter-weight in-process seam for it.
type mockCoordinator struct {
	mu                         sync.Mutex
	claimVersion               int
	claimExpiry                string
	closeReqs                  []map[string]any
	handoffReqs                []map[string]any
	heartbeatCount             int
	heartbeatOperationIDs      []string
	heartbeatTokens            []string
	heartbeatFailures          int  // transient (non-envelope) failures before success
	heartbeatExpired           bool // respond with a lease_expired envelope
	heartbeatExpireAfterReplay bool // first ID retries successfully; next fresh ID loses ownership
}

// mockLeaseGeneration is the generation this mock hands out on claim. The
// lifecycle endpoints below enforce it exactly as the daemon does since
// afc-105, because a mock that accepts any body cannot tell a client that
// forgot a required field from one that sends it: that is how afc-118 shipped
// a CLI unable to close its own issues while every test stayed green.
const mockLeaseGeneration = 7

// leaseGenerationMatches mirrors the daemon's fencing check.
func leaseGenerationMatches(body map[string]any) bool {
	got, ok := body["lease_generation"].(float64)
	return ok && int64(got) == mockLeaseGeneration
}

func writeLeaseExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": "lease_expired", "message": "lease_generation does not match the active lease"},
	})
}

func (m *mockCoordinator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok", "db_path": config.Default().DBPath,
		})
	})
	mux.HandleFunc("POST /v1/issues/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		expiry := m.claimExpiry
		if expiry == "" {
			expiry = "2099-01-01T00:00:00Z"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"lease_token":      "test-lease-token",
			"lease_generation": mockLeaseGeneration,
			"expires_at":       expiry,
			"attempt_id":       "test-attempt-id",
			"version":          m.claimVersion,
		})
	})
	mux.HandleFunc("POST /v1/issues/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationID string `json:"operation_id"`
			LeaseToken  string `json:"lease_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid heartbeat body", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.heartbeatCount++
		m.heartbeatOperationIDs = append(m.heartbeatOperationIDs, body.OperationID)
		m.heartbeatTokens = append(m.heartbeatTokens, body.LeaseToken)
		failNext := m.heartbeatFailures > 0
		if failNext {
			m.heartbeatFailures--
		}
		expired := m.heartbeatExpired
		if m.heartbeatExpireAfterReplay && len(m.heartbeatOperationIDs) >= 3 && body.OperationID != m.heartbeatOperationIDs[0] {
			expired = true
		}
		m.mu.Unlock()

		if expired {
			// Mirror the daemon's 410 lease_expired envelope so the client
			// surfaces *client.ClientError{Code: lease_expired}.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": "lease_expired", "message": "lease not found or expired"},
			})
			return
		}
		if failNext {
			// A non-envelope 500 exercises the client's plain transport-error
			// path (doJSON cannot decode an API envelope), which the run loop
			// must treat as a transient failure and retry within the window.
			http.Error(w, "simulated transport failure", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"expires_at": "2099-01-01T00:00:00Z"})
	})
	mux.HandleFunc("POST /v1/issues/{id}/close", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.closeReqs = append(m.closeReqs, body)
		m.mu.Unlock()
		if !leaseGenerationMatches(body) {
			writeLeaseExpired(w)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "closed", "resolution": body["resolution"]})
	})
	mux.HandleFunc("POST /v1/issues/{id}/handoff", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.handoffReqs = append(m.handoffReqs, body)
		m.mu.Unlock()
		if !leaseGenerationMatches(body) {
			writeLeaseExpired(w)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"note": map[string]any{"id": "n1", "body": body["note"]}})
	})
	return mux
}

func buildAfctlForRunTest(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "dibs")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build dibs: %v\noutput: %s", err, out)
	}
	return binPath
}

// testSocketDir returns a short-lived temp directory for unix sockets. See
// internal/testsocket for why: t.TempDir() embeds the full test name, and
// under a sandboxed TMPDIR that alone can push a socket path over the
// platform's sun_path limit (afc-104, afc-105, afc-117).
func testSocketDir(t *testing.T) string {
	t.Helper()
	return testsocket.Dir(t)
}

func startMockCoordinator(t *testing.T, mock *mockCoordinator) string {
	t.Helper()
	sockPath := filepath.Join(testSocketDir(t), "af-coordinator.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	go http.Serve(l, mock.handler())
	return sockPath
}

func TestIssueHeartbeatReadsPrivateTokenFileWithoutArgv(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{}
	sockPath := startMockCoordinator(t, mock)
	tokenFile := filepath.Join(t.TempDir(), "lease-token")
	if err := os.WriteFile(tokenFile, []byte("private-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binPath, "issue", "heartbeat", "afc-1", "--lease-generation", "7", "--ttl", "60")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath,
		"DIBS_LEASE_TOKEN=", "AF_LEASE_TOKEN=", "DIBS_LEASE_TOKEN_FILE="+tokenFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("heartbeat: %v: %s", err, out)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.heartbeatTokens) != 1 || mock.heartbeatTokens[0] != "private-token" {
		t.Fatalf("heartbeat token was not read from private file")
	}
	if strings.Contains(string(out), "private-token") {
		t.Fatal("heartbeat exposed token in output")
	}
}

func TestIssueRunClosesOnSuccess(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 7}
	sockPath := startMockCoordinator(t, mock)

	cmd := exec.Command(binPath, "issue", "run", "afc-1", "--actor", "tester", "--ttl", "60", "--", "sh", "-c", "exit 0")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("issue run failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 1 {
		t.Fatalf("expected exactly one close request, got %d", len(mock.closeReqs))
	}
	if len(mock.handoffReqs) != 0 {
		t.Fatalf("expected no handoff requests on success, got %d", len(mock.handoffReqs))
	}
	got := mock.closeReqs[0]
	if got["resolution"] != "done" {
		t.Errorf("resolution = %v, want done", got["resolution"])
	}
	if v, ok := got["expected_version"].(float64); !ok || int(v) != 7 {
		t.Errorf("expected_version = %v, want the claimed version 7 (not re-fetched)", got["expected_version"])
	}
	if got["lease_token"] != "test-lease-token" {
		t.Errorf("lease_token = %v, want test-lease-token", got["lease_token"])
	}
}

func TestIssueRunHandsOffOnFailureAndMirrorsExitCode(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 3}
	sockPath := startMockCoordinator(t, mock)

	cmd := exec.Command(binPath, "issue", "run", "afc-2", "--actor", "tester", "--ttl", "60", "--", "sh", "-c", "exit 5")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected an *exec.ExitError, got %v (stdout=%s stderr=%s)", err, stdout.String(), stderr.String())
	}
	if exitErr.ExitCode() != 5 {
		t.Fatalf("exit code = %d, want 5 (mirroring the child's exit code)", exitErr.ExitCode())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 0 {
		t.Fatalf("expected no close requests on failure, got %d", len(mock.closeReqs))
	}
	if len(mock.handoffReqs) != 1 {
		t.Fatalf("expected exactly one handoff request, got %d", len(mock.handoffReqs))
	}
	note, _ := mock.handoffReqs[0]["note"].(string)
	if !strings.HasPrefix(note, "HANDOFF:") {
		t.Errorf("handoff note = %q, want HANDOFF: prefix", note)
	}
	if !strings.Contains(note, "exit 5") {
		t.Errorf("handoff note = %q, want it to mention exit 5", note)
	}
}

func TestIssueRunExportsLeaseEnvToChild(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 1}
	sockPath := startMockCoordinator(t, mock)

	printEnv := `test "$AF_LEASE_TOKEN" = "test-lease-token" || exit 10
test "$AF_LEASE_GENERATION" = "7" || exit 11
test "$AF_ATTEMPT_ID" = "test-attempt-id" || exit 12
test "$AF_ISSUE_ID" = "afc-3" || exit 13
test "$AF_EXPECTED_VERSION" = "1" || exit 14
test "$DIBS_LEASE_TOKEN" = "test-lease-token" || exit 15
test "$DIBS_LEASE_GENERATION" = "7" || exit 16
test "$DIBS_ATTEMPT_ID" = "test-attempt-id" || exit 17
test "$DIBS_ISSUE_ID" = "afc-3" || exit 18
test "$DIBS_EXPECTED_VERSION" = "1" || exit 19
exit 0`
	cmd := exec.Command(binPath, "issue", "run", "afc-3", "--actor", "tester", "--ttl", "60", "--", "sh", "-c", printEnv)
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("issue run failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
}

// TestIssueRunStopsChildOnLeaseLoss is the AFC-SDD-0157 regression: when the
// heartbeat is rejected with lease_expired (forced replacement), the child
// must receive termination, no close or handoff request may be sent, and the
// CLI must exit non-zero with an ownership-lost error. Before this change the
// child kept running and the run closed after it finished.
func TestIssueRunStopsChildOnLeaseLoss(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 9, heartbeatExpired: true}
	sockPath := startMockCoordinator(t, mock)

	marker := filepath.Join(t.TempDir(), "term-marker")
	descendantPID := filepath.Join(t.TempDir(), "descendant-pid")
	// The leader records graceful SIGTERM handling while its background child
	// deliberately ignores SIGTERM. issue run must not return until bounded
	// cleanup has also killed that descendant.
	child := `trap 'echo terminated > "$TERM_MARKER"; exit 0' TERM
	sh -c 'trap "" TERM; echo $$ > "$DESCENDANT_PID"; while :; do sleep 1; done' &
	wait`
	cmd := exec.Command(binPath, "issue", "run", "afc-4", "--actor", "tester", "--ttl", "15", "--", "sh", "-c", child)
	cmd.Env = append(os.Environ(),
		"AF_COORDINATOR_SOCKET="+sockPath,
		"TERM_MARKER="+marker,
		"DESCENDANT_PID="+descendantPID,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected an *exec.ExitError, got %v (stdout=%s stderr=%s)", err, stdout.String(), stderr.String())
	}
	if exitErr.ExitCode() != 4 {
		t.Fatalf("exit code = %d, want 4 (lease_expired); stderr: %s", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "lease ownership lost") {
		t.Errorf("stderr = %q, want it to report lease ownership lost", stderr.String())
	}

	markerData, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatalf("child termination marker not written: %v", rerr)
	}
	if string(markerData) != "terminated\n" {
		t.Errorf("marker = %q, want %q", string(markerData), "terminated\n")
	}
	descendantData, rerr := os.ReadFile(descendantPID)
	if rerr != nil {
		t.Fatalf("descendant PID not written: %v", rerr)
	}
	descendant, rerr := strconv.Atoi(strings.TrimSpace(string(descendantData)))
	if rerr != nil {
		t.Fatalf("parse descendant PID %q: %v", descendantData, rerr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(descendant, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil {
			t.Fatalf("probe descendant process %d: %v", descendant, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d survived lease-loss cancellation", descendant)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 0 {
		t.Fatalf("expected no close requests after ownership loss, got %d", len(mock.closeReqs))
	}
	if len(mock.handoffReqs) != 0 {
		t.Fatalf("expected no handoff requests after ownership loss, got %d", len(mock.handoffReqs))
	}
}

// TestIssueRunRetriesTransientHeartbeatFailure guards the bounded-retry
// contract: a single retryable transport error before the deadline must not
// kill a still-owned job. The run retries within the known lease window, the
// child completes, and the issue is closed normally.
func TestIssueRunRetriesTransientHeartbeatFailure(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 2, heartbeatFailures: 1}
	sockPath := startMockCoordinator(t, mock)

	cmd := exec.Command(binPath, "issue", "run", "afc-5", "--actor", "tester", "--ttl", "15", "--", "sh", "-c", "sleep 10")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("issue run failed after a transient heartbeat error: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 1 {
		t.Fatalf("expected exactly one close request, got %d", len(mock.closeReqs))
	}
	if len(mock.handoffReqs) != 0 {
		t.Fatalf("expected no handoff requests, got %d", len(mock.handoffReqs))
	}
	if mock.heartbeatCount < 3 {
		t.Fatalf("expected exact-ID retry and fresh proof, heartbeatCount = %d", mock.heartbeatCount)
	}
	ids := mock.heartbeatOperationIDs
	if ids[0] == "" || ids[0] != ids[1] || ids[1] == ids[2] {
		t.Fatalf("heartbeat retry/fresh operation IDs = %v", ids)
	}
	if strings.Contains(stderr.String(), "lease ownership lost") {
		t.Errorf("stderr = %q, must not report ownership loss for a retried transient failure", stderr.String())
	}
}

func TestIssueRunTreatsReplayedHeartbeatExpiryAsHistorical(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 2, heartbeatFailures: 1, heartbeatExpireAfterReplay: true}
	sockPath := startMockCoordinator(t, mock)
	cmd := exec.Command(binPath, "issue", "run", "afc-6", "--actor", "tester", "--ttl", "15", "--", "sh", "-c", "sleep 60")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 4 {
		t.Fatalf("replayed heartbeat loss exit = %v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	ids := mock.heartbeatOperationIDs
	if len(ids) < 3 || ids[0] == "" || ids[0] != ids[1] || ids[1] == ids[2] {
		t.Fatalf("heartbeat operation IDs = %v", ids)
	}
	if len(mock.closeReqs) != 0 || len(mock.handoffReqs) != 0 {
		t.Fatalf("replayed expiry allowed close/handoff: close=%d handoff=%d", len(mock.closeReqs), len(mock.handoffReqs))
	}
}

func TestIssueRunShortTTLStopsBeforeExpiredChildContinues(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{
		claimVersion:     1,
		claimExpiry:      time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339Nano),
		heartbeatExpired: true,
	}
	sockPath := startMockCoordinator(t, mock)
	cmd := exec.Command(binPath, "issue", "run", "afc-7", "--ttl", "2", "--", "sh", "-c", "sleep 10")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 4 {
		t.Fatalf("short-TTL loss exit = %v; output=%s", err, out)
	}
	if elapsed := time.Since(start); elapsed >= 3*time.Second {
		t.Fatalf("short-TTL child continued after lease expiry for %s", elapsed)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.heartbeatCount == 0 || len(mock.closeReqs) != 0 || len(mock.handoffReqs) != 0 {
		t.Fatalf("short-TTL heartbeat=%d close=%d handoff=%d", mock.heartbeatCount, len(mock.closeReqs), len(mock.handoffReqs))
	}
}

func TestIssueRunRejectsUnknownClaimDeadlineBeforeStartingChild(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimExpiry: "invalid-date"}
	sockPath := startMockCoordinator(t, mock)
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command(binPath, "issue", "run", "afc-8", "--ttl", "2", "--", "sh", "-c", "touch \"$MARKER\"")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath, "MARKER="+marker)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "invalid claim lease expiry") {
		t.Fatalf("unknown deadline result = %v; output=%s", err, out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child started without a known lease deadline: %v", err)
	}
}

// TestIssueRunCleanShutdownWithHeartbeats covers the clean-shutdown contract:
// with healthy heartbeats the run completes, the issue is closed, and no
// ownership-loss path fires.
func TestIssueRunCleanShutdownWithHeartbeats(t *testing.T) {
	binPath := buildAfctlForRunTest(t)
	mock := &mockCoordinator{claimVersion: 4}
	sockPath := startMockCoordinator(t, mock)

	cmd := exec.Command(binPath, "issue", "run", "afc-6", "--actor", "tester", "--ttl", "15", "--", "sh", "-c", "sleep 10")
	cmd.Env = append(os.Environ(), "AF_COORDINATOR_SOCKET="+sockPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("issue run failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closeReqs) != 1 {
		t.Fatalf("expected exactly one close request, got %d", len(mock.closeReqs))
	}
	if len(mock.handoffReqs) != 0 {
		t.Fatalf("expected no handoff requests on clean shutdown, got %d", len(mock.handoffReqs))
	}
	if mock.heartbeatCount < 1 {
		t.Fatalf("expected at least one successful heartbeat before shutdown, got %d", mock.heartbeatCount)
	}
	if strings.Contains(stderr.String(), "lease ownership lost") {
		t.Errorf("stderr = %q, must not report ownership loss on a clean shutdown", stderr.String())
	}
}
