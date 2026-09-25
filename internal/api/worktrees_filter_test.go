package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestListWorktreesByRepositoryName(t *testing.T) {
	server, db := newTestServer(t)
	ctx := context.Background()
	if _, err := sqlite.CreateProject(ctx, db, "alpha", "Alpha", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := sqlite.CreateRepo(ctx, db, "alpha", core.CreateRepoRequest{Project: "alpha", LogicalName: "named-repo", CanonicalGitDir: "/tmp/named-repo", DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := sqlite.UpsertWorktree(ctx, db, repo.ID, core.CreateWorktreeRequest{AbsolutePath: "/tmp/named-repo/wt"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(server.URL + "/v1/worktrees?repo=named-repo")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	result := decodeJSON[struct {
		Worktrees []core.Worktree `json:"worktrees"`
	}](t, resp)
	if len(result.Worktrees) != 1 || result.Worktrees[0].ID != wt.ID {
		t.Fatalf("worktrees = %+v", result.Worktrees)
	}
}
