package sqlite

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/abevz/dibs/internal/api"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/testsocket"
	"github.com/abevz/dibs/migrations"
)

// The child serves the real daemon API over a Unix socket. The wrapper only
// pauses a selected request at a transaction proof point or after the store
// returns, letting the parent send SIGKILL at a deterministic boundary.
type crashProofStore struct {
	*Store
	mode, marker string
}

func (s *crashProofStore) pause(point string) {
	if s.mode != point {
		return
	}
	if err := os.WriteFile(s.marker, []byte(point), 0o600); err != nil {
		panic(err)
	}
	select {}
}

func (s *crashProofStore) proofContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, coordinationProofHookContextKey{}, coordinationProofHook(func(point coordinationProofPoint) {
		s.pause(string(point))
	}))
}

func (s *crashProofStore) CreateIssue(ctx context.Context, project string, req core.CreateIssueRequest) (core.Issue, error) {
	issue, err := s.Store.CreateIssue(ctx, project, req)
	if err == nil {
		s.pause("create_after_commit")
	}
	return issue, err
}

func (s *crashProofStore) ClaimIssueWithOperation(ctx context.Context, issueID string, req core.ClaimRequest) (core.ClaimResponse, error) {
	claim, err := s.Store.ClaimIssueWithOperation(s.proofContext(ctx), issueID, req)
	if err == nil {
		s.pause("claim_after_commit")
	}
	return claim, err
}

func (s *crashProofStore) HeartbeatLeaseWithOperation(ctx context.Context, issueID string, req core.HeartbeatRequest, now time.Time) (string, error) {
	expiry, err := s.Store.HeartbeatLeaseWithOperation(s.proofContext(ctx), issueID, req, now)
	if err == nil {
		s.pause("heartbeat_after_commit")
	}
	return expiry, err
}

func (s *crashProofStore) HandoffLease(ctx context.Context, issueID string, req core.HandoffRequest) (core.HandoffResponse, error) {
	resp, err := s.Store.HandoffLease(s.proofContext(ctx), issueID, req)
	if err == nil {
		s.pause("handoff_after_commit")
	}
	return resp, err
}

func (s *crashProofStore) UpdateIssue(ctx context.Context, issueID string, req core.UpdateIssueRequest) (core.Issue, error) {
	issue, err := s.Store.UpdateIssue(s.proofContext(ctx), issueID, req)
	if err == nil {
		s.pause("update_after_commit")
	}
	return issue, err
}

func (s *crashProofStore) CloseIssue(ctx context.Context, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	resp, err := s.Store.CloseIssue(ctx, issueID, req)
	if err == nil {
		s.pause("close_after_commit")
	}
	return resp, err
}

func TestCrashRecoveryDaemonHelper(t *testing.T) {
	if os.Getenv("DIBS_TEST_CRASH_HELPER") != "1" {
		return
	}
	dbPath, socketPath := os.Getenv("DIBS_TEST_CRASH_DB"), os.Getenv("DIBS_TEST_CRASH_SOCKET")
	lock, err := api.AcquireDatabaseLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := VerifyKnownMigrations(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	st := &crashProofStore{Store: NewStore(db), mode: os.Getenv("DIBS_TEST_CRASH_MODE"), marker: os.Getenv("DIBS_TEST_CRASH_MARKER")}
	if err := api.RunDaemon(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{DBPath: dbPath, SocketPath: socketPath}, st); err != nil {
		t.Fatal(err)
	}
}

func TestCrashRecoveryWorkerHelper(t *testing.T) {
	if os.Getenv("DIBS_TEST_WORKER_HELPER") != "1" {
		return
	}
	output := os.Getenv("DIBS_TEST_WORKER_OUTPUT")
	if os.Getenv("DIBS_TEST_WORKER_MODE") == "external" {
		if err := os.WriteFile(output, []byte("published"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	c := client.New(os.Getenv("DIBS_TEST_WORKER_SOCKET"))
	claim, err := c.ClaimIssueWithRequest(context.Background(), os.Getenv("DIBS_TEST_WORKER_ISSUE"), core.ClaimRequest{Holder: "worker", TTLSeconds: 600, OperationID: "restart-active-claim-0001"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runWorkerHelper(t *testing.T, mode, socket, issue, output string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryWorkerHelper$")
	cmd.Env = append(os.Environ(), "DIBS_TEST_WORKER_HELPER=1", "DIBS_TEST_WORKER_MODE="+mode,
		"DIBS_TEST_WORKER_SOCKET="+socket, "DIBS_TEST_WORKER_ISSUE="+issue,
		"DIBS_TEST_WORKER_OUTPUT="+output)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worker process: %v: %s", err, combined)
	}
}

type crashDaemon struct {
	cmd     *exec.Cmd
	done    chan error
	logPath string
}

type crashHarness struct {
	t                          *testing.T
	dbPath, socketPath, marker string
	client                     *client.Client
	process                    *crashDaemon
}

func newCrashHarness(t *testing.T) *crashHarness {
	t.Helper()
	dir := t.TempDir()
	h := &crashHarness{t: t, dbPath: filepath.Join(dir, "coordinator.db"), socketPath: testsocket.PathNamed(t, "crash"), marker: filepath.Join(dir, "point")}
	h.client = client.New(h.socketPath)
	h.start("")
	t.Cleanup(func() { h.kill() })
	return h
}

func (h *crashHarness) start(mode string) {
	h.t.Helper()
	if h.process != nil {
		h.t.Fatal("start with process still running")
	}
	_ = os.Remove(h.marker)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryDaemonHelper$")
	cmd.Env = append(os.Environ(), "DIBS_TEST_CRASH_HELPER=1", "DIBS_TEST_CRASH_DB="+h.dbPath,
		"DIBS_TEST_CRASH_SOCKET="+h.socketPath, "DIBS_TEST_CRASH_MODE="+mode,
		"DIBS_TEST_CRASH_MARKER="+h.marker)
	logPath := filepath.Join(filepath.Dir(h.dbPath), "daemon-"+strings.ReplaceAll(mode, "/", "-")+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		h.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		h.t.Fatal(err)
	}
	_ = logFile.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	h.process = &crashDaemon{cmd: cmd, done: done, logPath: logPath}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := h.client.Health(ctx)
		cancel()
		if err == nil {
			return
		}
		select {
		case processErr := <-done:
			h.process = nil
			output, _ := os.ReadFile(logPath)
			h.t.Fatalf("daemon exited: %v: %s", processErr, output)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("daemon did not start")
}

func (h *crashHarness) kill() {
	h.t.Helper()
	if h.process == nil {
		return
	}
	process := h.process
	h.process = nil
	_ = process.cmd.Process.Kill()
	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("daemon did not exit after SIGKILL")
	}
}

func (h *crashHarness) waitPoint() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(h.marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("daemon did not reach crash point")
}

func (h *crashHarness) crashRequest(call func() error) {
	h.t.Helper()
	result := make(chan error, 1)
	go func() { result <- call() }()
	h.waitPoint()
	h.kill()
	select {
	case err := <-result:
		if err == nil {
			h.t.Fatal("killed request returned success")
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("killed request did not return")
	}
	h.start("")
}

func (h *crashHarness) newIssue(title string) core.Issue {
	h.t.Helper()
	issue, err := h.client.CreateIssue(context.Background(), core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: title, Actor: "test"})
	if err != nil {
		h.t.Fatal(err)
	}
	return issue
}

func (h *crashHarness) project() {
	h.t.Helper()
	if _, err := h.client.CreateProject(context.Background(), "afc", "Crash test", ""); err != nil {
		h.t.Fatal(err)
	}
}

func countEvents(t *testing.T, c *client.Client, issueID, kind string) int {
	t.Helper()
	events, err := c.ListEvents(context.Background(), issueID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, event := range events {
		if event.EventType == kind {
			n++
		}
	}
	return n
}

func operationRows(t *testing.T, dbPath, operationID string) int {
	t.Helper()
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM operations WHERE operation_id = ?`, operationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCrashBeforeCommitHasNoPartialState(t *testing.T) {
	for _, kind := range []string{"claim", "heartbeat", "handoff", "update"} {
		t.Run(kind, func(t *testing.T) {
			h := newCrashHarness(t)
			h.project()
			issue := h.newIssue("before " + kind)
			var claim core.ClaimResponse
			if kind != "claim" {
				var err error
				claim, err = h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "test", TTLSeconds: 600})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, leaseBefore, err := h.client.GetIssue(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			beforeEvents, err := h.client.ListEvents(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			beforeNotes, err := h.client.ListNotes(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			h.kill()
			point := map[string]string{"claim": "claim_before_commit", "heartbeat": "heartbeat_before_commit", "handoff": "handoff_before_commit", "update": "update_after_authorize"}[kind]
			operationID := map[string]string{"claim": "crash-claim-before-0001", "heartbeat": "crash-heartbeat-before-0001", "handoff": "crash-handoff-before-0001", "update": "crash-update-before-0001"}[kind]
			h.start(point)
			var retry func() error
			switch kind {
			case "claim":
				retry = func() error {
					_, err := h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "test", TTLSeconds: 600, OperationID: "crash-claim-before-0001"})
					return err
				}
			case "heartbeat":
				retry = func() error {
					_, err := h.client.HeartbeatLeaseWithOperation(context.Background(), issue.ShortID, core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 900, OperationID: "crash-heartbeat-before-0001"})
					return err
				}
			case "handoff":
				retry = func() error {
					_, err := h.client.HandoffLeaseWithOperation(context.Background(), issue.ShortID, core.HandoffRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "HANDOFF: crash proof", OperationID: "crash-handoff-before-0001"})
					return err
				}
			case "update":
				retry = func() error {
					_, err := h.client.UpdateIssue(context.Background(), issue.ShortID, core.UpdateIssueRequest{Title: "must roll back", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "test", OperationID: "crash-update-before-0001"})
					return err
				}
			}
			h.crashRequest(retry)
			after, leaseAfter, err := h.client.GetIssue(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			afterEvents, err := h.client.ListEvents(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			afterNotes, err := h.client.ListNotes(context.Background(), issue.ShortID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(leaseBefore, leaseAfter) || !reflect.DeepEqual(beforeEvents, afterEvents) || !reflect.DeepEqual(beforeNotes, afterNotes) {
				t.Fatalf("%s partially committed after SIGKILL: issue %v -> %v, lease %v -> %v, events %d -> %d, notes %d -> %d", kind, before, after, leaseBefore, leaseAfter, len(beforeEvents), len(afterEvents), len(beforeNotes), len(afterNotes))
			}
			if got := operationRows(t, h.dbPath, operationID); got != 0 {
				t.Fatalf("%s rollback left %d ledger rows", kind, got)
			}
			if err := retry(); err != nil {
				t.Fatalf("%s retry after rollback: %v", kind, err)
			}
			if got := operationRows(t, h.dbPath, operationID); got != 1 {
				t.Fatalf("%s retry created %d ledger rows", kind, got)
			}
		})
	}
}

func TestCrashAfterCommitReplaysOriginalOutcome(t *testing.T) {
	for _, kind := range []string{"create", "claim", "heartbeat", "handoff", "close"} {
		t.Run(kind, func(t *testing.T) {
			h := newCrashHarness(t)
			h.project()
			var issue core.Issue
			var claim core.ClaimResponse
			if kind != "create" {
				issue = h.newIssue("after " + kind)
			}
			if kind == "heartbeat" || kind == "handoff" || kind == "close" {
				var err error
				claim, err = h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "test", TTLSeconds: 600})
				if err != nil {
					t.Fatal(err)
				}
			}
			h.kill()
			h.start(kind + "_after_commit")
			operationID := map[string]string{"create": "crash-create-after-0001", "claim": "crash-claim-after-0001", "heartbeat": "crash-heartbeat-after-0001", "handoff": "crash-handoff-after-0001", "close": "crash-close-after-0001"}[kind]
			var replay func() (any, error)
			switch kind {
			case "create":
				replay = func() (any, error) {
					return h.client.CreateIssue(context.Background(), core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: "after create", Actor: "test", OperationID: "crash-create-after-0001"})
				}
			case "claim":
				replay = func() (any, error) {
					return h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "test", TTLSeconds: 600, OperationID: "crash-claim-after-0001"})
				}
			case "heartbeat":
				replay = func() (any, error) {
					return h.client.HeartbeatLeaseWithOperation(context.Background(), issue.ShortID, core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 900, OperationID: "crash-heartbeat-after-0001"})
				}
			case "handoff":
				replay = func() (any, error) {
					return h.client.HandoffLeaseWithOperation(context.Background(), issue.ShortID, core.HandoffRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "HANDOFF: committed before death", OperationID: "crash-handoff-after-0001"})
				}
			case "close":
				replay = func() (any, error) {
					return h.client.CloseIssue(context.Background(), issue.ShortID, core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "test", Note: "committed before death", OperationID: "crash-close-after-0001"})
				}
			}
			h.crashRequest(func() error { _, err := replay(); return err })
			if got := operationRows(t, h.dbPath, operationID); got != 1 {
				t.Fatalf("%s commit left %d ledger rows", kind, got)
			}
			first, err := replay()
			if err != nil {
				t.Fatalf("%s replay after SIGKILL: %v", kind, err)
			}
			second, err := replay()
			if err != nil {
				t.Fatalf("%s second replay: %v", kind, err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("%s replay changed outcome: %v != %v", kind, first, second)
			}
			if kind == "heartbeat" {
				_, persistedLease, err := h.client.GetIssue(context.Background(), issue.ShortID)
				if err != nil {
					t.Fatal(err)
				}
				if persistedLease == nil || persistedLease.ExpiresAt != first.(string) {
					t.Fatalf("heartbeat replay renewed persisted expiry: lease=%+v original=%v", persistedLease, first)
				}
			}
			if got := operationRows(t, h.dbPath, operationID); got != 1 {
				t.Fatalf("%s replay changed ledger count to %d", kind, got)
			}
			if kind == "create" {
				issue = first.(core.Issue)
			}
			if kind == "claim" {
				got := first.(core.ClaimResponse)
				if got.LeaseToken == "" || got.LeaseGeneration != 1 {
					t.Fatalf("claim replay lost original lease: %+v", got)
				}
			}
			eventType := map[string]string{"create": "issue_created", "claim": "issue_claimed", "heartbeat": "issue_claimed", "handoff": "issue_released", "close": "issue_closed"}[kind]
			if got := countEvents(t, h.client, issue.ShortID, eventType); got != 1 {
				t.Fatalf("%s replay events = %d, want 1", kind, got)
			}
			if kind == "handoff" || kind == "close" {
				notes, err := h.client.ListNotes(context.Background(), issue.ShortID)
				if err != nil {
					t.Fatal(err)
				}
				if len(notes) != 1 {
					t.Fatalf("%s replay notes = %d, want 1", kind, len(notes))
				}
			}
		})
	}
}

func TestRestartRetainsActiveLeaseAndFencesExpiredWorker(t *testing.T) {
	h := newCrashHarness(t)
	h.project()
	activeIssue := h.newIssue("active across restart")
	claimOutput := filepath.Join(filepath.Dir(h.dbPath), "worker-claim.json")
	runWorkerHelper(t, "claim", h.socketPath, activeIssue.ShortID, claimOutput)
	data, err := os.ReadFile(claimOutput)
	if err != nil {
		t.Fatal(err)
	}
	var active core.ClaimResponse
	if err := json.Unmarshal(data, &active); err != nil {
		t.Fatal(err)
	}
	h.kill()
	h.start("")
	got, lease, err := h.client.GetIssue(context.Background(), activeIssue.ShortID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" || lease == nil || lease.AttemptID != active.AttemptID || lease.LeaseGeneration != active.LeaseGeneration || lease.ExpiresAt != active.ExpiresAt {
		t.Fatalf("active lease changed across restart: issue=%+v lease=%+v claim=%+v", got, lease, active)
	}
	if _, err := h.client.HeartbeatLeaseWithOperation(context.Background(), activeIssue.ShortID, core.HeartbeatRequest{LeaseToken: active.LeaseToken, LeaseGeneration: active.LeaseGeneration, TTLSeconds: 600, OperationID: "restart-active-heartbeat-0001"}); err != nil {
		t.Fatalf("active owner fenced after restart: %v", err)
	}

	expiredIssue := h.newIssue("expired across restart")
	expired, err := h.client.ClaimIssueWithRequest(context.Background(), expiredIssue.ShortID, core.ClaimRequest{Holder: "old", TTLSeconds: 1, OperationID: "restart-expired-claim-0001"})
	if err != nil {
		t.Fatal(err)
	}
	h.kill()
	deadline, err := time.Parse(time.RFC3339, expired.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if wait := time.Until(deadline.Add(time.Second)); wait > 0 {
		time.Sleep(wait)
	}
	h.start("")
	ready, err := h.client.ListReadyIssues(context.Background(), "afc", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range ready {
		if issue.ShortID == expiredIssue.ShortID {
			found = true
		}
	}
	if !found {
		t.Fatal("expired issue not ready after restart")
	}
	_, err = h.client.HeartbeatLeaseWithOperation(context.Background(), expiredIssue.ShortID, core.HeartbeatRequest{LeaseToken: expired.LeaseToken, LeaseGeneration: expired.LeaseGeneration, TTLSeconds: 600, OperationID: "restart-stale-heartbeat-0001"})
	if err == nil || !strings.Contains(err.Error(), "lease_expired") {
		t.Fatalf("expired heartbeat accepted: %v", err)
	}
	replacement, err := h.client.ClaimIssueWithRequest(context.Background(), expiredIssue.ShortID, core.ClaimRequest{Holder: "new", TTLSeconds: 600, OperationID: "restart-reclaim-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.LeaseGeneration != expired.LeaseGeneration+1 {
		t.Fatalf("replacement generation = %d", replacement.LeaseGeneration)
	}
	if got := countEvents(t, h.client, expiredIssue.ShortID, "issue_claimed"); got != 2 {
		t.Fatalf("claim events = %d", got)
	}
}

func TestWorkerDeathBeforeCloseRequiresReconciliation(t *testing.T) {
	h := newCrashHarness(t)
	h.project()
	issue := h.newIssue("external work before close")
	claim, err := h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "worker", TTLSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	// The marker represents external work; coordinator state alone cannot prove
	// whether such work completed after the worker died.
	externalMarker := filepath.Join(filepath.Dir(h.dbPath), "external-work-done")
	runWorkerHelper(t, "external", h.socketPath, issue.ShortID, externalMarker)
	h.kill()
	h.start("")
	got, lease, err := h.client.GetIssue(context.Background(), issue.ShortID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" || lease == nil || countEvents(t, h.client, issue.ShortID, "issue_closed") != 0 {
		t.Fatalf("worker death auto-closed or lost lease: %+v %+v", got, lease)
	}
	if _, err := os.Stat(externalMarker); err != nil {
		t.Fatal(err)
	}
	request := core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "worker", Note: "reconciled external publish", OperationID: "reconciled-close-0001"}
	first, err := h.client.CloseIssue(context.Background(), issue.ShortID, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.client.CloseIssue(context.Background(), issue.ShortID, request)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("close reconciliation replay: %v / %v: %v", first, second, err)
	}
	if got := countEvents(t, h.client, issue.ShortID, "issue_closed"); got != 1 {
		t.Fatalf("close events = %d", got)
	}
}

func TestLiveWALBackupRestoresEventsAndMigrationLedger(t *testing.T) {
	h := newCrashHarness(t)
	h.project()
	issue := h.newIssue("WAL backup")
	claim, err := h.client.ClaimIssueWithRequest(context.Background(), issue.ShortID, core.ClaimRequest{Holder: "worker", TTLSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.CloseIssue(context.Background(), issue.ShortID, core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "worker", OperationID: "backup-close-0001"}); err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(h.dbPath + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("expected live WAL with committed data: %v / %v", wal, err)
	}
	backupPath := filepath.Join(filepath.Dir(h.dbPath), "backup.db")
	backupDB, err := Open(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backupDB.ExecContext(context.Background(), `VACUUM INTO ?`, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := backupDB.Close(); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	var migrationCount, eventCount int
	if err := backup.QueryRow(`SELECT count(*) FROM _migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRow(`SELECT count(*) FROM events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount == 0 || eventCount == 0 {
		t.Fatalf("incomplete backup migrations=%d events=%d", migrationCount, eventCount)
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}
	h.kill()
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(filepath.Dir(h.dbPath), "restored.db")
	if err := os.WriteFile(restoredPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := &crashHarness{t: t, dbPath: restoredPath, socketPath: testsocket.PathNamed(t, "restore"), marker: filepath.Join(filepath.Dir(h.dbPath), "restore-point")}
	restored.client = client.New(restored.socketPath)
	restored.start("")
	t.Cleanup(func() { restored.kill() })
	got, lease, err := restored.client.GetIssue(context.Background(), issue.ShortID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || lease != nil || countEvents(t, restored.client, issue.ShortID, "issue_closed") != 1 {
		t.Fatalf("restored state incomplete: %+v %+v", got, lease)
	}
	restoredDB, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	var gotMigrations, gotEvents int
	if err := restoredDB.QueryRow(`SELECT count(*) FROM _migrations`).Scan(&gotMigrations); err != nil {
		t.Fatal(err)
	}
	if err := restoredDB.QueryRow(`SELECT count(*) FROM events`).Scan(&gotEvents); err != nil {
		t.Fatal(err)
	}
	if gotMigrations != migrationCount || gotEvents != eventCount {
		t.Fatalf("restored ledger counts %d/%d != backup %d/%d", gotMigrations, gotEvents, migrationCount, eventCount)
	}
}

func TestStartupRejectsUnknownMigrationAndCorruptDatabase(t *testing.T) {
	for _, kind := range []string{"unknown migration", "corrupt database"} {
		t.Run(kind, func(t *testing.T) {
			h := newCrashHarness(t)
			h.project()
			h.kill()
			if kind == "unknown migration" {
				db, err := Open(h.dbPath)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`INSERT INTO _migrations (name, applied_at) VALUES (?, ?)`, "9999_unknown.sql", "2026-09-24T00:00:00Z")
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = os.Remove(h.dbPath + "-wal")
				_ = os.Remove(h.dbPath + "-shm")
				if err := os.WriteFile(h.dbPath, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashRecoveryDaemonHelper$")
			cmd.Env = append(os.Environ(), "DIBS_TEST_CRASH_HELPER=1", "DIBS_TEST_CRASH_DB="+h.dbPath, "DIBS_TEST_CRASH_SOCKET="+h.socketPath)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("invalid database startup succeeded")
			}
			if !strings.Contains(string(output), map[string]string{"unknown migration": "unknown applied migration", "corrupt database": "database"}[kind]) {
				t.Fatalf("startup diagnostic for %s: %s", kind, output)
			}
			if _, healthErr := h.client.Health(context.Background()); healthErr == nil {
				t.Fatal("invalid database served the API")
			}
		})
	}
}

func TestInvalidMigrationRollsBackWithoutLedgerEntry(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "invalid-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bad := fstest.MapFS{"9999_invalid.sql": &fstest.MapFile{Data: []byte("CREATE TABLE must_rollback (id integer); INSERT INTO missing_table VALUES (1);")}}
	err = Migrate(context.Background(), db, bad)
	if err == nil || !strings.Contains(err.Error(), "9999_invalid.sql") {
		t.Fatalf("invalid migration diagnostic: %v", err)
	}
	var tableCount, ledgerCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='must_rollback'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM _migrations WHERE name='9999_invalid.sql'`).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 || ledgerCount != 0 {
		t.Fatalf("failed migration partially applied: table=%d ledger=%d", tableCount, ledgerCount)
	}
}
