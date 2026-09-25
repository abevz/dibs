package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	req := core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "my-repo",
		CanonicalGitDir: "/repos/my-repo.git",
		DefaultBranch:   "main",
	}
	repo, remotes, err := CreateRepo(context.Background(), db, "test", req)
	if err != nil {
		t.Fatal(err)
	}
	if repo.ID == "" {
		t.Error("expected non-empty repo ID")
	}
	if repo.LogicalName != "my-repo" {
		t.Errorf("expected logical_name 'my-repo', got %q", repo.LogicalName)
	}
	if repo.CanonicalGitDir != "/repos/my-repo.git" {
		t.Errorf("expected canonical_git_dir '/repos/my-repo.git', got %q", repo.CanonicalGitDir)
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("expected default_branch 'main', got %q", repo.DefaultBranch)
	}
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes, got %d", len(remotes))
	}
}

func TestCreateRepoWithRemotes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	req := core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "with-remotes",
		CanonicalGitDir: "/repos/with-remotes.git",
		DefaultBranch:   "main",
		Remotes: []core.CreateRemoteRequest{
			{RemoteName: "origin", FetchURL: "https://example.com/repo.git", IsPrimary: true},
			{RemoteName: "upstream", FetchURL: "https://upstream.example.com/repo.git"},
		},
	}
	repo, remotes, err := CreateRepo(context.Background(), db, "test", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 {
		t.Fatalf("expected 2 remotes, got %d", len(remotes))
	}
	if remotes[0].RemoteName != "origin" {
		t.Errorf("expected first remote name 'origin', got %q", remotes[0].RemoteName)
	}
	if !remotes[0].IsPrimary {
		t.Error("expected first remote to be primary")
	}
	if remotes[0].RepositoryID != repo.ID {
		t.Errorf("expected remote repository_id %q, got %q", repo.ID, remotes[0].RepositoryID)
	}
}

func TestGetRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	created, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "get-repo",
		CanonicalGitDir: "/repos/get-repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetRepo(context.Background(), db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.LogicalName != "get-repo" {
		t.Errorf("expected logical_name 'get-repo', got %q", got.LogicalName)
	}
	if got.ProjectID != created.ProjectID {
		t.Errorf("expected project_id %q, got %q", created.ProjectID, got.ProjectID)
	}
}

func TestGetRepoByLogicalName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	created, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "utils",
		CanonicalGitDir: "/home/utils",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetRepo(context.Background(), db, "utils")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.LogicalName != "utils" {
		t.Errorf("expected logical_name 'utils', got %q", got.LogicalName)
	}
}

func TestGetRepoRejectsAmbiguousLogicalName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	for _, key := range []string{"p1", "p2"} {
		if _, err := CreateProject(context.Background(), db, key, key, ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := CreateRepo(context.Background(), db, key, core.CreateRepoRequest{
			Project:         key,
			LogicalName:     "shared",
			CanonicalGitDir: "/repos/" + key + "-shared.git",
		}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := GetRepo(context.Background(), db, "shared")
	if err == nil {
		t.Fatal("expected error for ambiguous logical name")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Fatalf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}

func TestGetRepoInProjectByLogicalName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p1, err := CreateProject(context.Background(), db, "p1", "Project 1", "")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := CreateProject(context.Background(), db, "p2", "Project 2", "")
	if err != nil {
		t.Fatal(err)
	}

	repo1, _, err := CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p1-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p2-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	got1, err := GetRepoInProject(context.Background(), db, p1.ID, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got1.ID != repo1.ID {
		t.Fatalf("expected repo %q, got %q", repo1.ID, got1.ID)
	}

	got2, err := GetRepoInProject(context.Background(), db, p2.ID, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != repo2.ID {
		t.Fatalf("expected repo %q, got %q", repo2.ID, got2.ID)
	}
	if _, err := GetRepoInProject(context.Background(), db, p1.ID, repo2.ID); err == nil {
		t.Fatal("repository UUID from another project must not bypass project scope")
	}
}

func TestGetRepoNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := GetRepo(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestListRepos(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	// No repos yet.
	repos, err := ListRepos(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}

	CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo-a",
		CanonicalGitDir: "/repos/repo-a.git",
	})
	CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo-b",
		CanonicalGitDir: "/repos/repo-b.git",
	})

	repos, err = ListRepos(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(repos))
	}
}

func TestListReposByProject(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p1, err := CreateProject(context.Background(), db, "p1", "Project 1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateProject(context.Background(), db, "p2", "Project 2", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "p1-repo",
		CanonicalGitDir: "/repos/p1-repo.git",
	})
	CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "p2-repo",
		CanonicalGitDir: "/repos/p2-repo.git",
	})

	repos, err := ListRepos(context.Background(), db, p1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Errorf("expected 1 repo for project p1, got %d", len(repos))
	}
}

func TestListReposByProjectKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "myproj", "My Project", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateRepo(context.Background(), db, "myproj", core.CreateRepoRequest{
		Project:         "myproj",
		LogicalName:     "alpha",
		CanonicalGitDir: "/repos/alpha.git",
	})

	repos, err := ListReposByProjectKey(context.Background(), db, "myproj")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].LogicalName != "alpha" {
		t.Errorf("expected logical_name 'alpha', got %q", repos[0].LogicalName)
	}
}

func TestCreateRepoDuplicateName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "dup",
		CanonicalGitDir: "/repos/dup.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "dup",
		CanonicalGitDir: "/repos/dup-again.git",
	})
	if err == nil {
		t.Fatal("expected error for duplicate repo name")
	}
	if !isSQLiteConstraintError(err) {
		t.Fatalf("expected a constraint error, got %T: %v", err, err)
	}
}

func TestCreateRepoNonexistentProject(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, _, err := CreateRepo(context.Background(), db, "nonexistent", core.CreateRepoRequest{
		Project:         "nonexistent",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}
