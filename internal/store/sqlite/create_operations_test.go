package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

func createOperationRequest(id, title string) core.CreateIssueRequest {
	return core.CreateIssueRequest{
		OperationID: id, Project: "race", ScopeKind: "project", Title: title,
		IssueType: "task", Priority: 3, Actor: "worker-a", Tags: []string{"area/test"},
	}
}

func assertCreateEffect(t *testing.T, db *sql.DB, issue core.Issue, wantSeq int64) {
	t.Helper()
	ctx := context.Background()
	for _, check := range []struct {
		name, query string
		args        []any
		want        int64
	}{
		{"issue", `SELECT count(*) FROM issues WHERE id = ?`, []any{issue.ID}, 1},
		{"event", `SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'issue_created'`, []any{issue.ID}, 1},
		{"ledger", `SELECT count(*) FROM operations WHERE operation_kind = 'create' AND outcome_json LIKE ?`, []any{"%" + issue.ID + "%"}, 1},
		{"sequence", `SELECT next_issue_seq FROM projects WHERE key = 'race'`, nil, wantSeq},
	} {
		var got int64
		if err := db.QueryRowContext(ctx, check.query, check.args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}
}

func TestCreateOperationReplayAfterLaterStateChange(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	req := createOperationRequest("create-replay-0001", "one task")
	req.Tags = []string{"risk/low", "area/test"}
	first, err := CreateIssue(ctx, h.dbs[0], "race", req)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a lost create response followed by an independent claim: replay
	// still returns the original create outcome, not current issue state.
	if _, err := ClaimIssue(ctx, h.dbs[0], first.ID, "worker-a", 900); err != nil {
		t.Fatal(err)
	}
	retry := req
	retry.IssueType, retry.Priority = "", 0
	retry.Tags = []string{"area/test", "risk/low"}
	replay, err := CreateIssue(ctx, h.dbs[1], "race", retry)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay = %+v, want original %+v", replay, first)
	}
	assertCreateEffect(t, h.dbs[1], first, 2)
	var count int
	if err := h.dbs[1].QueryRow(`SELECT count(*) FROM issues WHERE project_id = ?`, first.ProjectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("issues = %d, want one", count)
	}

	// A new operation is a new logical action even with the same title.
	req.OperationID = "create-replay-0002"
	second, err := CreateIssue(ctx, h.dbs[0], "race", req)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.ShortID == first.ShortID {
		t.Fatalf("new operation reused issue: %+v", second)
	}
	assertCreateEffect(t, h.dbs[0], second, 3)
}

func TestCreateOperationPayloadMismatch(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	base := createOperationRequest("create-mismatch-0001", "original")
	first, err := CreateIssue(ctx, h.dbs[0], "race", base)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name    string
		change  func(*core.CreateIssueRequest)
		project string
	}{
		{"project", func(r *core.CreateIssueRequest) {}, "other"},
		{"title", func(r *core.CreateIssueRequest) { r.Title = "changed" }, "race"},
		{"scope", func(r *core.CreateIssueRequest) { r.ScopeKind = "repository" }, "race"},
		{"type", func(r *core.CreateIssueRequest) { r.IssueType = "bug" }, "race"},
		{"repo", func(r *core.CreateIssueRequest) { r.Repo = "other" }, "race"},
		{"worktree", func(r *core.CreateIssueRequest) { r.Worktree = "other" }, "race"},
		{"external key", func(r *core.CreateIssueRequest) { r.ExternalKey = "external" }, "race"},
		{"description", func(r *core.CreateIssueRequest) { r.Description = "changed" }, "race"},
		{"acceptance", func(r *core.CreateIssueRequest) { r.AcceptanceCriteria = "changed" }, "race"},
		{"priority", func(r *core.CreateIssueRequest) { r.Priority = 2 }, "race"},
		{"actor", func(r *core.CreateIssueRequest) { r.Actor = "worker-b" }, "race"},
		{"tags", func(r *core.CreateIssueRequest) { r.Tags = []string{"area/other"} }, "race"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			req := base
			check.change(&req)
			_, err := CreateIssue(ctx, h.dbs[1], check.project, req)
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != core.ErrIdempotencyConflict {
				t.Fatalf("err = %v, want idempotency_conflict", err)
			}
		})
	}
	assertCreateEffect(t, h.dbs[0], first, 2)
}

func TestConcurrentCreateOperations(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	for schedule := 0; schedule < 30; schedule++ {
		for _, different := range []bool{false, true} {
			name := fmt.Sprintf("schedule-%d-conflict-%t", schedule, different)
			t.Run(name, func(t *testing.T) {
				id := fmt.Sprintf("create-race-%04d-%t", schedule, different)
				start := make(chan struct{})
				type result struct {
					issue core.Issue
					err   error
				}
				results := make(chan result, 2)
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func(index int) {
						defer wg.Done()
						<-start
						title := "same"
						if different {
							title = fmt.Sprintf("title-%d", index)
						}
						issue, err := CreateIssue(context.Background(), h.dbs[index], "race", createOperationRequest(id, title))
						results <- result{issue, err}
					}(i)
				}
				close(start)
				wg.Wait()
				close(results)
				var created []core.Issue
				conflicts := 0
				for result := range results {
					if result.err == nil {
						created = append(created, result.issue)
						continue
					}
					var apiErr core.APIError
					if errors.As(result.err, &apiErr) && apiErr.Code == core.ErrIdempotencyConflict {
						conflicts++
						continue
					}
					t.Fatalf("unexpected error: %v", result.err)
				}
				if different {
					if len(created) != 1 || conflicts != 1 {
						t.Fatalf("created=%d conflicts=%d", len(created), conflicts)
					}
				} else {
					if len(created) != 2 || created[0].ID != created[1].ID || created[0].ShortID != created[1].ShortID {
						t.Fatalf("duplicate retry outcomes: %+v", created)
					}
				}
				var ledger int
				if err := h.dbs[0].QueryRow(`SELECT count(*) FROM operations WHERE operation_id = ?`, id).Scan(&ledger); err != nil {
					t.Fatal(err)
				}
				if ledger != 1 {
					t.Fatalf("ledger rows = %d, want 1", ledger)
				}
			})
		}
	}
}

func TestCreateOperationReplayAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.db")
	ctx := context.Background()
	firstDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, firstDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(ctx, firstDB, "race", "Race", ""); err != nil {
		t.Fatal(err)
	}
	req := createOperationRequest("create-restart-0001", "restart")
	first, err := CreateIssue(ctx, firstDB, "race", req)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}
	secondDB, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	replay, err := CreateIssue(ctx, secondDB, "race", req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay after reopen differs: %+v vs %+v", replay, first)
	}
	assertCreateEffect(t, secondDB, first, 2)
}

func TestCreateOperationKindAndWeakIDFailClosed(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	ctx := context.Background()
	issue, err := CreateIssue(ctx, h.dbs[0], "race", createOperationRequest("create-kind-seed", "seed"))
	if err != nil {
		t.Fatal(err)
	}
	claimID := "claim-kind-0001"
	if _, err := ClaimIssueWithOperation(ctx, h.dbs[0], issue.ID, core.ClaimRequest{Holder: "worker-a", TTLSeconds: 900, OperationID: claimID}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ name, id, want string }{
		{"kind mismatch", claimID, core.ErrIdempotencyConflict},
		{"weak key", "short", core.ErrValidationFailed},
	} {
		t.Run(check.name, func(t *testing.T) {
			_, err := CreateIssue(ctx, h.dbs[1], "race", createOperationRequest(check.id, "another"))
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != check.want {
				t.Fatalf("error = %v, want %s", err, check.want)
			}
		})
	}
	assertCreateEffect(t, h.dbs[0], issue, 2)
}

func TestCreateOperationFailedTransactionHasNoLedgerOrSequenceEffect(t *testing.T) {
	h := newCoordinationRaceHarness(t, 2)
	req := createOperationRequest("create-rollback-0001", "bad tags")
	req.Tags = []string{"area/test", "area/test"} // duplicate tag violates the real schema
	if _, err := CreateIssue(context.Background(), h.dbs[0], "race", req); err == nil {
		t.Fatal("duplicate tag unexpectedly committed")
	}
	var issues, events, ledger, nextSeq int
	for _, check := range []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM issues`, &issues},
		{`SELECT count(*) FROM events WHERE event_type = 'issue_created'`, &events},
		{`SELECT count(*) FROM operations`, &ledger},
		{`SELECT next_issue_seq FROM projects WHERE key = 'race'`, &nextSeq},
	} {
		if err := h.dbs[1].QueryRow(check.query).Scan(check.dest); err != nil {
			t.Fatal(err)
		}
	}
	if issues != 0 || events != 0 || ledger != 0 || nextSeq != 1 {
		t.Fatalf("partial create issues=%d events=%d ledger=%d seq=%d", issues, events, ledger, nextSeq)
	}
}
