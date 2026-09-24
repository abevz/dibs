package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

func TestSafetyAuditSurvivesSecondConnectionWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "safety.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	st := NewStore(db)
	issue := seedClaimableIssue(t, db, "safety audit")
	first, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "first", TTLSeconds: 3600, InvocationMode: core.InvocationModeInteractive})
	if err != nil {
		t.Fatal(err)
	}
	// A failed claim is counted durably, while the issue event stream is untouched.
	if _, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "second", TTLSeconds: 3600}); err == nil {
		t.Fatal("expected claim conflict")
	}
	now := time.Now().UTC()
	heartbeat := core.HeartbeatRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, TTLSeconds: 3600, OperationID: "op-safety-heartbeat-1"}
	if _, err := st.HeartbeatLeaseWithOperation(ctx, issue.ID, heartbeat, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.HeartbeatLeaseWithOperation(ctx, issue.ID, heartbeat, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseLeaseWithOperation(ctx, issue.ID, core.ReleaseRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := invocationEventPayload(t, db, issue.ID, "issue_released")
	if payload["heartbeat_count"] != float64(1) {
		t.Fatalf("heartbeat count on release = %v", payload["heartbeat_count"])
	}
	if payload["last_heartbeat_at"] != now.Format(time.RFC3339) {
		t.Fatalf("last heartbeat = %v", payload["last_heartbeat_at"])
	}
	second, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "second", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.HeartbeatLeaseWithOperation(ctx, issue.ID, core.HeartbeatRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, TTLSeconds: 3600}, time.Now().UTC())
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("stale heartbeat error = %v", err)
	}
	eventsAfter, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("rejection appended an issue event: %d -> %d", len(eventsBefore), len(eventsAfter))
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	snapshot, err := NewStore(other).SafetySnapshot(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StaleRejections != 1 || snapshot.ClaimConflicts != 1 || snapshot.ActiveLeases != 1 || snapshot.LatestMigration != "0011_rejection_counts.sql" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.TopStaleHolders) != 1 || snapshot.TopStaleHolders[0].Holder != "first" || snapshot.TopStaleHolders[0].CurrentGenerationAtLastRejection != second.LeaseGeneration {
		t.Fatalf("stale holder = %+v", snapshot.TopStaleHolders)
	}
	var serialized string
	if err := other.QueryRowContext(ctx, `SELECT group_concat(issue_id || kind || holder || reason_code || last_invocation_mode, ' ') FROM rejection_counts`).Scan(&serialized); err != nil {
		t.Fatal(err)
	}
	all, _ := json.Marshal(snapshot)
	if strings.Contains(serialized, first.LeaseToken) || strings.Contains(string(all), first.LeaseToken) || strings.Contains(string(all), heartbeat.OperationID) {
		t.Fatal("secret leaked into safety audit")
	}
}

func TestExpiryEventCarriesHeartbeatSummary(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	st := NewStore(db)
	issue := seedClaimableIssue(t, db, "expired summary")
	claim, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "first", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.HeartbeatLeaseWithOperation(ctx, issue.ID, core.HeartbeatRequest{
		LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 3600,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `UPDATE leases SET expires_at = ? WHERE issue_id = ?`, expired, issue.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "replacement", TTLSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	payload := invocationEventPayload(t, db, issue.ID, "lease_expired")
	if payload["heartbeat_count"] != float64(1) || payload["last_heartbeat_at"] == "" || payload["lease_generation"] != float64(claim.LeaseGeneration) {
		t.Fatalf("expiry summary = %v", payload)
	}
}

func TestStaleLifecycleRejectionsDoNotAppendEvents(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	st := NewStore(db)
	issue := seedClaimableIssue(t, db, "stale lifecycle")
	first, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "first", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseLeaseWithOperation(ctx, issue.ID, core.ReleaseRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	replacement, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "replacement", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{"release", func() error {
			return st.ReleaseLeaseWithOperation(ctx, issue.ID, core.ReleaseRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration}, time.Now().UTC())
		}},
		{"handoff", func() error {
			_, err := st.HandoffLease(ctx, issue.ID, core.HandoffRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, Note: "HANDOFF: stale"})
			return err
		}},
		{"update", func() error {
			_, err := st.UpdateIssue(ctx, issue.ID, core.UpdateIssueRequest{Title: "stale", ExpectedVersion: replacement.Version, LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, Actor: "first"})
			return err
		}},
		{"close", func() error {
			_, err := st.CloseIssue(ctx, issue.ID, core.CloseIssueRequest{Resolution: "done", ExpectedVersion: replacement.Version, LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, Actor: "first"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
				t.Fatalf("stale %s = %v", tt.name, err)
			}
		})
	}
	after, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("stale operations appended events: %d -> %d", len(before), len(after))
	}
	snapshot, err := st.SafetySnapshot(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StaleRejections != int64(len(tests)) {
		t.Fatalf("stale rejection count = %d", snapshot.StaleRejections)
	}
}

func TestOperatorVersionRejectionsHaveDurableCountsWithoutEvents(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	st := NewStore(db)
	issue := seedClaimableIssue(t, db, "operator conflicts")
	claim, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "worker", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, closeErr := st.OperatorCloseIssue(ctx, issue.ID, core.OperatorCloseIssueRequest{Resolution: "done", ExpectedVersion: issue.Version, Actor: "operator", Reason: "stale close"})
	_, releaseErr := st.OperatorReleaseIssue(ctx, issue.ID, core.OperatorReleaseIssueRequest{ExpectedVersion: issue.Version, Actor: "operator", Reason: "stale release"})
	for _, err := range []error{closeErr, releaseErr} {
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrConflict {
			t.Fatalf("operator conflict = %v", err)
		}
	}
	after, err := st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("operator rejection appended event")
	}
	if _, err := st.OperatorCloseIssue(ctx, issue.ID, core.OperatorCloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, Actor: "operator", Reason: "finish"}); err != nil {
		t.Fatal(err)
	}
	before, err = st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.OperatorReopenIssue(ctx, issue.ID, core.OperatorReopenIssueRequest{ExpectedVersion: claim.Version, Actor: "operator", Reason: "stale reopen"})
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrConflict {
		t.Fatalf("operator reopen = %v", err)
	}
	after, err = st.ListEvents(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("operator reopen rejection appended event")
	}
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(sum(count), 0) FROM rejection_counts WHERE issue_id = ? AND holder = 'operator'`, issue.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("durable operator conflicts = %d", n)
	}
}
