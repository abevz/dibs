package sqlite

import (
	"context"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestRelocateRepoPreservesIDsAndReplaysOneAuditEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	if _, err := CreateProject(ctx, db, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(ctx, db, "demo", core.CreateRepoRequest{Project: "demo", LogicalName: "app", CanonicalGitDir: "/old/main"})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := UpsertWorktree(ctx, db, repo.ID, core.CreateWorktreeRequest{Repo: repo.ID, AbsolutePath: "/old/main", IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	changes := []core.WorktreePathChange{{ID: wt.ID, OldPath: "/old/main", NewPath: "/new/main"}}
	req := core.RelocateRepoRequest{NewCanonicalGitDir: "/new/main", OperationID: "relocate-test-001", Actor: "tester"}
	first, err := RelocateRepo(ctx, db, repo.ID, "/old/main", req, changes)
	if err != nil {
		t.Fatal(err)
	}
	if first.Repository.ID != repo.ID || len(first.Worktrees) != 1 || first.Worktrees[0].ID != wt.ID || first.Worktrees[0].AbsolutePath != "/new/main" {
		t.Fatalf("IDs or path changed incorrectly: %+v", first)
	}
	replay, err := RelocateRepo(ctx, db, repo.ID, "/old/main", req, changes)
	if err != nil || replay.Repository.CanonicalGitDir != first.Repository.CanonicalGitDir || replay.Worktrees[0].ID != wt.ID {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'repo_relocated'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("audit events = %d, %v", events, err)
	}
	otherReq := req
	otherReq.NewCanonicalGitDir = "/elsewhere/main"
	if _, err := RelocateRepo(ctx, db, repo.ID, "/new/main", otherReq, nil); err == nil {
		t.Fatal("operation ID reuse with changed destination succeeded")
	}
	got, err := GetRepo(ctx, db, repo.ID)
	if err != nil || got.CanonicalGitDir != "/new/main" {
		t.Fatalf("stored repo after conflict = %+v, %v", got, err)
	}
}

func TestRelocateRepoRejectsTargetPathOwnedByAnotherWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	if _, err := CreateProject(ctx, db, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(ctx, db, "demo", core.CreateRepoRequest{Project: "demo", LogicalName: "app", CanonicalGitDir: "/old/main"})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := UpsertWorktree(ctx, db, repo.ID, core.CreateWorktreeRequest{Repo: repo.ID, AbsolutePath: "/old/main", IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := CreateRepo(ctx, db, "demo", core.CreateRepoRequest{Project: "demo", LogicalName: "other", CanonicalGitDir: "/new/main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpsertWorktree(ctx, db, other.ID, core.CreateWorktreeRequest{Repo: other.ID, AbsolutePath: "/new/main", IsMain: true}); err != nil {
		t.Fatal(err)
	}
	_, err = RelocateRepo(ctx, db, repo.ID, "/old/main", core.RelocateRepoRequest{NewCanonicalGitDir: "/new/main", OperationID: "relocate-test-002", Actor: "tester"}, []core.WorktreePathChange{{ID: wt.ID, OldPath: "/old/main", NewPath: "/new/main"}})
	if err == nil {
		t.Fatal("colliding target path accepted")
	}
	got, err := GetRepo(ctx, db, repo.ID)
	if err != nil || got.CanonicalGitDir != "/old/main" {
		t.Fatalf("repository changed on collision: %+v, %v", got, err)
	}
}
