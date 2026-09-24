package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
)

func lifecycleIssue(t *testing.T, h *coordinationRaceHarness, title string) (core.Issue, core.ClaimResponse) {
	t.Helper()
	ctx := context.Background()
	issue, err := CreateIssue(ctx, h.dbs[0], "race", core.CreateIssueRequest{ScopeKind: "project", Title: title, Actor: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(ctx, h.dbs[0], issue.ID, "worker", 3600)
	if err != nil {
		t.Fatal(err)
	}
	return issue, claim
}

func assertLifecycleCount(t *testing.T, h *coordinationRaceHarness, query string, args []any, want int) {
	t.Helper()
	var got int
	if err := h.dbs[1].QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: got %d, want %d", query, got, want)
	}
}

func requireIdempotencyConflict(t *testing.T, err error) {
	t.Helper()
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrIdempotencyConflict {
		t.Fatalf("error = %v, want idempotency_conflict", err)
	}
}

func TestHeartbeatOperationReplaysOriginalExpiry(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "heartbeat replay")
	req := core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 1800, OperationID: "heartbeat-replay-0001"}
	now := time.Now().UTC()
	first, err := HeartbeatLeaseWithOperation(ctx, h.dbs[0], issue.ID, req, now)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := HeartbeatLeaseWithOperation(ctx, h.dbs[1], issue.ID, req, now.Add(time.Minute))
	if err != nil || replay != first {
		t.Fatalf("replay expiry = %q, err = %v; want %q", replay, err, first)
	}
	var stored string
	if err := h.dbs[1].QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&stored); err != nil || stored != first {
		t.Fatalf("stored expiry = %q, err = %v; want %q", stored, err, first)
	}
	changed := req
	changed.TTLSeconds++
	_, err = HeartbeatLeaseWithOperation(ctx, h.dbs[1], issue.ID, changed, now.Add(time.Minute))
	requireIdempotencyConflict(t, err)
	if err := ReleaseLease(ctx, h.dbs[0], issue.ID, claim.LeaseToken, claim.LeaseGeneration, now); err != nil {
		t.Fatal(err)
	}
	replay, err = HeartbeatLeaseWithOperation(ctx, h.dbs[1], issue.ID, req, now.Add(2*time.Minute))
	if err != nil || replay != first {
		t.Fatalf("post-release replay = %q, err = %v", replay, err)
	}
	newAction := req
	newAction.OperationID = "heartbeat-new-0002"
	if _, err := HeartbeatLeaseWithOperation(ctx, h.dbs[1], issue.ID, newAction, now.Add(2*time.Minute)); err == nil {
		t.Fatal("new heartbeat with released lease succeeded")
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM operations WHERE operation_id = ?`, []any{req.OperationID}, 1)
}

func TestReleaseOperationReplaysAfterReplacement(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "release replay")
	req := core.ReleaseRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, OperationID: "release-replay-0001"}
	if err := ReleaseLeaseWithOperation(ctx, h.dbs[0], issue.ID, req, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	replacement, err := ClaimIssue(ctx, h.dbs[0], issue.ID, "replacement", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseLeaseWithOperation(ctx, h.dbs[1], issue.ID, req, time.Now().Add(time.Minute).UTC()); err != nil {
		t.Fatalf("replay after replacement: %v", err)
	}
	var token string
	if err := h.dbs[1].QueryRow(`SELECT lease_token FROM leases WHERE issue_id = ?`, issue.ID).Scan(&token); err != nil || token != replacement.LeaseToken {
		t.Fatalf("replacement token changed: %q, %v", token, err)
	}
	changed := req
	changed.LeaseGeneration++
	requireIdempotencyConflict(t, ReleaseLeaseWithOperation(ctx, h.dbs[1], issue.ID, changed, time.Now().UTC()))
	newAction := req
	newAction.OperationID = "release-new-0002"
	if err := ReleaseLeaseWithOperation(ctx, h.dbs[1], issue.ID, newAction, time.Now().UTC()); err == nil {
		t.Fatal("new release with old lease succeeded")
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_released'`, []any{issue.ID}, 1)
}

func TestHandoffOperationReplaysNoteAndRelease(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "handoff replay")
	req := core.HandoffRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "HANDOFF: owner review", OperationID: "handoff-replay-0001"}
	first, err := HandoffLease(ctx, h.dbs[0], issue.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssue(ctx, h.dbs[0], issue.ID, "replacement", 3600); err != nil {
		t.Fatal(err)
	}
	replay, err := HandoffLease(ctx, h.dbs[1], issue.ID, req)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("handoff replay = %+v, %v; want %+v", replay, err, first)
	}
	changed := req
	changed.Note = "HANDOFF: changed"
	_, err = HandoffLease(ctx, h.dbs[1], issue.ID, changed)
	requireIdempotencyConflict(t, err)
	newAction := req
	newAction.OperationID = "handoff-new-0002"
	if _, err := HandoffLease(ctx, h.dbs[1], issue.ID, newAction); err == nil {
		t.Fatal("new handoff with old lease succeeded")
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM notes WHERE issue_id = ?`, []any{issue.ID}, 1)
	assertLifecycleCount(t, h, `SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_released'`, []any{issue.ID}, 1)
}

func TestCloseOperationReplaysTerminalOutcome(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "close replay")
	req := core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "worker", Note: "finished", OperationID: "close-replay-0001"}
	first, err := CloseIssue(ctx, h.dbs[0], issue.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := CloseIssue(ctx, h.dbs[1], issue.ID, req)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("close replay = %+v, %v; want %+v", replay, err, first)
	}
	changed := req
	changed.Resolution = "cancelled"
	_, err = CloseIssue(ctx, h.dbs[1], issue.ID, changed)
	requireIdempotencyConflict(t, err)
	newAction := req
	newAction.OperationID = "close-new-0002"
	if _, err := CloseIssue(ctx, h.dbs[1], issue.ID, newAction); err == nil {
		t.Fatal("new close against terminal issue succeeded")
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM notes WHERE issue_id = ?`, []any{issue.ID}, 1)
	assertLifecycleCount(t, h, `SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_closed'`, []any{issue.ID}, 1)
	assertLifecycleCount(t, h, `SELECT version FROM issues WHERE id = ?`, []any{issue.ID}, claim.Version+1)
}

func TestUpdateOperationReplaysOriginalIssue(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "update replay")
	req := core.UpdateIssueRequest{Title: "first title", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Actor: "worker", OperationID: "update-replay-0001"}
	first, err := UpdateIssue(ctx, h.dbs[0], issue.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	later := req
	later.OperationID = "update-replay-0002"
	later.ExpectedVersion = first.Version
	later.Title = "later title"
	if _, err := UpdateIssue(ctx, h.dbs[0], issue.ID, later); err != nil {
		t.Fatal(err)
	}
	replay, err := UpdateIssue(ctx, h.dbs[1], issue.ID, req)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("update replay = %+v, %v; want %+v", replay, err, first)
	}
	changed := req
	changed.Title = "different"
	_, err = UpdateIssue(ctx, h.dbs[1], issue.ID, changed)
	requireIdempotencyConflict(t, err)
	newAction := req
	newAction.OperationID = "update-new-0003"
	if _, err := UpdateIssue(ctx, h.dbs[1], issue.ID, newAction); err == nil {
		t.Fatal("new update with stale version succeeded")
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_updated'`, []any{issue.ID}, 2)
	assertLifecycleCount(t, h, `SELECT version FROM issues WHERE id = ?`, []any{issue.ID}, claim.Version+2)
}

func TestLifecycleOperationKindConflictAndConcurrentHeartbeat(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, claim := lifecycleIssue(t, h, "concurrent heartbeat")
	req := core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 1800, OperationID: "concurrent-heartbeat-0001"}
	start := make(chan struct{})
	results := make(chan string, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			outcome, err := HeartbeatLeaseWithOperation(ctx, h.dbs[i], issue.ID, req, time.Now().UTC().Add(time.Duration(i)*time.Minute))
			results <- outcome
			errors <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original string
	for outcome := range results {
		if original != "" && outcome != original {
			t.Fatalf("concurrent replay outcomes differ: %q, %q", original, outcome)
		}
		original = outcome
	}
	assertLifecycleCount(t, h, `SELECT count(*) FROM operations WHERE operation_id = ?`, []any{req.OperationID}, 1)
	requireIdempotencyConflict(t, ReleaseLeaseWithOperation(ctx, h.dbs[1], issue.ID, core.ReleaseRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, OperationID: req.OperationID}, time.Now().UTC()))
}
