package api

import (
	"context"
	"github.com/abevz/af-coordinator/internal/core"
	"github.com/abevz/af-coordinator/internal/store/sqlite"
	"github.com/abevz/af-coordinator/migrations"

	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// registerRoutes duplicates the route setup from daemon.go for testing.
func registerRoutes(mux *http.ServeMux, db *sql.DB, logger *slog.Logger) {
	st := sqlite.NewStore(db)

	// Health endpoints
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/v1/health", healthHandler)
	mux.HandleFunc("GET /v1/export/jsonl", handleExportJSONL(st, logger))
	mux.HandleFunc("GET /v1/stats", handleStats(st, logger))

	// Projects
	mux.HandleFunc("POST /v1/projects", handleCreateProject(st, logger))
	mux.HandleFunc("GET /v1/projects", handleListProjects(st, logger))

	// Repos
	mux.HandleFunc("POST /v1/repos", handleCreateRepo(st, logger))
	mux.HandleFunc("GET /v1/repos", handleListRepos(st, logger))

	// Worktrees
	mux.HandleFunc("POST /v1/worktrees", handleRegisterWorktree(st, logger))
	mux.HandleFunc("GET /v1/worktrees", handleListWorktrees(st, logger))
	mux.HandleFunc("DELETE /v1/worktrees/{worktree_id}", handleDeleteWorktree(st, logger))
	mux.HandleFunc("GET /v1/events", handleWatchEvents(st, logger))

	// Artifact roots
	mux.HandleFunc("POST /v1/artifact-roots", handleCreateArtifactRoot(st, logger))
	mux.HandleFunc("GET /v1/artifact-roots", handleListArtifactRoots(st, logger))

	// Artifacts
	mux.HandleFunc("POST /v1/artifacts", handleCreateArtifact(st, logger))
	mux.HandleFunc("GET /v1/artifacts", handleListArtifacts(st, logger))

	// Issues
	mux.HandleFunc("POST /v1/issues", handleCreateIssue(st, logger))
	mux.HandleFunc("GET /v1/issues/ready", handleListReadyIssues(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}", handleGetIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/claim", handleClaimIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/heartbeat", handleHeartbeatLease(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/release", handleReleaseLease(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/handoff", handleHandoffLease(st, logger))
	mux.HandleFunc("PATCH /v1/issues/{issue_id}", handleUpdateIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/close", handleCloseIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/operator-close", handleOperatorCloseIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/operator-reopen", handleOperatorReopenIssue(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/dependencies", handleAddDependency(st, logger))
	mux.HandleFunc("DELETE /v1/issues/{issue_id}/dependencies/{depends_on}", handleRemoveDependency(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/links", handleLinkArtifact(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/tags", handleAddTag(st, logger))
	mux.HandleFunc("DELETE /v1/issues/{issue_id}/tags", handleRemoveTag(st, logger))
	mux.HandleFunc("POST /v1/issues/{issue_id}/notes", handleCreateNote(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}/notes", handleListNotes(st, logger))
	mux.HandleFunc("GET /v1/issues/{issue_id}/events", handleListEvents(st, logger))
	mux.HandleFunc("GET /v1/issues", handleListIssues(st, logger))
}

// newTestServer creates an in-memory SQLite DB, initializes the schema,
// creates an HTTP test server with all routes, and returns the server + db.
func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Single connection to keep the in-memory database shared.
	db.SetMaxOpenConns(1)
	// Busy timeout so concurrent readers on the single connection
	// block and retry instead of returning SQLITE_BUSY.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}

	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mux := http.NewServeMux()
	registerRoutes(mux, db, logger)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server, db
}

// decodeJSON decodes a JSON response body into the target type T.
func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Projects
// ---------------------------------------------------------------------------

func TestCreateProject(t *testing.T) {
	server, _ := newTestServer(t)

	body := `{"name":"Test Project","key":"test"}`
	resp, err := http.Post(server.URL+"/v1/projects", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		Project struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"project"`
	}
	result = decodeJSON[struct {
		Project struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"project"`
	}](t, resp)

	if result.Project.Key != "test" {
		t.Errorf("expected key 'test', got %q", result.Project.Key)
	}
	if result.Project.Name != "Test Project" {
		t.Errorf("expected name 'Test Project', got %q", result.Project.Name)
	}
	if result.Project.ID == "" {
		t.Error("expected non-empty project ID")
	}
}

func TestCreateProjectMissingName(t *testing.T) {
	server, _ := newTestServer(t)

	body := `{"key":"test"}`
	resp, err := http.Post(server.URL+"/v1/projects", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	errResp = decodeJSON[struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, resp)

	if errResp.Error.Code != "validation_failed" {
		t.Errorf("expected code validation_failed, got %q", errResp.Error.Code)
	}
}

func TestListProjects(t *testing.T) {
	server, db := newTestServer(t)

	// Insert two projects directly
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range []struct {
		id, key, name string
	}{
		{"p1", "alpha", "Alpha"},
		{"p2", "beta", "Beta"},
	} {
		_, err := db.Exec(
			`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
			 VALUES (?, ?, ?, '', 1, ?, ?)`,
			p.id, p.key, p.name, now, now,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Get(server.URL + "/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Projects []struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	result = decodeJSON[struct {
		Projects []struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"projects"`
	}](t, resp)

	if len(result.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(result.Projects))
	}
}

// ---------------------------------------------------------------------------
// Repos
// ---------------------------------------------------------------------------

func TestCreateRepo(t *testing.T) {
	server, db := newTestServer(t)

	// Create a project first
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test Project', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"project":"test","logical_name":"main","canonical_git_dir":"/tmp/repo","default_branch":"main"}`
	resp, err := http.Post(server.URL+"/v1/repos", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		Repository struct {
			ID              string `json:"id"`
			LogicalName     string `json:"logical_name"`
			CanonicalGitDir string `json:"canonical_git_dir"`
		} `json:"repository"`
		Remotes []any `json:"remotes"`
	}
	result = decodeJSON[struct {
		Repository struct {
			ID              string `json:"id"`
			LogicalName     string `json:"logical_name"`
			CanonicalGitDir string `json:"canonical_git_dir"`
		} `json:"repository"`
		Remotes []any `json:"remotes"`
	}](t, resp)

	if result.Repository.LogicalName != "main" {
		t.Errorf("expected logical_name 'main', got %q", result.Repository.LogicalName)
	}
}

func TestExportJSONL(t *testing.T) {
	server, db := newTestServer(t)

	now := "2026-07-08T16:00:00Z"
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"proj-1", "afc", "AF Coordinator", "", 2, now, now},
		},
		{
			query: `INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"repo-1", "proj-1", "main", "/tmp/af-coordinator.git", "main", "", "", now, now},
		},
		{
			query: `INSERT INTO issues (id, short_id, project_id, repository_id, worktree_id, scope_kind, issue_type, title, external_key, description, acceptance_criteria, status, priority, assignee, version, claimed_at, closed_at, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"issue-1", "afc-1", "proj-1", "repo-1", nil, "repository", "feature", "Export data", "", "", "", "open", 2, "", 1, nil, nil, now, now},
		},
		{
			query: `INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
			        VALUES (?, ?, ?, ?, ?, ?)`,
			args: []any{"event-1", "issue-1", "codex", "issue_created", `{"title":"Export data"}`, now},
		},
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Get(server.URL + "/v1/export/jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 export lines, got %d\n%s", len(lines), string(body))
	}

	var first struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first export line: %v", err)
	}
	if first.Type != "project" {
		t.Fatalf("first export record type = %q, want project", first.Type)
	}
}

func TestListRepos(t *testing.T) {
	server, db := newTestServer(t)

	// Create a project and a repo
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/repos")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Repositories []struct {
			ID string `json:"id"`
		} `json:"repositories"`
	}
	result = decodeJSON[struct {
		Repositories []struct {
			ID string `json:"id"`
		} `json:"repositories"`
	}](t, resp)

	if len(result.Repositories) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(result.Repositories))
	}
}

// ---------------------------------------------------------------------------
// Issues
// ---------------------------------------------------------------------------

func TestCreateIssue(t *testing.T) {
	server, db := newTestServer(t)

	// Create a project
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"project":"test","scope_kind":"project","title":"My issue","external_key":"gh://abevz/af-coordinator/issues/26","actor":"test"}`
	resp, err := http.Post(server.URL+"/v1/issues", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		Issue struct {
			ID          string `json:"id"`
			ShortID     string `json:"short_id"`
			Title       string `json:"title"`
			ExternalKey string `json:"external_key"`
			Status      string `json:"status"`
			ScopeKind   string `json:"scope_kind"`
		} `json:"issue"`
	}
	result = decodeJSON[struct {
		Issue struct {
			ID          string `json:"id"`
			ShortID     string `json:"short_id"`
			Title       string `json:"title"`
			ExternalKey string `json:"external_key"`
			Status      string `json:"status"`
			ScopeKind   string `json:"scope_kind"`
		} `json:"issue"`
	}](t, resp)

	if result.Issue.Title != "My issue" {
		t.Errorf("expected title 'My issue', got %q", result.Issue.Title)
	}
	if result.Issue.Status != "open" {
		t.Errorf("expected status 'open', got %q", result.Issue.Status)
	}
	if result.Issue.ExternalKey != "gh://abevz/af-coordinator/issues/26" {
		t.Errorf("expected external_key to round-trip, got %q", result.Issue.ExternalKey)
	}
	if result.Issue.ScopeKind != "project" {
		t.Errorf("expected scope_kind 'project', got %q", result.Issue.ScopeKind)
	}
	if !strings.HasPrefix(result.Issue.ShortID, "test-") {
		t.Errorf("expected short_id to start with 'test-', got %q", result.Issue.ShortID)
	}
}

func TestCreateIssueMissingTitle(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"project":"test","scope_kind":"project"}`
	resp, err := http.Post(server.URL+"/v1/issues", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}
}

func TestCreateIssueProjectNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	body := `{"project":"nonexistent","scope_kind":"project","title":"My issue","actor":"test"}`
	resp, err := http.Post(server.URL+"/v1/issues", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", resp.StatusCode)
	}
}

func TestGetIssue(t *testing.T) {
	server, db := newTestServer(t)

	// Create project + issue directly
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Test issue', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/issues/" + issueID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issue struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"issue"`
	}
	result = decodeJSON[struct {
		Issue struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"issue"`
	}](t, resp)

	if result.Issue.ID != issueID {
		t.Errorf("expected issue ID %q, got %q", issueID, result.Issue.ID)
	}
	if result.Issue.Title != "Test issue" {
		t.Errorf("expected title 'Test issue', got %q", result.Issue.Title)
	}
}

func TestGetIssueIncludesExplicitDependencyIdentifiers(t *testing.T) {
	server, db := newTestServer(t)

	if _, err := sqlite.CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	source, err := sqlite.CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Source",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := sqlite.CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.AddDependency(context.Background(), db, source.ID, core.AddDependencyRequest{
		DependsOn: target.ID,
		Kind:      "blocks",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/issues/" + source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issue struct {
			Dependencies []struct {
				IssueID          string `json:"issue_id"`
				IssueShortID     string `json:"issue_short_id"`
				DependsOnID      string `json:"depends_on_id"`
				DependsOnShortID string `json:"depends_on_short_id"`
				Kind             string `json:"kind"`
			} `json:"dependencies"`
		} `json:"issue"`
	}
	result = decodeJSON[struct {
		Issue struct {
			Dependencies []struct {
				IssueID          string `json:"issue_id"`
				IssueShortID     string `json:"issue_short_id"`
				DependsOnID      string `json:"depends_on_id"`
				DependsOnShortID string `json:"depends_on_short_id"`
				Kind             string `json:"kind"`
			} `json:"dependencies"`
		} `json:"issue"`
	}](t, resp)

	if len(result.Issue.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Issue.Dependencies))
	}
	dep := result.Issue.Dependencies[0]
	if dep.IssueID != source.ID || dep.IssueShortID != source.ShortID {
		t.Fatalf("unexpected source dependency identity: %+v", dep)
	}
	if dep.DependsOnID != target.ID || dep.DependsOnShortID != target.ShortID {
		t.Fatalf("unexpected dependency target identity: %+v", dep)
	}
}

func TestAddDependencyCrossProjectReturnsValidationFailed(t *testing.T) {
	server, db := newTestServer(t)

	if _, err := sqlite.CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.CreateProject(context.Background(), db, "p2", "Project 2", ""); err != nil {
		t.Fatal(err)
	}
	source, err := sqlite.CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{ScopeKind: "project", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := sqlite.CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"depends_on":%q,"kind":"blocks","actor":"tester"}`, target.ID)
	resp, err := http.Post(server.URL+"/v1/issues/"+source.ID+"/dependencies", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-project add dependency status = %d, want 400", resp.StatusCode)
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != core.ErrValidationFailed {
		t.Fatalf("cross-project add dependency code = %q, want %q", result.Error.Code, core.ErrValidationFailed)
	}
}

func TestGetIssueLeaseTokenLeak(t *testing.T) {
	server, db := newTestServer(t)

	// Create project + issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	issueID := "issue-1"
	_, _ = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Test issue', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)

	// Claim it
	claimBody := `{"holder":"agent-1","ttl_seconds":3600}`
	claimReq, _ := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRespHTTP, err := http.DefaultClient.Do(claimReq)
	if err != nil || claimRespHTTP.StatusCode != http.StatusOK {
		t.Fatalf("failed to claim issue")
	}

	// Get it
	resp, err := http.Get(server.URL + "/v1/issues/" + issueID)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	leaseObj, ok := result["lease"].(map[string]any)
	if !ok {
		t.Fatalf("expected lease object in response, got %v", result["lease"])
	}

	if _, hasToken := leaseObj["lease_token"]; hasToken {
		t.Errorf("SECURITY LEAK: lease_token must not be returned in GET issue response")
	}
	if holder, ok := leaseObj["holder"].(string); !ok || holder != "agent-1" {
		t.Errorf("expected holder agent-1, got %v", leaseObj["holder"])
	}
	if attemptID, ok := leaseObj["attempt_id"].(string); !ok || attemptID == "" {
		t.Errorf("expected non-empty attempt_id, got %v", leaseObj["attempt_id"])
	}
	if generation, ok := leaseObj["lease_generation"].(float64); !ok || generation != 1 {
		t.Errorf("expected lease_generation 1, got %v", leaseObj["lease_generation"])
	}
}

func TestGetIssueNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/v1/issues/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", resp.StatusCode)
	}
}

func TestClaimIssue(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Claimable', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"holder":"agent-1","ttl_seconds":3600,"session_id":"session-claim-1"}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var claimResp struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
		AttemptID       string `json:"attempt_id"`
	}
	claimResp = decodeJSON[struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
		AttemptID       string `json:"attempt_id"`
	}](t, resp)

	if claimResp.LeaseToken == "" {
		t.Error("expected non-empty lease_token")
	}
	if claimResp.ExpiresAt == "" {
		t.Error("expected non-empty expires_at")
	}
	if claimResp.AttemptID == "" {
		t.Error("expected non-empty attempt_id")
	}
	if claimResp.LeaseGeneration != 1 {
		t.Errorf("expected lease_generation 1, got %d", claimResp.LeaseGeneration)
	}
	var sessionID string
	if err := db.QueryRow(`SELECT session_id FROM leases WHERE issue_id = ?`, issueID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if sessionID != "session-claim-1" {
		t.Fatalf("session_id = %q", sessionID)
	}
}

func TestClaimIssueRejectsBlockedTaskAndSameHolderTokenRecovery(t *testing.T) {
	t.Run("unfinished blocker", func(t *testing.T) {
		server, db := newTestServer(t)
		if _, err := sqlite.CreateProject(context.Background(), db, "claim-ready", "Claim Ready", ""); err != nil {
			t.Fatal(err)
		}
		blocker, err := sqlite.CreateIssue(context.Background(), db, "claim-ready", core.CreateIssueRequest{ScopeKind: "project", Title: "Blocker"})
		if err != nil {
			t.Fatal(err)
		}
		target, err := sqlite.CreateIssue(context.Background(), db, "claim-ready", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlite.AddDependency(context.Background(), db, target.ID, core.AddDependencyRequest{
			DependsOn: blocker.ID, Kind: "blocks", Actor: "planner",
		}); err != nil {
			t.Fatal(err)
		}

		resp, err := http.Post(server.URL+"/v1/issues/"+target.ID+"/claim", "application/json",
			strings.NewReader(`{"holder":"worker","ttl_seconds":3600}`))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409", resp.StatusCode)
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if envelope.Error.Code != string(core.ErrIssueNotReady) {
			t.Fatalf("error code = %q, want issue_not_ready", envelope.Error.Code)
		}
	})

	t.Run("same holder", func(t *testing.T) {
		server, db := newTestServer(t)
		if _, err := sqlite.CreateProject(context.Background(), db, "claim-auth", "Claim Auth", ""); err != nil {
			t.Fatal(err)
		}
		issue, err := sqlite.CreateIssue(context.Background(), db, "claim-auth", core.CreateIssueRequest{ScopeKind: "project", Title: "Secret lease"})
		if err != nil {
			t.Fatal(err)
		}
		first, err := sqlite.ClaimIssue(context.Background(), db, issue.ID, "worker", 3600)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.Post(server.URL+"/v1/issues/"+issue.ID+"/claim", "application/json",
			strings.NewReader(`{"holder":"worker","ttl_seconds":7200}`))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), `"code":"lease_held"`) {
			t.Fatalf("status/body = %d %s, want lease_held", resp.StatusCode, body)
		}
		if strings.Contains(string(body), first.LeaseToken) {
			t.Fatalf("same-holder conflict leaked lease token: %s", body)
		}
	})
}

func TestHandoffLeaseRecordsNoteAndReleasesAtomically(t *testing.T) {
	server, db := newTestServer(t)

	if _, err := sqlite.CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := sqlite.CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Handoff through API",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := sqlite.ClaimIssue(context.Background(), db, issue.ID, "api-agent", 3600)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(core.HandoffRequest{
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Note:            "HANDOFF: validate the API response",
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+issue.ID+"/handoff", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	handoff := decodeJSON[core.HandoffResponse](t, resp)
	if handoff.Note.Author != "api-agent" || handoff.Note.Body != "HANDOFF: validate the API response" {
		t.Fatalf("handoff response = %+v", handoff)
	}

	updated, lease, err := sqlite.GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "open" || lease != nil {
		t.Fatalf("unexpected post-handoff state: status=%q lease=%+v", updated.Status, lease)
	}

	invalid, err := sqlite.CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Invalid handoff"})
	if err != nil {
		t.Fatal(err)
	}
	invalidClaim, err := sqlite.ClaimIssue(context.Background(), db, invalid.ID, "api-agent", 3600)
	if err != nil {
		t.Fatal(err)
	}
	invalidBody, err := json.Marshal(core.HandoffRequest{LeaseToken: invalidClaim.LeaseToken, Note: "missing prefix"})
	if err != nil {
		t.Fatal(err)
	}
	invalidReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+invalid.ID+"/handoff", strings.NewReader(string(invalidBody)))
	if err != nil {
		t.Fatal(err)
	}
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidResp, err := http.DefaultClient.Do(invalidReq)
	if err != nil {
		t.Fatal(err)
	}
	defer invalidResp.Body.Close()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid handoff status = %d, want %d", invalidResp.StatusCode, http.StatusBadRequest)
	}
	_, invalidLease, err := sqlite.GetIssue(context.Background(), db, invalid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if invalidLease == nil {
		t.Fatal("invalid API request released the lease")
	}
}

func TestClaimIssueNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	body := `{"holder":"agent-1","ttl_seconds":3600}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/nonexistent/claim", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", resp.StatusCode)
	}
}

func TestListIssues(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issues
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"Issue A", "Issue B"} {
		shortID := "test-" + string(rune('1'+i))
		_, err := db.Exec(
			`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
			 VALUES (?, ?, 'proj-1', 'project', ?, '', 'open', 3, '', 1, ?, ?)`,
			"issue-"+string(rune('a'+i)), shortID, title, now, now,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Get(server.URL + "/v1/issues?project=test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issues []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"issues"`
	}
	result = decodeJSON[struct {
		Issues []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"issues"`
	}](t, resp)

	if len(result.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(result.Issues))
	}
}

func TestListIssuesMultiValueFilters(t *testing.T) {
	server, db := newTestServer(t)
	now := "2026-07-13T10:00:00Z"
	for _, project := range []struct{ id, key string }{
		{id: "project-afc", key: "afc"},
		{id: "project-aion", key: "aion"},
	} {
		if _, err := db.Exec(
			`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
			 VALUES (?, ?, ?, '', 1, ?, ?)`,
			project.id, project.key, project.key, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, issue := range []struct {
		id, shortID, projectID, issueType, status string
	}{
		{id: "issue-afc-epic", shortID: "afc-1", projectID: "project-afc", issueType: "epic", status: "open"},
		{id: "issue-aion-chore", shortID: "aion-1", projectID: "project-aion", issueType: "chore", status: "in_progress"},
		{id: "issue-aion-bug", shortID: "aion-2", projectID: "project-aion", issueType: "bug", status: "open"},
	} {
		if _, err := db.Exec(
			`INSERT INTO issues (id, short_id, project_id, scope_kind, issue_type, title, description, status, priority, assignee, version, created_at, updated_at)
			 VALUES (?, ?, ?, 'project', ?, ?, '', ?, 3, '', 1, ?, ?)`,
			issue.id, issue.shortID, issue.projectID, issue.issueType, issue.id, issue.status, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Get(server.URL + "/v1/issues?project=afc,%20aion&type=epic&type=chore&status=open,in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	result := decodeJSON[struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}](t, resp)
	if len(result.Issues) != 2 {
		t.Fatalf("expected 2 matching issues, got %d", len(result.Issues))
	}
	if got, want := []string{result.Issues[0].ID, result.Issues[1].ID}, []string{"issue-afc-epic", "issue-aion-chore"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("issue IDs = %q, want %q", got, want)
	}

	for _, rawQuery := range []string{"project=afc,", "type=epic,unknown"} {
		resp, err := http.Get(server.URL + "/v1/issues?" + rawQuery)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			resp.Body.Close()
			t.Fatalf("query %q: expected 400, got %d", rawQuery, resp.StatusCode)
		}
		result := decodeJSON[core.APIErrorResponse](t, resp)
		if result.Error.Code != core.ErrValidationFailed {
			t.Fatalf("query %q: error code = %q, want %q", rawQuery, result.Error.Code, core.ErrValidationFailed)
		}
	}
}

func TestStats(t *testing.T) {
	server, db := newTestServer(t)
	now := "2026-07-13T10:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('project-1', 'test', 'Test', '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, issue_type, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', 'project-1', 'project', 'task', 'Issue', '', 'open', 3, '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/stats?project=test&since=2026-07-13T00%3A00%3A00Z&until=2026-07-14T00%3A00%3A00Z")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	result := decodeJSON[struct {
		Report struct {
			Version   string `json:"version"`
			Inventory struct {
				Total int `json:"total"`
			} `json:"inventory"`
		} `json:"report"`
	}](t, resp)
	if result.Report.Version != "v1" || result.Report.Inventory.Total != 1 {
		t.Fatalf("stats response = %#v", result)
	}

	resp, err = http.Get(server.URL + "/v1/stats?since=not-a-time")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}
	apiErr := decodeJSON[core.APIErrorResponse](t, resp)
	if apiErr.Error.Code != core.ErrValidationFailed {
		t.Fatalf("error code = %q, want %q", apiErr.Error.Code, core.ErrValidationFailed)
	}
}

func TestListIssuesFilterByExternalKey(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, external_key, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-a', 'test-1', 'proj-1', 'project', 'Issue A', 'temporal:wf-1', '', 'open', 3, '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, external_key, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-b', 'test-2', 'proj-1', 'project', 'Issue B', 'gh://abevz/af-coordinator/issues/26', '', 'open', 3, '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/issues?project=test&external_key=gh%3A%2F%2Fabevz%2Faf-coordinator%2Fissues%2F26")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issues []struct {
			ID          string `json:"id"`
			ExternalKey string `json:"external_key"`
		} `json:"issues"`
	}
	result = decodeJSON[struct {
		Issues []struct {
			ID          string `json:"id"`
			ExternalKey string `json:"external_key"`
		} `json:"issues"`
	}](t, resp)

	if len(result.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result.Issues))
	}
	if result.Issues[0].ID != "issue-b" {
		t.Fatalf("issue id = %q, want issue-b", result.Issues[0].ID)
	}
}

func TestListIssuesShowsHolder(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and an issue.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Claimed', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Claim the issue via the API.
	body := `{"holder":"agent-99","ttl_seconds":3600}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on claim, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List issues — the holder should appear in the response.
	resp, err = http.Get(server.URL + "/v1/issues?project=test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issues []struct {
			ID             string `json:"id"`
			Holder         string `json:"holder"`
			LeaseExpiresAt string `json:"lease_expires_at"`
		} `json:"issues"`
	}
	result = decodeJSON[struct {
		Issues []struct {
			ID             string `json:"id"`
			Holder         string `json:"holder"`
			LeaseExpiresAt string `json:"lease_expires_at"`
		} `json:"issues"`
	}](t, resp)

	if len(result.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result.Issues))
	}
	if result.Issues[0].Holder != "agent-99" {
		t.Errorf("expected holder 'agent-99', got %q", result.Issues[0].Holder)
	}
	if result.Issues[0].LeaseExpiresAt == "" {
		t.Error("expected non-empty lease_expires_at")
	}
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

func TestHealth(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var health struct {
		Status string `json:"status"`
	}
	health = decodeJSON[struct {
		Status string `json:"status"`
	}](t, resp)

	if health.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", health.Status)
	}
}

func TestHealthz(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestListReadyIssuesScopesRepoByProject(t *testing.T) {
	server, db := newTestServer(t)

	if _, err := sqlite.CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.CreateProject(context.Background(), db, "p2", "Project 2", ""); err != nil {
		t.Fatal(err)
	}

	repo1, _, err := sqlite.CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p1-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := sqlite.CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p2-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sqlite.CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo1.ID,
		Title:     "P1 issue",
	}); err != nil {
		t.Fatal(err)
	}
	want, err := sqlite.CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo2.ID,
		Title:     "P2 issue",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/issues/ready?project=p2&repo=shared")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}
	result = decodeJSON[struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}](t, resp)

	if len(result.Issues) != 1 {
		t.Fatalf("expected 1 ready issue, got %d", len(result.Issues))
	}
	if result.Issues[0].ID != want.ID {
		t.Fatalf("expected issue %q, got %q", want.ID, result.Issues[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Worktrees
// ---------------------------------------------------------------------------

func TestCreateWorktree(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and repo
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"repo":"repo-1","absolute_path":"/tmp/worktree","branch":"feature-1"}`
	resp, err := http.Post(server.URL+"/v1/worktrees", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		Worktree struct {
			ID           string `json:"id"`
			AbsolutePath string `json:"absolute_path"`
			Branch       string `json:"branch"`
		} `json:"worktree"`
	}
	result = decodeJSON[struct {
		Worktree struct {
			ID           string `json:"id"`
			AbsolutePath string `json:"absolute_path"`
			Branch       string `json:"branch"`
		} `json:"worktree"`
	}](t, resp)

	if result.Worktree.AbsolutePath != "/tmp/worktree" {
		t.Errorf("expected absolute_path '/tmp/worktree', got %q", result.Worktree.AbsolutePath)
	}
}

// ---------------------------------------------------------------------------
// Artifact Roots
// ---------------------------------------------------------------------------

func TestCreateArtifactRoot(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and repo
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"repo":"repo-1","root_path":"docs/specs","kind":"spec"}`
	resp, err := http.Post(server.URL+"/v1/artifact-roots", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		ArtifactRoot struct {
			ID       string `json:"id"`
			RootPath string `json:"root_path"`
			Kind     string `json:"kind"`
		} `json:"artifact_root"`
	}
	result = decodeJSON[struct {
		ArtifactRoot struct {
			ID       string `json:"id"`
			RootPath string `json:"root_path"`
			Kind     string `json:"kind"`
		} `json:"artifact_root"`
	}](t, resp)

	if result.ArtifactRoot.RootPath != "docs/specs" {
		t.Errorf("expected root_path 'docs/specs', got %q", result.ArtifactRoot.RootPath)
	}
}

func TestDeleteWorktree(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO worktrees (id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch, is_main, is_ephemeral, last_seen_at, created_at, updated_at)
		 VALUES ('wt-1', 'repo-1', '/tmp/worktree', 'feature-1', '', '', '', 0, 0, ?, ?, ?)`,
		now, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/worktrees/wt-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	result := decodeJSON[struct {
		Worktree struct {
			ID string `json:"id"`
		} `json:"worktree"`
	}](t, resp)
	if result.Worktree.ID != "wt-1" {
		t.Fatalf("expected deleted worktree id wt-1, got %q", result.Worktree.ID)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM worktrees WHERE id = 'wt-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected worktree row to be deleted, found %d row(s)", count)
	}
}

func TestDeleteWorktreeRejectsMain(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO worktrees (id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch, is_main, is_ephemeral, last_seen_at, created_at, updated_at)
		 VALUES ('wt-main', 'repo-1', '/tmp/main', 'main', '', '', '', 1, 0, ?, ?, ?)`,
		now, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/worktrees/wt-main", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", resp.StatusCode)
	}

	errResp := decodeJSON[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, resp)
	if errResp.Error.Code != core.ErrConflict {
		t.Fatalf("expected error code %q, got %q", core.ErrConflict, errResp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Artifacts
// ---------------------------------------------------------------------------

func TestCreateArtifact(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and repo
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, created_at, updated_at)
		 VALUES ('repo-1', 'proj-1', 'main', '/tmp/repo', 'main', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"repo":"repo-1","kind":"spec","relative_path":"docs/api.md"}`
	resp, err := http.Post(server.URL+"/v1/artifacts", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	var result struct {
		Artifact struct {
			ID           string `json:"id"`
			RelativePath string `json:"relative_path"`
			Kind         string `json:"kind"`
		} `json:"artifact"`
	}
	result = decodeJSON[struct {
		Artifact struct {
			ID           string `json:"id"`
			RelativePath string `json:"relative_path"`
			Kind         string `json:"kind"`
		} `json:"artifact"`
	}](t, resp)

	if result.Artifact.RelativePath != "docs/api.md" {
		t.Errorf("expected relative_path 'docs/api.md', got %q", result.Artifact.RelativePath)
	}
}

// ---------------------------------------------------------------------------
// Update Issue
// ---------------------------------------------------------------------------

func TestUpdateIssue(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Original', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"title":"Updated","external_key":"temporal:workflow-456","expected_version":1,"actor":"test"}`
	req, err := http.NewRequest("PATCH", server.URL+"/v1/issues/"+issueID, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Issue struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			ExternalKey string `json:"external_key"`
		} `json:"issue"`
	}
	result = decodeJSON[struct {
		Issue struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			ExternalKey string `json:"external_key"`
		} `json:"issue"`
	}](t, resp)

	if result.Issue.Title != "Updated" {
		t.Errorf("expected title 'Updated', got %q", result.Issue.Title)
	}
	if result.Issue.ExternalKey != "temporal:workflow-456" {
		t.Errorf("expected external_key to update, got %q", result.Issue.ExternalKey)
	}
}

func TestUpdateIssueRejectsExpiredLease(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Expired owner', '', 'in_progress', 3, '', 2, ?, ?)`,
		issueID, now, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO leases (issue_id, holder, lease_token, lease_generation, expires_at, created_at, updated_at)
		 VALUES (?, 'agent-1', 'expired-token', 1, ?, ?, ?)`,
		issueID, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), now, now,
	); err != nil {
		t.Fatal(err)
	}

	body := `{"title":"stale owner write","expected_version":2,"lease_token":"expired-token","lease_generation":1,"actor":"agent-1"}`
	req, err := http.NewRequest("PATCH", server.URL+"/v1/issues/"+issueID, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("status = %d, want 410 Gone", resp.StatusCode)
	}
	envelope := decodeJSON[core.APIErrorResponse](t, resp)
	if envelope.Error.Code != core.ErrLeaseExpired {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, core.ErrLeaseExpired)
	}

	var title string
	var version int
	if err := db.QueryRow(`SELECT title, version FROM issues WHERE id = ?`, issueID).Scan(&title, &version); err != nil {
		t.Fatal(err)
	}
	if title != "Expired owner" || version != 2 {
		t.Fatalf("expired update changed issue: title=%q version=%d", title, version)
	}
}

// ---------------------------------------------------------------------------
// Close Issue
// ---------------------------------------------------------------------------

func TestCloseIssue(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Closable', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	leaseToken := "test-close-token"
	_, err = db.Exec(
		`INSERT INTO leases (issue_id, holder, lease_token, lease_generation, expires_at, created_at, updated_at)
		 VALUES (?, 'test', ?, 1, ?, ?, ?)`,
		issueID, leaseToken, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE issues SET status = 'in_progress', version = 2 WHERE id = ?`, issueID)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"resolution":"done","branch":"codex/afc-27","pr_url":"https://github.com/abevz/af-coordinator/pull/27","commit_sha":"ba6d011","expected_version":2,"lease_token":"test-close-token","lease_generation":1,"actor":"test"}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/close", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
		Branch     string `json:"branch"`
		PRURL      string `json:"pr_url"`
		CommitSHA  string `json:"commit_sha"`
		ClosedAt   string `json:"closed_at"`
	}
	result = decodeJSON[struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
		Branch     string `json:"branch"`
		PRURL      string `json:"pr_url"`
		CommitSHA  string `json:"commit_sha"`
		ClosedAt   string `json:"closed_at"`
	}](t, resp)

	if result.Status != "closed" || result.Resolution != "done" {
		t.Fatalf("unexpected close response: %+v", result)
	}
	if result.Branch != "codex/afc-27" || result.PRURL != "https://github.com/abevz/af-coordinator/pull/27" || result.CommitSHA != "ba6d011" {
		t.Fatalf("unexpected structured close refs: %+v", result)
	}
	if result.ClosedAt == "" {
		t.Fatal("expected closed_at in close response")
	}
}

func TestOperatorCloseAndReopenIssue(t *testing.T) {
	server, db := newTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	t.Setenv("AF_OPERATOR_TOKEN", "test-token")
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	issueID := "issue-operator"
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, issue_type, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'epic', 'Closable epic', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	); err != nil {
		t.Fatal(err)
	}
	rejectedReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+issueID+"/operator-close",
		strings.NewReader(`{"resolution":"done","expected_version":1,"actor":"operator","reason":"all child work complete","lease_token":"fake"}`))
	if err != nil {
		t.Fatal(err)
	}
	rejectedReq.Header.Set("Content-Type", "application/json")
	rejectedReq.Header.Set("Authorization", "Bearer test-token")
	rejectedResp, err := http.DefaultClient.Do(rejectedReq)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("operator close with lease token status = %d, want %d", rejectedResp.StatusCode, http.StatusBadRequest)
	}
	_ = rejectedResp.Body.Close()

	closeReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+issueID+"/operator-close",
		strings.NewReader(`{"resolution":"done","expected_version":1,"actor":"operator","reason":"all child work complete"}`))
	if err != nil {
		t.Fatal(err)
	}
	closeReq.Header.Set("Content-Type", "application/json")
	closeReq.Header.Set("Authorization", "Bearer test-token")
	closeResp, err := http.DefaultClient.Do(closeReq)
	if err != nil {
		t.Fatal(err)
	}
	if closeResp.StatusCode != http.StatusOK {
		t.Fatalf("operator close status = %d", closeResp.StatusCode)
	}
	_ = closeResp.Body.Close()

	reopenReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+issueID+"/operator-reopen",
		strings.NewReader(`{"expected_version":2,"actor":"operator","reason":"parent requires follow-up"}`))
	if err != nil {
		t.Fatal(err)
	}
	reopenReq.Header.Set("Content-Type", "application/json")
	reopenReq.Header.Set("Authorization", "Bearer test-token")
	reopenResp, err := http.DefaultClient.Do(reopenReq)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenResp.Body.Close()
	if reopenResp.StatusCode != http.StatusOK {
		t.Fatalf("operator reopen status = %d", reopenResp.StatusCode)
	}
	result := decodeJSON[struct {
		Issue struct {
			Status   string `json:"status"`
			ClosedAt string `json:"closed_at"`
		} `json:"issue"`
	}](t, reopenResp)
	if result.Issue.Status != "open" || result.Issue.ClosedAt != "" {
		t.Fatalf("unexpected reopen response: %+v", result.Issue)
	}
}

func TestOperatorCloseIssueWithMetadata(t *testing.T) {
	server, db := newTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	t.Setenv("AF_OPERATOR_TOKEN", "test-token")
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	issueID := "issue-meta"
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Meta close', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	); err != nil {
		t.Fatal(err)
	}

	body := `{"resolution":"done","expected_version":1,"actor":"operator","reason":"merged via PR","branch":"feat/close-meta","pr_url":"https://github.com/example/repo/pull/99","commit_sha":"abc123def","note":"Closed via operator after merge"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/"+issueID+"/operator-close", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
		Branch     string `json:"branch"`
		PRURL      string `json:"pr_url"`
		CommitSHA  string `json:"commit_sha"`
		ClosedAt   string `json:"closed_at"`
	}
	result = decodeJSON[struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
		Branch     string `json:"branch"`
		PRURL      string `json:"pr_url"`
		CommitSHA  string `json:"commit_sha"`
		ClosedAt   string `json:"closed_at"`
	}](t, resp)

	if result.Status != "closed" || result.Resolution != "done" {
		t.Fatalf("unexpected close response: %+v", result)
	}
	if result.Branch != "feat/close-meta" {
		t.Fatalf("result.Branch = %q, want feat/close-meta", result.Branch)
	}
	if result.PRURL != "https://github.com/example/repo/pull/99" {
		t.Fatalf("result.PRURL = %q, want https://github.com/example/repo/pull/99", result.PRURL)
	}
	if result.CommitSHA != "abc123def" {
		t.Fatalf("result.CommitSHA = %q, want abc123def", result.CommitSHA)
	}
	if result.ClosedAt == "" {
		t.Fatal("expected closed_at in close response")
	}

	// Verify note was inserted.
	var noteCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE issue_id = ?`, issueID).Scan(&noteCount); err != nil {
		t.Fatal(err)
	}
	if noteCount != 1 {
		t.Fatalf("expected 1 note, got %d", noteCount)
	}
}

// TestOperatorCloseReopenTokenValidation verifies that operator-close and
// operator-reopen fail closed with 403 forbidden when AF_OPERATOR_TOKEN is
// missing/empty or when the Authorization header does not match the expected
// Bearer token. os.Getenv returns "" for both a missing and an empty
// AF_OPERATOR_TOKEN, so the empty env value deterministically simulates the
// not-configured branch for both. These subtests must not call t.Parallel
// because they rely on t.Setenv.
func TestOperatorCloseReopenTokenValidation(t *testing.T) {
	closeBody := `{"resolution":"done","expected_version":1,"actor":"operator","reason":"token validation"}`
	reopenBody := `{"expected_version":1,"actor":"operator","reason":"token validation"}`

	tests := []struct {
		name       string
		endpoint   string
		body       string
		envToken   string
		authHeader string
		wantMsg    string
	}{
		{
			name:       "close missing/empty token env",
			endpoint:   "operator-close",
			body:       closeBody,
			envToken:   "",
			authHeader: "Bearer test-token",
			wantMsg:    "AF_OPERATOR_TOKEN not configured on server",
		},
		{
			name:       "reopen missing/empty token env",
			endpoint:   "operator-reopen",
			body:       reopenBody,
			envToken:   "",
			authHeader: "Bearer test-token",
			wantMsg:    "AF_OPERATOR_TOKEN not configured on server",
		},
		{
			name:       "close mismatched token",
			endpoint:   "operator-close",
			body:       closeBody,
			envToken:   "test-token",
			authHeader: "Bearer wrong-token",
			wantMsg:    "invalid or missing operator token",
		},
		{
			name:       "reopen mismatched token",
			endpoint:   "operator-reopen",
			body:       reopenBody,
			envToken:   "test-token",
			authHeader: "Bearer wrong-token",
			wantMsg:    "invalid or missing operator token",
		},
		{
			name:       "close missing authorization header",
			endpoint:   "operator-close",
			body:       closeBody,
			envToken:   "test-token",
			authHeader: "",
			wantMsg:    "invalid or missing operator token",
		},
		{
			name:       "reopen missing authorization header",
			endpoint:   "operator-reopen",
			body:       reopenBody,
			envToken:   "test-token",
			authHeader: "",
			wantMsg:    "invalid or missing operator token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AF_OPERATOR_TOKEN", tt.envToken)
			server, _ := newTestServer(t)

			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/issue-token-validation/"+tt.endpoint, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
			apiErr := decodeJSON[core.APIErrorResponse](t, resp)
			if apiErr.Error.Code != core.ErrForbidden {
				t.Fatalf("error code = %q, want %q", apiErr.Error.Code, core.ErrForbidden)
			}
			if apiErr.Error.Message != tt.wantMsg {
				t.Fatalf("error message = %q, want %q", apiErr.Error.Message, tt.wantMsg)
			}
		})
	}
}

func TestCloseIssueWithoutLeaseReturnsGone(t *testing.T) {
	server, db := newTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', 'proj-1', 'project', 'Unleased', '', 'open', 3, '', 1, ?, ?)`, now, now,
	); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/issues/issue-1/close",
		strings.NewReader(`{"resolution":"done","expected_version":1,"lease_token":"missing","actor":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("close without lease status = %d, want %d", resp.StatusCode, http.StatusGone)
	}
}

// ---------------------------------------------------------------------------
// Notes
// ---------------------------------------------------------------------------

func TestCreateAndListNotes(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Noted', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Create a note
	noteBody := `{"author":"tester","body":"This is a note"}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/notes", strings.NewReader(noteBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created for note, got %d", resp.StatusCode)
	}

	// List notes
	resp, err = http.Get(server.URL + "/v1/issues/" + issueID + "/notes")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for note list, got %d", resp.StatusCode)
	}

	var notesResult struct {
		Notes []struct {
			ID     string `json:"id"`
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"notes"`
	}
	notesResult = decodeJSON[struct {
		Notes []struct {
			ID     string `json:"id"`
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"notes"`
	}](t, resp)

	if len(notesResult.Notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notesResult.Notes))
	}
	if notesResult.Notes[0].Body != "This is a note" {
		t.Errorf("expected note body 'This is a note', got %q", notesResult.Notes[0].Body)
	}
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

func TestListEvents(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Eventful', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Insert an event directly
	_, err = db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES ('evt-1', ?, 'system', 'issue.created', '{}', ?)`,
		issueID, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/issues/" + issueID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var eventsResult struct {
		Events []struct {
			ID        string `json:"id"`
			EventType string `json:"event_type"`
		} `json:"events"`
	}
	eventsResult = decodeJSON[struct {
		Events []struct {
			ID        string `json:"id"`
			EventType string `json:"event_type"`
		} `json:"events"`
	}](t, resp)

	if len(eventsResult.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(eventsResult.Events))
	}
	if eventsResult.Events[0].EventType != "issue.created" {
		t.Errorf("expected event_type 'issue.created', got %q", eventsResult.Events[0].EventType)
	}
}

func TestWatchEvents(t *testing.T) {
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', 'proj-1', 'project', 'Eventful', '', 'open', 3, '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES ('evt-1', 'issue-1', 'system', 'issue.created', '{"title":"Eventful"}', ?)`,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(server.URL + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	result := decodeJSON[core.EventPage](t, resp)
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.NextSince == "" {
		t.Fatal("expected non-empty next_since")
	}
	if !json.Valid([]byte(result.Events[0].PayloadJSON)) {
		t.Fatalf("expected valid payload_json, got %q", result.Events[0].PayloadJSON)
	}

	resp, err = http.Get(server.URL + "/v1/events?since=" + result.NextSince)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on cursor follow-up, got %d", resp.StatusCode)
	}
	followUp := decodeJSON[core.EventPage](t, resp)
	if len(followUp.Events) != 0 {
		t.Fatalf("expected 0 follow-up events, got %d", len(followUp.Events))
	}
	if followUp.NextSince != result.NextSince {
		t.Fatalf("expected next_since to stay at %q, got %q", result.NextSince, followUp.NextSince)
	}
}

func TestWatchEventsInvalidCursor(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/v1/events?since=bad-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}
}

func TestWatchEventsAcceptsLegacyCursor(t *testing.T) {
	server, db := newTestServer(t)

	const createdAt = "2026-07-13T20:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', 'proj-1', 'project', 'Eventful', '', 'open', 3, '', 1, ?, ?)`,
		createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at) VALUES
		 ('evt-1', 'issue-1', 'system', 'first_inserted', '{}', ?),
		 ('evt-2', 'issue-1', 'system', 'second_inserted', '{}', ?)`,
		createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}

	legacy, err := json.Marshal(map[string]string{"id": "evt-1", "created_at": createdAt})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(server.URL + "/v1/events?since=v1." + base64.RawURLEncoding.EncodeToString(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200 for legacy cursor, got %d", resp.StatusCode)
	}
	result := decodeJSON[core.EventPage](t, resp)
	if len(result.Events) != 1 || result.Events[0].ID != "evt-2" {
		t.Fatalf("legacy cursor events = %#v", result.Events)
	}
	if !strings.HasPrefix(result.NextSince, "v2.") {
		t.Fatalf("next_since = %q, want v2 cursor", result.NextSince)
	}
}

func TestWatchEventsLongPollTimeout(t *testing.T) {
	server, _ := newTestServer(t)

	start := time.Now()
	resp, err := http.Get(server.URL + "/v1/events?wait_ms=50")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("expected long-poll to wait at least ~40ms, got %v", elapsed)
	}

	result := decodeJSON[core.EventPage](t, resp)
	if len(result.Events) != 0 {
		t.Fatalf("expected no events, got %d", len(result.Events))
	}
	if result.NextSince != "" {
		t.Fatalf("expected empty next_since, got %q", result.NextSince)
	}
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

func TestAddRemoveDependency(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and two issues
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', 'proj-1', 'project', 'First', '', 'open', 3, '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-2', 'test-2', 'proj-1', 'project', 'Second', '', 'open', 3, '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Add dependency
	depBody := `{"depends_on":"issue-2","kind":"blocks","actor":"test"}`
	req, err := http.NewRequest("POST", server.URL+"/v1/issues/issue-1/dependencies", strings.NewReader(depBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created for dependency, got %d", resp.StatusCode)
	}

	// Remove dependency
	req, err = http.NewRequest("DELETE", server.URL+"/v1/issues/issue-1/dependencies/issue-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for dependency removal, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Release lease
// ---------------------------------------------------------------------------

func TestReleaseLease(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Leased', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Claim first to get a token
	claimBody := `{"holder":"agent-1","ttl_seconds":3600}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(claimBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var claimResp struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
	}
	claimResp = decodeJSON[struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
	}](t, resp)

	// Release the lease
	releaseBody := fmt.Sprintf(`{"lease_token":%q,"lease_generation":%d}`, claimResp.LeaseToken, claimResp.LeaseGeneration)
	req, _ = http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/release", strings.NewReader(releaseBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Heartbeat lease
// ---------------------------------------------------------------------------

func TestHeartbeatLease(t *testing.T) {
	server, db := newTestServer(t)

	// Create project and issue
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-1', 'test', 'Test', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	issueID := "issue-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'test-1', 'proj-1', 'project', 'Heartbeatable', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Claim first to get a token
	claimBody := `{"holder":"agent-1","ttl_seconds":3600}`
	req, _ := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(claimBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var claimResp struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
	}
	claimResp = decodeJSON[struct {
		LeaseToken      string `json:"lease_token"`
		LeaseGeneration int64  `json:"lease_generation"`
		ExpiresAt       string `json:"expires_at"`
	}](t, resp)

	// Heartbeat
	hbBody := fmt.Sprintf(`{"lease_token":%q,"lease_generation":%d,"ttl_seconds":3600}`, claimResp.LeaseToken, claimResp.LeaseGeneration)
	req, _ = http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/heartbeat", strings.NewReader(hbBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var hbResp struct {
		ExpiresAt string `json:"expires_at"`
	}
	hbResp = decodeJSON[struct {
		ExpiresAt string `json:"expires_at"`
	}](t, resp)

	if hbResp.ExpiresAt == "" {
		t.Error("expected non-empty expires_at in heartbeat response")
	}
}

// ─── Concurrency ────────────────────────────────────────────────────────────

func TestConcurrentClaimSameIssue(t *testing.T) {
	t.Parallel()
	server, db := newTestServer(t)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-concur', 'concur', 'Concur', '', 1, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	issueID := "issue-concur-1"
	_, err = db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, 'concur-1', 'proj-concur', 'project', 'Concurrent claim', '', 'open', 3, '', 1, ?, ?)`,
		issueID, now, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 2
	results := make(chan int, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			body := `{"holder":"agent-` + fmt.Sprintf("%d", id) + `","ttl_seconds":3600}`
			req, reqErr := http.NewRequest("POST", server.URL+"/v1/issues/"+issueID+"/claim", strings.NewReader(body))
			if reqErr != nil {
				t.Errorf("goroutine %d: build request: %v", id, reqErr)
				results <- 0
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, reqErr := http.DefaultClient.Do(req)
			if reqErr != nil {
				t.Errorf("goroutine %d: do request: %v", id, reqErr)
				results <- 0
				return
			}
			resp.Body.Close()
			results <- resp.StatusCode
		}(i)
	}

	statuses := make([]int, 0, goroutines)
	for i := 0; i < goroutines; i++ {
		statuses = append(statuses, <-results)
	}

	successCount := 0
	conflictCount := 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			successCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Errorf("unexpected status: %d", s)
		}
	}

	if successCount != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCount)
	}
	if conflictCount != 1 {
		t.Errorf("expected exactly 1 conflict (409), got %d", conflictCount)
	}
}

// claimOverHTTP posts a claim body and returns the status plus decoded fields.
func claimOverHTTP(t *testing.T, serverURL, issueID, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", serverURL+"/v1/issues/"+issueID+"/claim", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	return resp.StatusCode, decoded
}

func seedClaimableIssueRow(t *testing.T, db *sql.DB, issueID, shortID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES ('proj-op', 'op', 'Op', '', 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES (?, ?, 'proj-op', 'project', 'Claimable', '', 'open', 3, '', 1, ?, ?)`,
		issueID, shortID, now, now); err != nil {
		t.Fatal(err)
	}
}

// TestClaimIdempotentReplayOverHTTP exercises AFC-SDD-0159 end to end through
// the transport: the same operation_id replays the original outcome, a new
// operation_id gets normal lease_held, and a mismatched reuse fails closed.
func TestClaimIdempotentReplayOverHTTP(t *testing.T) {
	server, db := newTestServer(t)
	issueID := "issue-op-1"
	seedClaimableIssueRow(t, db, issueID, "op-1")

	const operationID = "op-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	body := `{"holder":"agent-1","ttl_seconds":3600,"operation_id":"` + operationID + `"}`

	status, first := claimOverHTTP(t, server.URL, issueID, body)
	if status != http.StatusOK {
		t.Fatalf("first claim status = %d, want 200", status)
	}
	token, _ := first["lease_token"].(string)
	if token == "" {
		t.Fatal("expected a lease token")
	}

	status, replay := claimOverHTTP(t, server.URL, issueID, body)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", status)
	}
	if replay["lease_token"] != first["lease_token"] {
		t.Error("replay did not return the original lease token")
	}
	if replay["lease_generation"] != first["lease_generation"] {
		t.Error("replay did not preserve the original lease generation")
	}
	if replay["attempt_id"] != first["attempt_id"] {
		t.Error("replay did not preserve the original attempt id")
	}

	// A different operation ID is a new logical operation: normal conflict,
	// and it must not disclose the active token.
	newOp := `{"holder":"agent-1","ttl_seconds":3600,"operation_id":"op-11112222-3333-4444-5555-666677778888"}`
	status, conflict := claimOverHTTP(t, server.URL, issueID, newOp)
	if status != http.StatusConflict {
		t.Fatalf("new operation status = %d, want 409", status)
	}
	if encoded, _ := json.Marshal(conflict); strings.Contains(string(encoded), token) {
		t.Error("lease_held response disclosed the active lease token")
	}

	// Same operation ID, different arguments: typed idempotency conflict.
	mismatch := `{"holder":"agent-1","ttl_seconds":60,"operation_id":"` + operationID + `"}`
	status, conflictBody := claimOverHTTP(t, server.URL, issueID, mismatch)
	if status != http.StatusConflict {
		t.Fatalf("mismatched reuse status = %d, want 409", status)
	}
	errObj, _ := conflictBody["error"].(map[string]any)
	if errObj == nil || errObj["code"] != core.ErrIdempotencyConflict {
		t.Fatalf("mismatched reuse error = %v, want %s", conflictBody, core.ErrIdempotencyConflict)
	}
}

// TestIssueReadNeverExposesOperationID confirms the ledger stays invisible to
// ordinary agent-facing reads.
func TestIssueReadNeverExposesOperationID(t *testing.T) {
	server, db := newTestServer(t)
	issueID := "issue-op-2"
	seedClaimableIssueRow(t, db, issueID, "op-2")

	const operationID = "op-secret00-1111-2222-3333-444455556666"
	status, claimed := claimOverHTTP(t, server.URL, issueID,
		`{"holder":"agent-1","ttl_seconds":3600,"session_id":"s-1","operation_id":"`+operationID+`"}`)
	if status != http.StatusOK {
		t.Fatalf("claim status = %d, want 200", status)
	}
	token, _ := claimed["lease_token"].(string)

	for _, path := range []string{"/v1/issues/" + issueID, "/v1/issues"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), operationID) {
			t.Errorf("%s exposed the operation_id", path)
		}
		if token != "" && strings.Contains(string(payload), token) {
			t.Errorf("%s exposed the lease token", path)
		}
	}
}
