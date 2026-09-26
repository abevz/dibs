package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestRelocateRepoAPIRejectsDifferentGitAndReplays(t *testing.T) {
	server, db := newTestServer(t)
	root := t.TempDir()
	newParent := filepath.Join(root, "new")
	main := filepath.Join(newParent, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("-C", main, "init", "-b", "main")
	oldParent := filepath.Join(root, "old")
	if err := os.Symlink(newParent, oldParent); err != nil {
		t.Fatal(err)
	}
	oldMain := filepath.Join(oldParent, "main")
	ctx := context.Background()
	if _, err := sqlite.CreateProject(ctx, db, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := sqlite.CreateRepo(ctx, db, "demo", core.CreateRepoRequest{Project: "demo", LogicalName: "app", CanonicalGitDir: oldMain})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := sqlite.UpsertWorktree(ctx, db, repo.ID, core.CreateWorktreeRequest{Repo: repo.ID, AbsolutePath: oldMain, IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := sqlite.CreateIssue(ctx, db, "demo", core.CreateIssueRequest{Project: "demo", ScopeKind: "worktree", Repo: repo.ID, Worktree: wt.ID, Title: "retain issue", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, op string) (*http.Response, core.RelocateRepoResult) {
		t.Helper()
		body, _ := json.Marshal(core.RelocateRepoRequest{NewCanonicalGitDir: path, OperationID: op, Actor: "tester"})
		resp, err := http.Post(server.URL+"/v1/repos/"+repo.ID+"/relocate", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			return resp, core.RelocateRepoResult{}
		}
		return resp, decodeJSON[core.RelocateRepoResult](t, resp)
	}
	foreign := filepath.Join(root, "foreign")
	git("init", foreign)
	bad, _ := call(foreign, "relocate-api-bad")
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign path status = %d", bad.StatusCode)
	}
	bad.Body.Close()
	first, result := call(main, "relocate-api-good")
	if first.StatusCode != http.StatusOK || result.Repository.ID != repo.ID || len(result.Worktrees) != 1 || result.Worktrees[0].ID != wt.ID || result.Worktrees[0].AbsolutePath != main {
		t.Fatalf("relocate status/result = %d %+v", first.StatusCode, result)
	}
	issues, err := sqlite.ListIssues(ctx, db, core.IssueListParams{Worktree: wt.ID})
	if err != nil || len(issues) != 1 || issues[0].ID != issue.ID {
		t.Fatalf("issue lookup after relocation = %+v, %v", issues, err)
	}
	replay, replayResult := call(main, "relocate-api-good")
	if replay.StatusCode != http.StatusOK || replayResult.OperationID != result.OperationID {
		t.Fatalf("replay status/result = %d %+v", replay.StatusCode, replayResult)
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'repo_relocated'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events = %d, %v", events, err)
	}
}
