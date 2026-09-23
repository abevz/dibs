package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

const coordinationRaceSchedules = 100

type coordinationRaceHarness struct {
	dbs []*sql.DB
}

type coordinationResult[T any] struct {
	value T
	err   error
}

func TestCoordinationRaceMatrix(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "two workers claim one ready issue", run: testRaceConcurrentClaims},
		{name: "old heartbeat versus expiry and reclaim", run: testRaceHeartbeatAndReclaim},
		{name: "old close after reclaim", run: testRaceCloseAfterReclaim},
		{name: "handoff versus heartbeat", run: testRaceHandoffAndHeartbeat},
		{name: "request cancellation between authorization and write", run: testRaceCancellationBeforeWrite},
		{name: "timeout after commit then retry", run: testRaceCommittedCloseRetry},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

// TestMultiConnectionDependencyCycleSerialization complements the six-race
// lifecycle matrix with the afc-107 dependency invariant that afc-110 owns in
// traceability. Two production-initialized handles must serialize opposite
// edges so exactly one edge and one audit event commit.
func TestMultiConnectionDependencyCycleSerialization(t *testing.T) {
	harness := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		a := harness.newIssue(t, fmt.Sprintf("dependency A %d", schedule))
		b := harness.newIssue(t, fmt.Sprintf("dependency B %d", schedule))
		start := make(chan struct{})
		results := make(chan error, 2)
		edges := []struct {
			issue     string
			dependsOn string
		}{
			{issue: a.ID, dependsOn: b.ID},
			{issue: b.ID, dependsOn: a.ID},
		}
		for index, edge := range edges {
			index, edge := index, edge
			go func() {
				<-start
				results <- AddDependency(context.Background(), harness.dbs[index], edge.issue, core.AddDependencyRequest{
					DependsOn: edge.dependsOn,
					Kind:      "blocks",
					Actor:     fmt.Sprintf("worker-%d", index),
				})
			}()
		}
		close(start)

		committed := 0
		cycles := 0
		for attempt := 0; attempt < 2; attempt++ {
			select {
			case err := <-results:
				if err == nil {
					committed++
					continue
				}
				requireCoordinationErrorCode(t, err, core.ErrDependencyCycle)
				cycles++
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for dependency mutation")
			}
		}
		if committed != 1 || cycles != 1 {
			t.Fatalf("schedule %d committed/cycles = %d/%d, want 1/1", schedule, committed, cycles)
		}

		var edgeCount int
		if err := harness.dbs[0].QueryRow(
			`SELECT count(*) FROM dependencies WHERE issue_id IN (?, ?)`, a.ID, b.ID,
		).Scan(&edgeCount); err != nil {
			t.Fatal(err)
		}
		if edgeCount != 1 {
			t.Fatalf("schedule %d dependency edges = %d, want 1", schedule, edgeCount)
		}
		var eventCount int
		if err := harness.dbs[0].QueryRow(
			`SELECT count(*) FROM events WHERE issue_id IN (?, ?) AND event_type = 'dependency_added'`, a.ID, b.ID,
		).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		if eventCount != 1 {
			t.Fatalf("schedule %d dependency_added events = %d, want 1", schedule, eventCount)
		}

		gotA, _, err := GetIssue(context.Background(), harness.dbs[0], a.ID)
		if err != nil {
			t.Fatal(err)
		}
		gotB, _, err := GetIssue(context.Background(), harness.dbs[1], b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if gotA.Blocked == gotB.Blocked {
			t.Fatalf("schedule %d blocked states = %t/%t, want one dependant", schedule, gotA.Blocked, gotB.Blocked)
		}
	}
}

func testRaceConcurrentClaims(t *testing.T) {
	harness := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		issue := harness.newIssue(t, fmt.Sprintf("concurrent claim %d", schedule))
		start := make(chan struct{})
		results := make(chan coordinationResult[core.ClaimResponse], 2)

		for worker := 0; worker < 2; worker++ {
			worker := worker
			go func() {
				<-start
				claim, err := ClaimIssueWithSession(context.Background(), harness.dbs[worker], issue.ID,
					fmt.Sprintf("worker-%d", worker), 60, fmt.Sprintf("schedule-%d", schedule))
				results <- coordinationResult[core.ClaimResponse]{value: claim, err: err}
			}()
		}
		close(start)

		var winner core.ClaimResponse
		winners := 0
		losers := 0
		for attempt := 0; attempt < 2; attempt++ {
			result := awaitCoordinationResult(t, results)
			if result.err == nil {
				winner = result.value
				winners++
				continue
			}
			requireCoordinationErrorCode(t, result.err, core.ErrLeaseHeld)
			losers++
		}
		if winners != 1 || losers != 1 {
			t.Fatalf("schedule %d winners/losers = %d/%d, want 1/1", schedule, winners, losers)
		}

		got, lease, err := GetIssue(context.Background(), harness.dbs[0], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "in_progress" || got.Version != issue.Version+1 {
			t.Fatalf("schedule %d issue = %+v, want one claim transition", schedule, got)
		}
		if lease == nil || lease.LeaseToken != winner.LeaseToken || lease.LeaseGeneration != 1 || winner.LeaseGeneration != 1 {
			t.Fatalf("schedule %d lease = %+v, winner = %+v", schedule, lease, winner)
		}
		requireLeaseCount(t, harness.dbs[0], issue.ID, 1)
		events := requireCoordinationEvents(t, harness.dbs[0], issue.ID,
			[]string{"issue_created", "issue_claimed"}, winner.LeaseToken)
		requireEventGeneration(t, events, "issue_claimed", 0, winner.LeaseGeneration)
	}
}

func testRaceHeartbeatAndReclaim(t *testing.T) {
	t.Run("renewal commits first", func(t *testing.T) {
		harness := newCoordinationRaceHarness(t, 2)
		for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
			issue := harness.newIssue(t, fmt.Sprintf("heartbeat wins %d", schedule))
			first, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "old-worker", 60)
			if err != nil {
				t.Fatal(err)
			}

			heartbeatCtx, entered, release := newCoordinationProofBarrier(t, coordinationProofHeartbeatBeforeCommit)
			heartbeatResult := make(chan coordinationResult[string], 1)
			go func() {
				expiresAt, err := HeartbeatLease(heartbeatCtx, harness.dbs[0], issue.ID,
					first.LeaseToken, first.LeaseGeneration, 120, time.Now().UTC())
				heartbeatResult <- coordinationResult[string]{value: expiresAt, err: err}
			}()
			waitForCoordinationProofPoint(t, entered)

			claimResult := make(chan coordinationResult[core.ClaimResponse], 1)
			go func() {
				claim, err := ClaimIssue(context.Background(), harness.dbs[1], issue.ID, "replacement", 60)
				claimResult <- coordinationResult[core.ClaimResponse]{value: claim, err: err}
			}()
			release()

			heartbeat := awaitCoordinationResult(t, heartbeatResult)
			if heartbeat.err != nil {
				t.Fatalf("schedule %d heartbeat: %v", schedule, heartbeat.err)
			}
			reclaim := awaitCoordinationResult(t, claimResult)
			requireCoordinationErrorCode(t, reclaim.err, core.ErrLeaseHeld)

			got, lease, err := GetIssue(context.Background(), harness.dbs[1], issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "in_progress" || lease == nil || lease.LeaseToken != first.LeaseToken ||
				lease.LeaseGeneration != first.LeaseGeneration || lease.ExpiresAt != heartbeat.value {
				t.Fatalf("schedule %d renewed state = issue=%+v lease=%+v", schedule, got, lease)
			}
			requireLeaseCount(t, harness.dbs[1], issue.ID, 1)
			events := requireCoordinationEvents(t, harness.dbs[1], issue.ID,
				[]string{"issue_created", "issue_claimed"}, first.LeaseToken)
			requireEventGeneration(t, events, "issue_claimed", 0, first.LeaseGeneration)
		}
	})

	t.Run("reclaim commits first", func(t *testing.T) {
		harness := newCoordinationRaceHarness(t, 2)
		for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
			issue := harness.newIssue(t, fmt.Sprintf("reclaim wins %d", schedule))
			first, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "old-worker", 60)
			if err != nil {
				t.Fatal(err)
			}
			forceLeaseExpiry(t, harness.dbs[0], issue.ID)

			claimCtx, entered, release := newCoordinationProofBarrier(t, coordinationProofClaimBeforeCommit)
			claimResult := make(chan coordinationResult[core.ClaimResponse], 1)
			go func() {
				claim, err := ClaimIssue(claimCtx, harness.dbs[1], issue.ID, "replacement", 60)
				claimResult <- coordinationResult[core.ClaimResponse]{value: claim, err: err}
			}()
			waitForCoordinationProofPoint(t, entered)

			heartbeatResult := make(chan coordinationResult[string], 1)
			go func() {
				expiresAt, err := HeartbeatLease(context.Background(), harness.dbs[0], issue.ID,
					first.LeaseToken, first.LeaseGeneration, 120, time.Now().UTC())
				heartbeatResult <- coordinationResult[string]{value: expiresAt, err: err}
			}()
			release()

			replacement := awaitCoordinationResult(t, claimResult)
			if replacement.err != nil {
				t.Fatalf("schedule %d reclaim: %v", schedule, replacement.err)
			}
			heartbeat := awaitCoordinationResult(t, heartbeatResult)
			requireCoordinationErrorCode(t, heartbeat.err, core.ErrLeaseExpired)

			got, lease, err := GetIssue(context.Background(), harness.dbs[0], issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "in_progress" || lease == nil || lease.LeaseToken != replacement.value.LeaseToken ||
				lease.LeaseGeneration != first.LeaseGeneration+1 || lease.ExpiresAt != replacement.value.ExpiresAt {
				t.Fatalf("schedule %d replacement state = issue=%+v lease=%+v", schedule, got, lease)
			}
			requireLeaseCount(t, harness.dbs[0], issue.ID, 1)
			events := requireCoordinationEvents(t, harness.dbs[0], issue.ID,
				[]string{"issue_created", "issue_claimed", "lease_expired", "issue_claimed"},
				first.LeaseToken, replacement.value.LeaseToken)
			requireEventGeneration(t, events, "issue_claimed", 0, first.LeaseGeneration)
			requireEventGeneration(t, events, "issue_claimed", 1, replacement.value.LeaseGeneration)
			requireEventGeneration(t, events, "lease_expired", 0, first.LeaseGeneration)
		}
	})
}

func testRaceCloseAfterReclaim(t *testing.T) {
	harness := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		issue := harness.newIssue(t, fmt.Sprintf("stale close %d", schedule))
		first, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "old-worker", 60)
		if err != nil {
			t.Fatal(err)
		}
		forceLeaseExpiry(t, harness.dbs[0], issue.ID)

		claimCtx, entered, release := newCoordinationProofBarrier(t, coordinationProofClaimBeforeCommit)
		claimResult := make(chan coordinationResult[core.ClaimResponse], 1)
		go func() {
			claim, err := ClaimIssue(claimCtx, harness.dbs[1], issue.ID, "replacement", 60)
			claimResult <- coordinationResult[core.ClaimResponse]{value: claim, err: err}
		}()
		waitForCoordinationProofPoint(t, entered)

		closeResult := make(chan coordinationResult[core.CloseIssueResult], 1)
		go func() {
			closed, err := CloseIssue(context.Background(), harness.dbs[0], issue.ID, core.CloseIssueRequest{
				Resolution:      "done",
				ExpectedVersion: first.Version + 1,
				LeaseToken:      first.LeaseToken,
				LeaseGeneration: first.LeaseGeneration,
				Actor:           "old-worker",
				Note:            "must not be written",
			})
			closeResult <- coordinationResult[core.CloseIssueResult]{value: closed, err: err}
		}()
		release()

		replacement := awaitCoordinationResult(t, claimResult)
		if replacement.err != nil {
			t.Fatalf("schedule %d reclaim: %v", schedule, replacement.err)
		}
		staleClose := awaitCoordinationResult(t, closeResult)
		requireCoordinationErrorCode(t, staleClose.err, core.ErrLeaseExpired)

		got, lease, err := GetIssue(context.Background(), harness.dbs[1], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "in_progress" || got.Version != replacement.value.Version || lease == nil ||
			lease.LeaseToken != replacement.value.LeaseToken || lease.LeaseGeneration != replacement.value.LeaseGeneration {
			t.Fatalf("schedule %d stale close changed replacement state: issue=%+v lease=%+v", schedule, got, lease)
		}
		requireLeaseCount(t, harness.dbs[1], issue.ID, 1)
		notes, err := ListNotes(context.Background(), harness.dbs[1], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 0 {
			t.Fatalf("schedule %d stale close wrote notes: %+v", schedule, notes)
		}
		events := requireCoordinationEvents(t, harness.dbs[1], issue.ID,
			[]string{"issue_created", "issue_claimed", "lease_expired", "issue_claimed"},
			first.LeaseToken, replacement.value.LeaseToken)
		requireEventGeneration(t, events, "issue_claimed", 1, replacement.value.LeaseGeneration)
	}
}

func testRaceHandoffAndHeartbeat(t *testing.T) {
	tests := []struct {
		name         string
		handoffFirst bool
	}{
		{name: "heartbeat commits first", handoffFirst: false},
		{name: "handoff commits first", handoffFirst: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCoordinationRaceHarness(t, 2)
			for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
				issue := harness.newIssue(t, fmt.Sprintf("handoff heartbeat %t %d", test.handoffFirst, schedule))
				claim, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "worker", 60)
				if err != nil {
					t.Fatal(err)
				}

				heartbeatResult := make(chan coordinationResult[string], 1)
				handoffResult := make(chan coordinationResult[core.HandoffResponse], 1)
				var entered <-chan struct{}
				var release func()

				if test.handoffFirst {
					handoffCtx, handoffEntered, handoffRelease := newCoordinationProofBarrier(t, coordinationProofHandoffBeforeCommit)
					entered, release = handoffEntered, handoffRelease
					go func() {
						handoff, err := HandoffLease(handoffCtx, harness.dbs[1], issue.ID, core.HandoffRequest{
							LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
							Note: "HANDOFF: deterministic race proof",
						})
						handoffResult <- coordinationResult[core.HandoffResponse]{value: handoff, err: err}
					}()
					waitForCoordinationProofPoint(t, entered)
					go func() {
						expiresAt, err := HeartbeatLease(context.Background(), harness.dbs[0], issue.ID,
							claim.LeaseToken, claim.LeaseGeneration, 120, time.Now().UTC())
						heartbeatResult <- coordinationResult[string]{value: expiresAt, err: err}
					}()
				} else {
					heartbeatCtx, heartbeatEntered, heartbeatRelease := newCoordinationProofBarrier(t, coordinationProofHeartbeatBeforeCommit)
					entered, release = heartbeatEntered, heartbeatRelease
					go func() {
						expiresAt, err := HeartbeatLease(heartbeatCtx, harness.dbs[0], issue.ID,
							claim.LeaseToken, claim.LeaseGeneration, 120, time.Now().UTC())
						heartbeatResult <- coordinationResult[string]{value: expiresAt, err: err}
					}()
					waitForCoordinationProofPoint(t, entered)
					go func() {
						handoff, err := HandoffLease(context.Background(), harness.dbs[1], issue.ID, core.HandoffRequest{
							LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
							Note: "HANDOFF: deterministic race proof",
						})
						handoffResult <- coordinationResult[core.HandoffResponse]{value: handoff, err: err}
					}()
				}
				release()

				handoff := awaitCoordinationResult(t, handoffResult)
				if handoff.err != nil {
					t.Fatalf("schedule %d handoff: %v", schedule, handoff.err)
				}
				if handoff.value.Note.ID == "" {
					t.Fatalf("schedule %d handoff returned no note", schedule)
				}
				heartbeat := awaitCoordinationResult(t, heartbeatResult)
				if test.handoffFirst {
					requireCoordinationErrorCode(t, heartbeat.err, core.ErrLeaseExpired)
				} else if heartbeat.err != nil {
					t.Fatalf("schedule %d heartbeat: %v", schedule, heartbeat.err)
				}

				got, lease, err := GetIssue(context.Background(), harness.dbs[0], issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != "open" || got.Version != claim.Version+1 || lease != nil {
					t.Fatalf("schedule %d handoff state = issue=%+v lease=%+v", schedule, got, lease)
				}
				requireLeaseCount(t, harness.dbs[0], issue.ID, 0)
				notes, err := ListNotes(context.Background(), harness.dbs[0], issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(notes) != 1 || notes[0].Body != "HANDOFF: deterministic race proof" {
					t.Fatalf("schedule %d notes = %+v", schedule, notes)
				}
				events := requireCoordinationEvents(t, harness.dbs[0], issue.ID,
					[]string{"issue_created", "issue_claimed", "note_added", "issue_released"}, claim.LeaseToken)
				requireEventGeneration(t, events, "issue_released", 0, claim.LeaseGeneration)
			}
		})
	}
}

func testRaceCancellationBeforeWrite(t *testing.T) {
	harness := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		issue := harness.newIssue(t, fmt.Sprintf("cancel before write %d", schedule))
		claim, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "worker", 60)
		if err != nil {
			t.Fatal(err)
		}

		baseCtx, cancel := context.WithCancel(context.Background())
		hookCalled := make(chan struct{})
		var once sync.Once
		ctx := context.WithValue(baseCtx, coordinationProofHookContextKey{}, coordinationProofHook(func(point coordinationProofPoint) {
			if point == coordinationProofUpdateAfterAuthorize {
				once.Do(func() { close(hookCalled) })
				cancel()
			}
		}))
		_, err = UpdateIssue(ctx, harness.dbs[0], issue.ID, core.UpdateIssueRequest{
			Title:           "must not commit",
			ExpectedVersion: claim.Version,
			LeaseToken:      claim.LeaseToken,
			LeaseGeneration: claim.LeaseGeneration,
			Actor:           "worker",
		})
		cancel()
		waitForCoordinationProofPoint(t, hookCalled)
		if err == nil {
			t.Fatalf("schedule %d cancelled update committed", schedule)
		}

		got, lease, err := GetIssue(context.Background(), harness.dbs[1], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != issue.Title || got.Status != "in_progress" || got.Version != claim.Version || lease == nil ||
			lease.LeaseToken != claim.LeaseToken || lease.LeaseGeneration != claim.LeaseGeneration {
			t.Fatalf("schedule %d cancelled update left partial state: issue=%+v lease=%+v", schedule, got, lease)
		}
		requireLeaseCount(t, harness.dbs[1], issue.ID, 1)
		events := requireCoordinationEvents(t, harness.dbs[1], issue.ID,
			[]string{"issue_created", "issue_claimed"}, claim.LeaseToken)
		requireEventGeneration(t, events, "issue_claimed", 0, claim.LeaseGeneration)
	}
}

func testRaceCommittedCloseRetry(t *testing.T) {
	harness := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < coordinationRaceSchedules; schedule++ {
		issue := harness.newIssue(t, fmt.Sprintf("committed close retry %d", schedule))
		claim, err := ClaimIssue(context.Background(), harness.dbs[0], issue.ID, "worker", 60)
		if err != nil {
			t.Fatal(err)
		}
		req := core.CloseIssueRequest{
			Resolution:      "done",
			ExpectedVersion: claim.Version,
			LeaseToken:      claim.LeaseToken,
			LeaseGeneration: claim.LeaseGeneration,
			Actor:           "worker",
			Note:            "close committed before response loss",
		}
		if _, err := CloseIssue(context.Background(), harness.dbs[0], issue.ID, req); err != nil {
			t.Fatalf("schedule %d first close: %v", schedule, err)
		}

		// Model a lost successful response: the caller retries the same logical
		// mutation. Until afc-111/113 add operation IDs and stored outcomes, the
		// retry is non-duplicating but returns a current-state conflict rather
		// than the original success response.
		_, err = CloseIssue(context.Background(), harness.dbs[1], issue.ID, req)
		requireCoordinationErrorCode(t, err, core.ErrConflict)

		got, lease, err := GetIssue(context.Background(), harness.dbs[1], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "done" || got.Version != claim.Version+1 || lease != nil {
			t.Fatalf("schedule %d retry state = issue=%+v lease=%+v", schedule, got, lease)
		}
		requireLeaseCount(t, harness.dbs[1], issue.ID, 0)
		notes, err := ListNotes(context.Background(), harness.dbs[1], issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 1 || notes[0].Body != req.Note {
			t.Fatalf("schedule %d close notes = %+v", schedule, notes)
		}
		events := requireCoordinationEvents(t, harness.dbs[1], issue.ID,
			[]string{"issue_created", "issue_claimed", "note_added", "issue_closed"}, claim.LeaseToken)
		requireEventGeneration(t, events, "issue_closed", 0, claim.LeaseGeneration)
	}
}

func newCoordinationRaceHarness(t *testing.T, connections int) *coordinationRaceHarness {
	t.Helper()
	if connections < 2 {
		t.Fatalf("connections = %d, want at least 2", connections)
	}

	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	harness := &coordinationRaceHarness{}
	t.Cleanup(func() {
		for _, db := range harness.dbs {
			_ = db.Close()
		}
	})

	primary, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	harness.dbs = append(harness.dbs, primary)
	if err := Migrate(context.Background(), primary, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), primary, "race", "Coordination Race Matrix", ""); err != nil {
		t.Fatal(err)
	}

	for connection := 1; connection < connections; connection++ {
		db, err := Open(dbPath)
		if err != nil {
			t.Fatalf("open connection %d: %v", connection, err)
		}
		harness.dbs = append(harness.dbs, db)
	}
	return harness
}

func (h *coordinationRaceHarness) newIssue(t *testing.T, title string) core.Issue {
	t.Helper()
	issue, err := CreateIssue(context.Background(), h.dbs[0], "race", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     title,
	})
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

func newCoordinationProofBarrier(t *testing.T, point coordinationProofPoint) (context.Context, <-chan struct{}, func()) {
	t.Helper()
	entered := make(chan struct{})
	releaseChannel := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseChannel) }) }
	t.Cleanup(release)

	hook := coordinationProofHook(func(got coordinationProofPoint) {
		if got != point {
			return
		}
		enteredOnce.Do(func() { close(entered) })
		<-releaseChannel
	})
	ctx := context.WithValue(context.Background(), coordinationProofHookContextKey{}, hook)
	return ctx, entered, release
}

func waitForCoordinationProofPoint(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for coordination proof point")
	}
}

func awaitCoordinationResult[T any](t *testing.T, results <-chan coordinationResult[T]) coordinationResult[T] {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for coordination operation")
		var zero coordinationResult[T]
		return zero
	}
}

func forceLeaseExpiry(t *testing.T, db *sql.DB, issueID string) {
	t.Helper()
	result, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`,
		"2000-01-01T00:00:00Z", issueID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expired lease rows = %d, want 1", rows)
	}
}

func requireCoordinationErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func requireLeaseCount(t *testing.T, db *sql.DB, issueID string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT count(*) FROM leases WHERE issue_id = ?`, issueID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("lease count = %d, want %d", got, want)
	}
}

func requireCoordinationEvents(t *testing.T, db *sql.DB, issueID string, wantTypes []string, secrets ...string) []core.Event {
	t.Helper()
	events, err := ListEvents(context.Background(), db, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for index, event := range events {
		if event.EventType != wantTypes[index] {
			t.Fatalf("event %d type = %q, want %q", index, event.EventType, wantTypes[index])
		}
		if index > 0 && event.Sequence <= events[index-1].Sequence {
			t.Fatalf("event sequence is not increasing: %d then %d", events[index-1].Sequence, event.Sequence)
		}
		for _, secret := range secrets {
			if secret != "" && strings.Contains(event.PayloadJSON, secret) {
				t.Fatalf("event %s leaked lease token", event.EventType)
			}
		}
	}
	return events
}

func requireEventGeneration(t *testing.T, events []core.Event, eventType string, occurrence int, want int64) {
	t.Helper()
	seen := 0
	for _, event := range events {
		if event.EventType != eventType {
			continue
		}
		if seen != occurrence {
			seen++
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["lease_generation"] != float64(want) {
			t.Fatalf("%s occurrence %d lease_generation = %v, want %d", eventType, occurrence, payload["lease_generation"], want)
		}
		return
	}
	t.Fatalf("event %s occurrence %d not found", eventType, occurrence)
}
