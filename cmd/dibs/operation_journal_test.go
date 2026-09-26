package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
)

func TestManualClaimSessionID(t *testing.T) {
	for _, tc := range []struct {
		name, explicit, host string
		pid                  int
		want                 string
	}{
		{"automatic", "", "my-host", 4321, "dibs-claim:v1:my-host:4321"},
		{"explicit", "custom-session", "my-host", 4321, "custom-session"},
		{"no PID", "", "my-host", 0, ""},
		{"invalid host", "", "bad:host", 4321, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := manualClaimSessionID(tc.explicit, tc.host, tc.pid); got != tc.want {
				t.Fatalf("session ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaimJournalPersistsSessionAndRestoresLegacyRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const oldID = "op-old-1111-2222-3333-444455556666"
	const newID = "op-new-1111-2222-3333-444455556666"
	const session = "dibs-claim:v1:test-host:4321"
	if _, _, err := journalOperationID("claim", "demo-1", oldID); err != nil {
		t.Fatal(err)
	}
	path, previous, err := journalClaimOperation("demo-1", newID, session)
	if err != nil {
		t.Fatal(err)
	}
	if previous != oldID {
		t.Fatalf("previous journal = %q, want %q", previous, oldID)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal permissions: info=%v err=%v", info, err)
	}
	gotID, gotSession, _, err := readJournaledClaimOperation("demo-1")
	if err != nil || gotID != newID || gotSession != session {
		t.Fatalf("journal = (%q, %q), err=%v", gotID, gotSession, err)
	}
	restoreJournaledOperationID(path, previous)
	gotID, gotSession, _, err = readJournaledClaimOperation("demo-1")
	if err != nil || gotID != oldID || gotSession != "" {
		t.Fatalf("restored legacy journal = (%q, %q), err=%v", gotID, gotSession, err)
	}
}

func TestManualClaimCLISendsTypedSession(t *testing.T) {
	if os.Getenv("DIBS_TEST_CLAIM_CHILD") == "1" {
		args := []string{"demo-1"}
		if explicit := os.Getenv("DIBS_TEST_CLAIM_EXPLICIT"); explicit != "" {
			args = append(args, "--session-id", explicit)
		}
		_ = runIssueClaim(context.Background(), client.New(os.Getenv("DIBS_TEST_CLAIM_SOCKET")), args)
		return
	}
	for _, explicit := range []string{"", "custom-session"} {
		t.Run(explicit, func(t *testing.T) {
			home := t.TempDir()
			socket := filepath.Join(testSocketDir(t), "claim-session.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			received := make(chan core.ClaimRequest, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req core.ClaimRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				received <- req
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"test rejection"}}`))
			}))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			command := exec.Command(os.Args[0], "-test.run=^TestManualClaimCLISendsTypedSession$")
			command.Env = append(os.Environ(), "HOME="+home, "DIBS_ACTOR=tester", "DIBS_TEST_CLAIM_CHILD=1", "DIBS_TEST_CLAIM_SOCKET="+socket, "DIBS_TEST_CLAIM_EXPLICIT="+explicit)
			if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "test rejection") {
				t.Fatalf("child err=%v output=%q", err, output)
			}
			select {
			case req := <-received:
				if explicit != "" && req.SessionID != explicit {
					t.Fatalf("explicit session = %q", req.SessionID)
				}
				if explicit == "" && !strings.HasPrefix(req.SessionID, "dibs-claim:v1:") {
					t.Fatalf("automatic session = %q", req.SessionID)
				}
			default:
				t.Fatal("claim request not received")
			}
		})
	}
}

func TestCreateCLIPreservesJournalAfterServerInternalError(t *testing.T) {
	if os.Getenv("DIBS_TEST_CREATE_CHILD") == "1" {
		runIssueCreate(context.Background(), client.New(os.Getenv("DIBS_TEST_CREATE_SOCKET")), []string{
			"--project", "demo", "--scope-kind", "project", "--title", "uncertain", "--allow-duplicate",
		})
		return
	}
	home := t.TempDir()
	socket := filepath.Join(testSocketDir(t), "create-error.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/issues" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"commit outcome unknown"}}`))
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestCreateCLIPreservesJournalAfterServerInternalError$")
	command.Env = append(os.Environ(), "HOME="+home, "DIBS_ACTOR=tester", "DIBS_TEST_CREATE_CHILD=1", "DIBS_TEST_CREATE_SOCKET="+socket)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "create outcome is unconfirmed") {
		t.Fatalf("child error = %v, output = %q", err, output)
	}
	req := core.CreateIssueRequest{Project: "demo", ScopeKind: "project", Title: "uncertain", Actor: "tester"}
	target := "demo-" + core.OperationFingerprint(core.CreateFingerprintFields("demo", req))
	t.Setenv("HOME", home)
	id, _, err := readJournaledOperationID("create", target)
	if err != nil || id == "" {
		t.Fatalf("journal after API 500: id=%q err=%v", id, err)
	}
}

func TestCreateJournalSurvivesUncertainServerError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const previous = "op-previous-1111-2222-3333-444455556666"
	const current = "op-current-1111-2222-3333-444455556666"
	if _, _, err := journalOperationID("create", "demo-request", previous); err != nil {
		t.Fatal(err)
	}
	path, displaced, err := journalOperationID("create", "demo-request", current)
	if err != nil {
		t.Fatal(err)
	}
	if createFailureDefinitelyRejected(&client.ClientError{Code: "internal_error", Message: "commit outcome unknown"}) {
		t.Fatal("internal_error was treated as a definite rejection")
	}
	got, _, err := readJournaledOperationID("create", "demo-request")
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("journal after uncertain server error = %q, want %q", got, current)
	}
	if !createFailureDefinitelyRejected(&client.ClientError{Code: "validation_failed", Message: "invalid request"}) {
		t.Fatal("validation_failed was not treated as a definite rejection")
	}
	restoreJournaledOperationID(path, displaced)
	got, _, err = readJournaledOperationID("create", "demo-request")
	if err != nil {
		t.Fatal(err)
	}
	if got != previous {
		t.Fatalf("journal after definite rejection = %q, want %q", got, previous)
	}
}

func TestExistingLegacyOperationJournalRemainsInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".local/state/af-coordinator/operations")
	legacyDB := filepath.Join(home, ".local/share/af-coordinator/af-coordinator.db")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyDB), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyDB, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := expandOperationJournalDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("operation journal = %q, want %q", got, legacy)
	}
}

// TestJournalOperationIDPersistsBeforeSend covers the property the journal
// exists for: the key is on disk, readable back, and 0600 because it can
// replay an outcome containing a lease token.
func TestJournalOperationIDPersistsBeforeSend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, previous, err := journalOperationID("claim", "demo-1", "op-first-0000-1111-2222-333344445555")
	if err != nil {
		t.Fatal(err)
	}
	if previous != "" {
		t.Errorf("previous = %q, want empty for a fresh journal", previous)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal mode = %o, want 600", perm)
	}

	got, _, err := readJournaledOperationID("claim", "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "op-first-0000-1111-2222-333344445555" {
		t.Errorf("journaled id = %q", got)
	}
}

// TestRejectedClaimRestoresPreviousOperationID is a regression test for a
// journal bug found during end-to-end validation: a later claim attempt that
// the daemon definitively rejected overwrote — and so destroyed — the
// operation ID of an earlier claim that had actually committed, leaving the
// committed outcome unrecoverable.
func TestRejectedClaimRestoresPreviousOperationID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const committed = "op-committed-1111-2222-3333-444455556666"
	const rejected = "op-rejected-9999-8888-7777-666655554444"

	if _, _, err := journalOperationID("claim", "demo-1", committed); err != nil {
		t.Fatal(err)
	}

	path, previous, err := journalOperationID("claim", "demo-1", rejected)
	if err != nil {
		t.Fatal(err)
	}
	if previous != committed {
		t.Fatalf("previous = %q, want the committed operation %q", previous, committed)
	}

	// The daemon answers with a typed rejection: the displaced key must return.
	restoreJournaledOperationID(path, previous)

	got, _, err := readJournaledOperationID("claim", "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != committed {
		t.Errorf("journaled id = %q, want the committed operation %q restored", got, committed)
	}
}

// TestRestoreRemovesJournalWhenNothingWasDisplaced keeps a rejected first
// attempt from leaving a useless key behind.
func TestRestoreRemovesJournalWhenNothingWasDisplaced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, previous, err := journalOperationID("claim", "demo-1", "op-only-0000-1111-2222-333344445555")
	if err != nil {
		t.Fatal(err)
	}
	restoreJournaledOperationID(path, previous)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("journal still present after restoring an empty predecessor: %v", err)
	}
}

// TestSanitizeJournalNameContainsPathTraversal keeps a caller-supplied issue
// reference from steering the journal path.
func TestSanitizeJournalNameContainsPathTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, _, err := journalOperationID("claim", "../../escape", "op-traversal-1111-2222-3333-444455556666")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := expandOperationJournalDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("journal escaped its directory: %s", path)
	}
}
