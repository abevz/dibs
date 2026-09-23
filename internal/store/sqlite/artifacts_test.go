package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateArtifactRoot(t *testing.T) {
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

	root, err := CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/specs",
		Kind:     "sdd",
		Primary:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.ID == "" {
		t.Error("expected non-empty artifact root ID")
	}
	if root.RepositoryID != repo.ID {
		t.Errorf("expected repository_id %q, got %q", repo.ID, root.RepositoryID)
	}
	if root.RootPath != "docs/specs" {
		t.Errorf("expected root_path 'docs/specs', got %q", root.RootPath)
	}
	if root.Kind != "sdd" {
		t.Errorf("expected kind 'sdd', got %q", root.Kind)
	}
	if !root.IsPrimary {
		t.Error("expected artifact root to be primary")
	}
}

func TestCreateArtifactRootDefaultsKind(t *testing.T) {
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

	root, err := CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/adr",
		// Kind defaults to "sdd"
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.Kind != "sdd" {
		t.Errorf("expected default kind 'sdd', got %q", root.Kind)
	}
}

func TestCreateDuplicateArtifactRoot(t *testing.T) {
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

	_, err = CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/specs",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/specs",
	})
	if err == nil {
		t.Fatal("expected error for duplicate artifact root path")
	}
	if !isSQLiteConstraintError(err) {
		t.Fatalf("expected a constraint error, got %T: %v", err, err)
	}
}

func TestGetArtifactRoot(t *testing.T) {
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

	created, err := CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/design",
		Kind:     "design",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetArtifactRoot(context.Background(), db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.RootPath != "docs/design" {
		t.Errorf("expected root_path 'docs/design', got %q", got.RootPath)
	}
}

func TestGetArtifactRootNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetArtifactRoot(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent artifact root")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestListArtifactRoots(t *testing.T) {
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

	// No roots yet.
	roots, err := ListArtifactRoots(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("expected 0 artifact roots, got %d", len(roots))
	}

	CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/specs",
	})
	CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs/adr",
	})

	roots, err = ListArtifactRoots(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Errorf("expected 2 artifact roots, got %d", len(roots))
	}
}

func TestCreateArtifact(t *testing.T) {
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

	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/specs/001-sdd.md",
		Title:        "SDD v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ID == "" {
		t.Error("expected non-empty artifact ID")
	}
	if artifact.RepositoryID != repo.ID {
		t.Errorf("expected repository_id %q, got %q", repo.ID, artifact.RepositoryID)
	}
	if artifact.RelativePath != "docs/specs/001-sdd.md" {
		t.Errorf("expected relative_path 'docs/specs/001-sdd.md', got %q", artifact.RelativePath)
	}
}

func TestCreateArtifactWithWorktreeAndRoot(t *testing.T) {
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

	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/feat",
	})
	if err != nil {
		t.Fatal(err)
	}

	root, err := CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		Repo:     repo.ID,
		RootPath: "docs",
	})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:           repo.ID,
		Kind:           "adr",
		RelativePath:   "docs/adr/001-decision.md",
		Title:          "Decision 1",
		Worktree:       wt.ID,
		ArtifactRootID: root.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.WorktreeID != wt.ID {
		t.Errorf("expected worktree_id %q, got %q", wt.ID, artifact.WorktreeID)
	}
	if artifact.ArtifactRootID != root.ID {
		t.Errorf("expected artifact_root_id %q, got %q", root.ID, artifact.ArtifactRootID)
	}
}

func TestGetArtifact(t *testing.T) {
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

	created, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/spec.md",
		Title:        "Spec",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetArtifact(context.Background(), db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.RelativePath != "docs/spec.md" {
		t.Errorf("expected relative_path 'docs/spec.md', got %q", got.RelativePath)
	}
}

func TestGetArtifactNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetArtifact(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestListArtifacts(t *testing.T) {
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

	// No artifacts yet.
	artifacts, err := ListArtifacts(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Errorf("expected 0 artifacts, got %d", len(artifacts))
	}

	CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/a.md",
	})
	CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "adr",
		RelativePath: "docs/b.md",
	})

	artifacts, err = ListArtifacts(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(artifacts))
	}
}

func TestListArtifactsByRepo(t *testing.T) {
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

	CreateArtifact(context.Background(), db, repo1.ID, core.CreateArtifactRequest{
		Repo:         repo1.ID,
		Kind:         "spec",
		RelativePath: "docs/r1.md",
	})
	CreateArtifact(context.Background(), db, repo2.ID, core.CreateArtifactRequest{
		Repo:         repo2.ID,
		Kind:         "spec",
		RelativePath: "docs/r2.md",
	})

	artifacts, err := ListArtifacts(context.Background(), db, repo1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Errorf("expected 1 artifact for repo1, got %d", len(artifacts))
	}
}

func TestCreateArtifactRejectsInvalidKind(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/tmp/repo",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		RelativePath: "bad.kind.md",
		Kind:         "invalid-kind",
	})
	if err == nil {
		t.Fatal("expected error for invalid artifact kind")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Errorf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}

func TestCreateArtifactRootRejectsInvalidKind(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/tmp/repo",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateArtifactRoot(context.Background(), db, repo.ID, core.CreateArtifactRootRequest{
		RootPath: "/bad-kind",
		Kind:     "invalid-kind",
	})
	if err == nil {
		t.Fatal("expected error for invalid artifact root kind")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Errorf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}

func TestCreateArtifactUpsert(t *testing.T) {
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

	art1, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/dup.md",
		Title:        "Original",
	})
	if err != nil {
		t.Fatal(err)
	}

	art2, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "spec",
		RelativePath: "docs/dup.md",
		Title:        "Updated",
	})
	if err != nil {
		t.Fatal(err)
	}

	if art1.ID != art2.ID {
		t.Fatalf("expected upsert to return same ID: %s != %s", art1.ID, art2.ID)
	}
	if art2.Title != "Updated" {
		t.Fatalf("expected updated title, got: %s", art2.Title)
	}
	if art2.Kind != "spec" {
		t.Fatalf("expected updated kind, got: %s", art2.Kind)
	}
}

func TestCreateArtifactInvalidKind(t *testing.T) {
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

	_, err = CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "invalid-kind",
		RelativePath: "docs/bad.md",
	})
	if err == nil {
		t.Fatal("expected error for invalid artifact kind")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Errorf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}
