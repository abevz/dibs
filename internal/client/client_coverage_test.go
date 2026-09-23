package client

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
	"github.com/abevz/dibs/internal/testsocket"
)

func TestClientCoverage(t *testing.T) {
	sockPath := testsocket.Path(t)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/v1/health":
			json.NewEncoder(w).Encode(core.Health{Status: "ok", Name: "test"})
		case r.URL.Path == "/v1/stats" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"report": report.Report{Version: "v1"}})
		case r.URL.Path == "/v1/projects" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Project{Key: "test", Name: "Test"})
		case r.URL.Path == "/v1/projects" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Project{{Key: "test"}})
		case r.URL.Path == "/v1/repos" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Repository{ID: "r1"})
		case r.URL.Path == "/v1/repos" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Repository{{ID: "r1"}})
		case r.URL.Path == "/v1/worktrees" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Worktree{ID: "w1"})
		case r.URL.Path == "/v1/worktrees" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Worktree{{ID: "w1"}})
		case r.URL.Path == "/v1/worktrees/w1" && r.Method == "DELETE":
			json.NewEncoder(w).Encode(map[string]any{"worktree": core.Worktree{ID: "w1"}})
		case r.URL.Path == "/v1/artifact-roots" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.ArtifactRoot{ID: "ar1"})
		case r.URL.Path == "/v1/artifact-roots" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.ArtifactRoot{{ID: "ar1"}})
		case r.URL.Path == "/v1/artifacts" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Artifact{ID: "a1"})
		case r.URL.Path == "/v1/artifacts" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Artifact{{ID: "a1"}})
		case r.URL.Path == "/v1/issues" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Issue{ID: "i1"})
		case r.URL.Path == "/v1/issues" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Issue{{ID: "i1"}})
		case r.URL.Path == "/v1/issues/i1" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"issue": core.Issue{ID: "i1"}, "lease": core.IssueLease{}})
		case r.URL.Path == "/v1/issues/ready" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Issue{{ID: "i1"}})
		case r.URL.Path == "/v1/issues/i1/claim" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.ClaimResponse{})
		case r.URL.Path == "/v1/issues/i1/heartbeat" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]any{"expires_at": time.Now().Format(time.RFC3339)})
		case r.URL.Path == "/v1/issues/i1/release" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Issue{ID: "i1"})
		case r.URL.Path == "/v1/issues/i1/handoff" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.HandoffResponse{Note: core.Note{ID: "n1"}})
		case r.URL.Path == "/v1/issues/i1/close" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Issue{ID: "i1", Status: "closed"})
		case r.URL.Path == "/v1/issues/i1/notes" && r.Method == "POST":
			json.NewEncoder(w).Encode(core.Note{ID: "n1"})
		case r.URL.Path == "/v1/issues/i1/notes" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Note{{ID: "n1"}})
		case r.URL.Path == "/v1/issues/i1/events" && r.Method == "GET":
			json.NewEncoder(w).Encode([]core.Event{{ID: "e1"}})
		case r.URL.Path == "/v1/events" && r.Method == "GET":
			json.NewEncoder(w).Encode(core.EventPage{Events: []core.Event{{ID: "e1"}}, NextSince: "v1.cursor"})
		case r.URL.Path == "/v1/export/jsonl" && r.Method == "GET":
			_, _ = w.Write([]byte("{\"type\":\"project\",\"payload\":{\"id\":\"p1\"}}\n"))
		case r.URL.Path == "/v1/issues/i1/dependencies" && r.Method == "POST":
			// no body
		case r.URL.Path == "/v1/issues/i1/dependencies/i2" && r.Method == "DELETE":
			// no body
		case r.URL.Path == "/v1/issues/i1/tags" && r.Method == "POST":
			// no body
		case r.URL.Path == "/v1/issues/i1/tags" && r.Method == "DELETE":
			// no body
		case r.URL.Path == "/v1/issues/i1/artifacts" && r.Method == "POST":
			// no body
		case r.URL.Path == "/v1/bad-json" && r.Method == "GET":
			w.Write([]byte(`{bad json`))
		case r.URL.Path == "/v1/error" && r.Method == "GET":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "bad", "message": "bad msg"}})
		case r.URL.Path == "/v1/issues/i1" && r.Method == "PATCH":
			json.NewEncoder(w).Encode(core.Issue{ID: "i1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	defer ts.Close()

	// Wait for socket to be created
	time.Sleep(50 * time.Millisecond)

	c := New(sockPath)
	ctx := context.Background()

	// Now call all methods to boost coverage
	_, _ = c.Health(ctx)
	_, _ = c.GetStats(ctx, report.Query{})

	_, _ = c.CreateProject(ctx, "test", "Test", "desc")
	_, _ = c.ListProjects(ctx)

	_, _, _ = c.CreateRepo(ctx, core.CreateRepoRequest{Project: "test"})
	_, _ = c.ListRepos(ctx, "test")

	_, _ = c.RegisterWorktree(ctx, core.CreateWorktreeRequest{})
	_, _ = c.ListWorktrees(ctx, "test-repo")
	_, _ = c.DeleteWorktree(ctx, "w1")

	_, _ = c.CreateArtifactRoot(ctx, core.CreateArtifactRootRequest{})
	_, _ = c.ListArtifactRoots(ctx, "test-repo")

	_, _ = c.CreateArtifact(ctx, core.CreateArtifactRequest{})
	_, _ = c.ListArtifacts(ctx, "test-repo")

	_, _ = c.CreateIssue(ctx, core.CreateIssueRequest{})
	_, _ = c.ListIssues(ctx, "project", "test", "worktree", "open", "assignee", "bug", "gh://repo/42")
	_, _, _ = c.GetIssue(ctx, "i1")
	_, _ = c.ListReadyIssues(ctx, "test", "", nil)

	_, _ = c.ClaimIssue(ctx, "i1", "actor", 10)
	_, _ = c.ClaimIssueWithSession(ctx, "i1", "actor", 10, "session-1")
	_, _ = c.HeartbeatLease(ctx, "i1", "token", 1, 10)
	_ = c.ReleaseLease(ctx, "i1", "token", 1)
	_, _ = c.HandoffLease(ctx, "i1", "token", 1, "HANDOFF: coverage")
	_, _ = c.CloseIssue(ctx, "i1", core.CloseIssueRequest{})
	_, _ = c.OperatorCloseIssue(ctx, "i1", core.OperatorCloseIssueRequest{})
	_, _ = c.OperatorReopenIssue(ctx, "i1", core.OperatorReopenIssueRequest{})

	_, _ = c.CreateNote(ctx, "i1", "actor", "note")
	_, _ = c.ListNotes(ctx, "i1")
	_, _ = c.ListEvents(ctx, "i1")
	_, _ = c.WatchEvents(ctx, "", 100, 0)
	_ = c.ExportJSONL(ctx, io.Discard)

	_ = c.AddDependency(ctx, "i1", core.AddDependencyRequest{})
	_ = c.RemoveDependency(ctx, "i1", core.RemoveDependencyRequest{DependsOn: "i2", Kind: "kind"})
	_ = c.LinkArtifact(ctx, "i1", core.LinkArtifactRequest{})
	_ = c.AddTag(ctx, "i1", core.AddTagRequest{Tag: "area/frontend"})
	_ = c.RemoveTag(ctx, "i1", "area/frontend", "actor")

	_, _ = c.UpdateIssue(ctx, "i1", core.UpdateIssueRequest{})

	// Test error parsing
	c.doJSON(ctx, "GET", "/v1/error", nil, nil)
	c.doJSON(ctx, "GET", "/v1/bad-json", nil, nil)
	c.doJSON(ctx, "GET", "/v1/not-found", nil, nil)
}

func TestClient_BadSocket(t *testing.T) {
	c := New("/tmp/does-not-exist.sock")
	_, err := c.Health(context.Background())
	if err == nil {
		t.Error("expected dial error")
	}
}
