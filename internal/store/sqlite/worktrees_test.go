package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateWorktree(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "my-repo",
		CanonicalGitDir: "/repos/my-repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/main",
		Branch:       "main",
		HeadCommit:   "abc123",
		IsMain:       true,
	}
	wt, isNew, err := UpsertWorktree(context.Background(), db, repo.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("expected isNew to be true for a new worktree")
	}
	if wt.ID == "" {
		t.Error("expected non-empty worktree ID")
	}
	if wt.RepositoryID != repo.ID {
		t.Errorf("expected repository_id %q, got %q", repo.ID, wt.RepositoryID)
	}
	if wt.AbsolutePath != "/worktrees/main" {
		t.Errorf("expected absolute_path '/worktrees/main', got %q", wt.AbsolutePath)
	}
	if wt.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", wt.Branch)
	}
	if wt.HeadCommit != "abc123" {
		t.Errorf("expected head_commit 'abc123', got %q", wt.HeadCommit)
	}
	if !wt.IsMain {
		t.Error("expected worktree to be main")
	}
}

func TestUpsertWorktreeUpdatesExisting(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First insert.
	wt1, isNew, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/my-wt",
		Branch:       "feature/x",
		HeadCommit:   "aaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("expected isNew on first insert")
	}

	// Upsert again with same path — should update.
	wt2, isNew, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/my-wt",
		Branch:       "feature/y",
		HeadCommit:   "bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Error("expected isNew to be false on update")
	}
	if wt2.ID != wt1.ID {
		t.Errorf("expected same ID %q after update, got %q", wt1.ID, wt2.ID)
	}
	if wt2.Branch != "feature/y" {
		t.Errorf("expected branch 'feature/y', got %q", wt2.Branch)
	}
	if wt2.HeadCommit != "bbb" {
		t.Errorf("expected head_commit 'bbb', got %q", wt2.HeadCommit)
	}
}

func TestGetWorktree(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	created, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/get-me",
		Branch:       "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetWorktree(context.Background(), db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.AbsolutePath != "/worktrees/get-me" {
		t.Errorf("expected absolute_path '/worktrees/get-me', got %q", got.AbsolutePath)
	}
}

func TestGetWorktreeNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetWorktree(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent worktree")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestListWorktrees(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	// No worktrees yet.
	worktrees, err := ListWorktrees(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 0 {
		t.Errorf("expected 0 worktrees, got %d", len(worktrees))
	}

	UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/wt-a",
		Branch:       "main",
	})
	UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/wt-b",
		Branch:       "feature",
	})

	worktrees, err = ListWorktrees(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Errorf("expected 2 worktrees, got %d", len(worktrees))
	}
}

func TestListWorktreesByRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo1, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo-1",
		CanonicalGitDir: "/repos/repo-1.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo-2",
		CanonicalGitDir: "/repos/repo-2.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	UpsertWorktree(context.Background(), db, repo1.ID, core.CreateWorktreeRequest{
		Repo:         repo1.ID,
		AbsolutePath: "/worktrees/repo1-wt",
	})
	UpsertWorktree(context.Background(), db, repo2.ID, core.CreateWorktreeRequest{
		Repo:         repo2.ID,
		AbsolutePath: "/worktrees/repo2-wt",
	})

	worktrees, err := ListWorktrees(context.Background(), db, repo1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Errorf("expected 1 worktree for repo1, got %d", len(worktrees))
	}
}

func TestCreateWorktreeIsEphemeral(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/ephemeral",
		Branch:       "temp",
		IsEphemeral:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !wt.IsEphemeral {
		t.Error("expected worktree to be ephemeral")
	}
}

func TestDeleteWorktree(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/delete-me",
		Branch:       "feature/delete-me",
	})
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := DeleteWorktree(context.Background(), db, wt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != wt.ID {
		t.Fatalf("expected deleted worktree id %q, got %q", wt.ID, deleted.ID)
	}

	_, err = GetWorktree(context.Background(), db, wt.ID)
	if err == nil {
		t.Fatal("expected deleted worktree to be gone")
	}
}

func TestDeleteWorktreeRejectsMain(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/main",
		Branch:       "main",
		IsMain:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = DeleteWorktree(context.Background(), db, wt.ID)
	if err == nil {
		t.Fatal("expected main worktree delete to fail")
	}

	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrConflict {
		t.Fatalf("expected code %q, got %q", core.ErrConflict, apiErr.Code)
	}
}

func TestDeleteWorktreeRejectsIssueReferences(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/in-use",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		Project:   "test",
		Repo:      repo.ID,
		Worktree:  wt.ID,
		ScopeKind: "worktree",
		Title:     "Bound to worktree",
		Actor:     "tester",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = DeleteWorktree(context.Background(), db, wt.ID)
	if err == nil {
		t.Fatal("expected worktree delete with issue refs to fail")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrConflict {
		t.Fatalf("expected code %q, got %q", core.ErrConflict, apiErr.Code)
	}
}

func TestDeleteWorktreeRejectsArtifactReferences(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/artifact-bound",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Worktree:     wt.ID,
		Kind:         "doc",
		RelativePath: "docs/specs/005/tasks.md",
		Title:        "Spec tasks",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = DeleteWorktree(context.Background(), db, wt.ID)
	if err == nil {
		t.Fatal("expected worktree delete with artifact refs to fail")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrConflict {
		t.Fatalf("expected code %q, got %q", core.ErrConflict, apiErr.Code)
	}
}
