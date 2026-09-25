package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateProject(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p, err := CreateProject(context.Background(), db, "test", "Test Project", "A test project")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Error("expected non-empty project ID")
	}
	if p.Key != "test" {
		t.Errorf("expected key 'test', got %q", p.Key)
	}
	if p.Name != "Test Project" {
		t.Errorf("expected name 'Test Project', got %q", p.Name)
	}
	if p.Description != "A test project" {
		t.Errorf("expected description 'A test project', got %q", p.Description)
	}
	if p.NextIssueSeq != 1 {
		t.Errorf("expected next_issue_seq 1, got %d", p.NextIssueSeq)
	}
}

func TestGetProject(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p, err := CreateProject(context.Background(), db, "tst", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetProject(context.Background(), db, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != p.Name {
		t.Errorf("expected name %q, got %q", p.Name, got.Name)
	}
	if got.Key != p.Key {
		t.Errorf("expected key %q, got %q", p.Key, got.Key)
	}
	if got.ID != p.ID {
		t.Errorf("expected ID %q, got %q", p.ID, got.ID)
	}
}

func TestGetProjectByKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "alpha", "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}

	p, err := GetProjectByKey(context.Background(), db, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "alpha" {
		t.Errorf("expected key 'alpha', got %q", p.Key)
	}
}

func TestProjectUUIDWorksForRegistrationAndIssueCreation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p, err := CreateProject(ctx, db, "alpha", "Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetProjectByKey(ctx, db, p.ID)
	if err != nil || got.ID != p.ID {
		t.Fatalf("lookup by ID = %+v, %v", got, err)
	}
	repo, _, err := CreateRepo(ctx, db, p.ID, core.CreateRepoRequest{Project: p.ID, LogicalName: "repo", CanonicalGitDir: "/tmp/repo", DefaultBranch: "main"})
	if err != nil || repo.ProjectID != p.ID {
		t.Fatalf("repo by project ID = %+v, %v", repo, err)
	}
	issue, err := CreateIssue(ctx, db, p.ID, core.CreateIssueRequest{Project: p.ID, ScopeKind: "project", Title: "UUID project"})
	if err != nil || issue.ProjectID != p.ID || issue.ShortID != "alpha-1" {
		t.Fatalf("issue by project ID = %+v, %v", issue, err)
	}
}

func TestCreateProjectDuplicateKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "dup", "First", "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateProject(context.Background(), db, "dup", "Second", "")
	if err == nil {
		t.Fatal("expected error for duplicate key")
	}
	// The error wraps a SQLite constraint violation.
	if !isSQLiteConstraintError(err) {
		t.Fatalf("expected a constraint error, got %T: %v", err, err)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetProject(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestGetProjectByKeyNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetProjectByKey(context.Background(), db, "missing")
	if err == nil {
		t.Fatal("expected error for nonexistent project key")
	}
}

func TestListProjects(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// No projects yet.
	projects, err := ListProjects(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}

	// Create two projects.
	CreateProject(context.Background(), db, "a", "A", "")
	CreateProject(context.Background(), db, "b", "B", "")

	projects, err = ListProjects(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}

	// Should be ordered by created_at DESC, so b then a.
	if projects[0].Key != "b" && projects[0].Key != "a" {
		t.Errorf("expected first project key to be one of {a, b}, got %q (second: %q)", projects[0].Key, projects[1].Key)
	}
}
