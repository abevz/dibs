package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

const testOperationID = "op-11111111-2222-3333-4444-555555555555"

func claimReq(holder string, ttl int, operationID string) core.ClaimRequest {
	return core.ClaimRequest{Holder: holder, TTLSeconds: ttl, OperationID: operationID}
}

func seedClaimableIssue(t *testing.T, db *sql.DB, title string) core.Issue {
	t.Helper()
	if _, err := CreateProject(context.Background(), db, "ops", "Ops", ""); err != nil && !isSQLiteConstraintError(err) {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "ops", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     title,
	})
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

// TestClaimOperationReplayReturnsOriginalOutcome is the core AFC-SDD-0159
// property: a client that loses the response to a committed claim retries the
// same operation and receives the original outcome, token included, with no
// second claim and no operator involvement.
func TestClaimOperationReplayReturnsOriginalOutcome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "replay")

	first, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	replay, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}

	if replay.LeaseToken != first.LeaseToken {
		t.Errorf("replay token = %q, want original %q", replay.LeaseToken, first.LeaseToken)
	}
	if replay.LeaseGeneration != first.LeaseGeneration {
		t.Errorf("replay generation = %d, want original %d", replay.LeaseGeneration, first.LeaseGeneration)
	}
	if replay.AttemptID != first.AttemptID {
		t.Errorf("replay attempt = %q, want original %q", replay.AttemptID, first.AttemptID)
	}
	if replay.ExpiresAt != first.ExpiresAt {
		t.Errorf("replay expiry = %q, want original %q", replay.ExpiresAt, first.ExpiresAt)
	}
	if replay.Version != first.Version {
		t.Errorf("replay version = %d, want original %d", replay.Version, first.Version)
	}

	// The replay must not have executed a second mutation.
	assertSingleClaimEffect(t, db, issue.ID, first.LeaseGeneration)

	// The replayed token must actually work, not merely look right.
	if _, err := HeartbeatLease(ctx, db, issue.ID, replay.LeaseToken, replay.LeaseGeneration, 3600, time.Now().UTC()); err != nil {
		t.Fatalf("heartbeat with replayed token: %v", err)
	}
}

// assertSingleClaimEffect proves the underlying mutation ran exactly once:
// one lease, one unchanged fencing generation, one claim event.
func assertSingleClaimEffect(t *testing.T, db *sql.DB, issueID string, wantGeneration int64) {
	t.Helper()
	ctx := context.Background()

	var leases int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leases WHERE issue_id = ?`, issueID).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != 1 {
		t.Errorf("lease rows = %d, want 1", leases)
	}

	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT lease_generation FROM issues WHERE id = ?`, issueID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != wantGeneration {
		t.Errorf("issue lease_generation = %d, want %d (replay must not advance fencing)", generation, wantGeneration)
	}

	var claimEvents int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_claimed'`, issueID,
	).Scan(&claimEvents); err != nil {
		t.Fatal(err)
	}
	if claimEvents != 1 {
		t.Errorf("issue_claimed events = %d, want 1", claimEvents)
	}

	var operations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE target_id = ?`, issueID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Errorf("ledger rows = %d, want 1", operations)
	}
}

// TestClaimOperationConflictsFailClosed covers reuse of one operation ID for a
// materially different request. Every case must refuse without mutating.
func TestClaimOperationConflictsFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "conflict-a")
	other := seedClaimableIssue(t, db, "conflict-b")

	first, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	tests := []struct {
		name    string
		issueID string
		req     core.ClaimRequest
	}{
		{
			name:    "different target issue",
			issueID: other.ID,
			req:     claimReq("worker-a", 3600, testOperationID),
		},
		{
			name:    "different ttl argument",
			issueID: issue.ID,
			req:     claimReq("worker-a", 60, testOperationID),
		},
		{
			name:    "different holder attribution",
			issueID: issue.ID,
			req:     claimReq("worker-b", 3600, testOperationID),
		},
		{
			name:    "different session correlation",
			issueID: issue.ID,
			req:     core.ClaimRequest{Holder: "worker-a", TTLSeconds: 3600, SessionID: "s-2", OperationID: testOperationID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ClaimIssueWithOperation(ctx, db, test.issueID, test.req)
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != core.ErrIdempotencyConflict {
				t.Fatalf("err = %v, want %s", err, core.ErrIdempotencyConflict)
			}
		})
	}

	// The refused requests must have changed nothing.
	assertSingleClaimEffect(t, db, issue.ID, first.LeaseGeneration)

	var otherLeases int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leases WHERE issue_id = ?`, other.ID).Scan(&otherLeases); err != nil {
		t.Fatal(err)
	}
	if otherLeases != 0 {
		t.Errorf("conflicting request claimed the other issue: %d leases", otherLeases)
	}
}

// TestClaimOperationKindMismatchFailsClosed proves the ledger binds the
// operation kind, so an ID recorded for one kind of mutation cannot replay as
// another even with a matching target.
func TestClaimOperationKindMismatchFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "kind")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := core.OperationFingerprint(map[string]string{"issue_id": issue.ID})
	if err := recordOperation(ctx, tx, testOperationID, "close", issue.ID, "worker-a", fingerprint, map[string]string{"status": "done"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrIdempotencyConflict {
		t.Fatalf("err = %v, want %s", err, core.ErrIdempotencyConflict)
	}
	if !strings.Contains(apiErr.Message, "close") {
		t.Errorf("message should name the recorded kind, got %q", apiErr.Message)
	}
}

// TestNewOperationIDCannotRecoverActiveLease is the security boundary against
// AFC-SDD-0154 regression. A second worker — or the same worker having lost
// its operation ID — gets normal lease_held behavior and never sees the token,
// no matter how exactly it reproduces holder and session identity.
func TestNewOperationIDCannotRecoverActiveLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "no-recovery")

	first, err := ClaimIssueWithOperation(ctx, db, issue.ID,
		core.ClaimRequest{Holder: "claude-code", TTLSeconds: 3600, SessionID: "session-a", OperationID: testOperationID})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	// Identical holder and session, different operation ID: still lease_held.
	sameIdentity := core.ClaimRequest{
		Holder: "claude-code", TTLSeconds: 3600, SessionID: "session-a",
		OperationID: "op-99999999-8888-7777-6666-555555555555",
	}
	_, err = ClaimIssueWithOperation(ctx, db, issue.ID, sameIdentity)
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
		t.Fatalf("err = %v, want %s", err, core.ErrLeaseHeld)
	}
	if strings.Contains(apiErr.Message, first.LeaseToken) {
		t.Fatal("lease_held message disclosed the active lease token")
	}

	// The same must hold with no operation ID at all (pre-ledger client).
	_, err = ClaimIssueWithOperation(ctx, db, issue.ID,
		core.ClaimRequest{Holder: "claude-code", TTLSeconds: 3600, SessionID: "session-a"})
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
		t.Fatalf("legacy claim err = %v, want %s", err, core.ErrLeaseHeld)
	}

	assertSingleClaimEffect(t, db, issue.ID, first.LeaseGeneration)
}

// TestClaimWithoutOperationIDIsUnchanged pins backward compatibility: a client
// that never sends an operation ID behaves exactly as before and writes no
// ledger row.
func TestClaimWithoutOperationIDIsUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "legacy")

	first, err := ClaimIssue(ctx, db, issue.ID, "worker-a", 3600)
	if err != nil {
		t.Fatalf("legacy claim: %v", err)
	}
	if first.LeaseToken == "" {
		t.Fatal("expected a lease token")
	}

	var operations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 0 {
		t.Errorf("ledger rows = %d, want 0 for a claim without an operation ID", operations)
	}
}

// TestOperationIDValidationRejectsWeakKeys keeps the idempotency key strong
// enough to be unguessable, since knowing it authorizes replay of a token.
func TestOperationIDValidationRejectsWeakKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "weak")

	for _, weak := range []string{"1", "short", " padded-operation-id ", "has space in it"} {
		_, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, weak))
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrValidationFailed {
			t.Errorf("operation_id %q: err = %v, want %s", weak, err, core.ErrValidationFailed)
		}
	}
}

// TestClaimReplayAfterLeaseReleased proves a replay answers "what did my
// committed operation do?" rather than "what is true now": the outcome is
// still returned after the lease it created has been released.
func TestClaimReplayAfterLeaseReleased(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "released")

	first, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := ReleaseLease(ctx, db, issue.ID, first.LeaseToken, first.LeaseGeneration, time.Now().UTC()); err != nil {
		t.Fatalf("release: %v", err)
	}

	replay, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("replay after release: %v", err)
	}
	if replay.LeaseToken != first.LeaseToken || replay.LeaseGeneration != first.LeaseGeneration {
		t.Error("replay after release did not return the original committed outcome")
	}

	// The replay must not have re-created the released lease.
	var leases int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leases WHERE issue_id = ?`, issue.ID).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != 0 {
		t.Errorf("lease rows = %d, want 0; replay must not re-execute the claim", leases)
	}
}

// TestConcurrentIdenticalClaimOperations runs the canonical duplicate case
// across independent production-initialized connections, in the style
// afc-110 established. Only one logical mutation may exist.
func TestConcurrentIdenticalClaimOperations(t *testing.T) {
	t.Parallel()
	harness := newCoordinationRaceHarness(t, 2)
	issue := harness.newIssue(t, "concurrent replay")

	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		operationID := testOperationID + "-" + strings.Repeat("x", schedule%3) + itoa(schedule)
		issue := issue
		if schedule > 0 {
			issue = harness.newIssue(t, "concurrent replay")
		}

		results := make(chan coordinationResult[core.ClaimResponse], 2)
		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup

		for connection := 0; connection < 2; connection++ {
			done.Add(1)
			go func(db *sql.DB) {
				defer done.Done()
				start.Wait()
				resp, err := ClaimIssueWithOperation(context.Background(), db, issue.ID,
					claimReq("worker-a", 3600, operationID))
				results <- coordinationResult[core.ClaimResponse]{value: resp, err: err}
			}(harness.dbs[connection])
		}

		start.Done()
		done.Wait()
		close(results)

		var responses []core.ClaimResponse
		for result := range results {
			if result.err != nil {
				t.Fatalf("schedule %d: concurrent identical operation failed: %v", schedule, result.err)
			}
			responses = append(responses, result.value)
		}
		if len(responses) != 2 {
			t.Fatalf("schedule %d: got %d responses, want 2", schedule, len(responses))
		}
		if responses[0].LeaseToken != responses[1].LeaseToken {
			t.Fatalf("schedule %d: two valid capabilities issued (%q and %q)",
				schedule, responses[0].LeaseToken, responses[1].LeaseToken)
		}
		if responses[0].LeaseGeneration != responses[1].LeaseGeneration {
			t.Fatalf("schedule %d: divergent fencing generations %d and %d",
				schedule, responses[0].LeaseGeneration, responses[1].LeaseGeneration)
		}
		assertSingleClaimEffect(t, harness.dbs[0], issue.ID, responses[0].LeaseGeneration)
	}
}

// TestConcurrentConflictingClaimOperations covers the same operation ID used
// concurrently for different requests: exactly one commits, the other fails
// closed with a typed conflict.
func TestConcurrentConflictingClaimOperations(t *testing.T) {
	t.Parallel()
	harness := newCoordinationRaceHarness(t, 2)

	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		issueA := harness.newIssue(t, "conflict race a")
		issueB := harness.newIssue(t, "conflict race b")
		operationID := testOperationID + "-race-" + itoa(schedule)

		results := make(chan coordinationResult[core.ClaimResponse], 2)
		targets := []string{issueA.ID, issueB.ID}
		var start sync.WaitGroup
		start.Add(1)
		var done sync.WaitGroup

		for connection := 0; connection < 2; connection++ {
			done.Add(1)
			go func(db *sql.DB, target string) {
				defer done.Done()
				start.Wait()
				resp, err := ClaimIssueWithOperation(context.Background(), db, target,
					claimReq("worker-a", 3600, operationID))
				results <- coordinationResult[core.ClaimResponse]{value: resp, err: err}
			}(harness.dbs[connection], targets[connection])
		}

		start.Done()
		done.Wait()
		close(results)

		successes, conflicts := 0, 0
		for result := range results {
			switch {
			case result.err == nil:
				successes++
			default:
				var apiErr core.APIError
				if errors.As(result.err, &apiErr) && apiErr.Code == core.ErrIdempotencyConflict {
					conflicts++
					continue
				}
				t.Fatalf("schedule %d: unexpected error %v", schedule, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("schedule %d: successes=%d conflicts=%d, want exactly 1 and 1",
				schedule, successes, conflicts)
		}

		var ledgerRows int
		if err := harness.dbs[0].QueryRowContext(context.Background(),
			`SELECT count(*) FROM operations WHERE operation_id = ?`, operationID).Scan(&ledgerRows); err != nil {
			t.Fatal(err)
		}
		if ledgerRows != 1 {
			t.Fatalf("schedule %d: ledger rows = %d, want 1", schedule, ledgerRows)
		}
	}
}

// TestClaimReplaySurvivesRestart proves the ledger is durable rather than an
// in-process cache: the outcome replays through a completely new connection
// opened after the original one is closed.
func TestClaimReplaySurvivesRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "restart.db")

	firstBoot, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, firstBoot, migrations.FS); err != nil {
		t.Fatal(err)
	}
	issue := seedClaimableIssue(t, firstBoot, "restart")
	first, err := ClaimIssueWithOperation(ctx, firstBoot, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := firstBoot.Close(); err != nil {
		t.Fatal(err)
	}

	secondBoot, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondBoot.Close() })
	if err := Migrate(ctx, secondBoot, migrations.FS); err != nil {
		t.Fatal(err)
	}

	replay, err := ClaimIssueWithOperation(ctx, secondBoot, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	if replay.LeaseToken != first.LeaseToken || replay.LeaseGeneration != first.LeaseGeneration {
		t.Error("replay after restart did not return the original committed outcome")
	}
	assertSingleClaimEffect(t, secondBoot, issue.ID, first.LeaseGeneration)
}

// TestLeaseTokenNeverReachesEventsOrLedgerReads pins the secrecy requirement:
// the token must not leak through the audit trail, and the ledger's own
// columns must not expose it outside an exact replay.
func TestLeaseTokenNeverReachesEventsOrLedgerReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	issue := seedClaimableIssue(t, db, "secrecy")

	first, err := ClaimIssueWithOperation(ctx, db, issue.ID, claimReq("worker-a", 3600, testOperationID))
	if err != nil {
		t.Fatal(err)
	}

	var payloads string
	if err := db.QueryRowContext(ctx,
		`SELECT coalesce(group_concat(payload_json, ' '), '') FROM events WHERE issue_id = ?`, issue.ID,
	).Scan(&payloads); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payloads, first.LeaseToken) {
		t.Error("lease token leaked into the event stream")
	}
	if strings.Contains(payloads, testOperationID) {
		t.Error("operation_id leaked into the event stream")
	}

	// Issue reads must expose neither the operation ID nor the token.
	_, lease, err := GetIssue(ctx, db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected an active lease")
	}
	encoded := mustJSON(t, lease)
	if strings.Contains(encoded, first.LeaseToken) {
		t.Error("issue read exposed the lease token")
	}
	if strings.Contains(encoded, testOperationID) {
		t.Error("issue read exposed the operation_id")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
