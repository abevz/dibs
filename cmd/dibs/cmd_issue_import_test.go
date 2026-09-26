package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/github"
)

type fakeImportGitHub struct {
	issue github.Issue
	err   error
}

func (f fakeImportGitHub) GetIssue(context.Context, github.IssueRef) (github.Issue, error) {
	return f.issue, f.err
}
func (fakeImportGitHub) ListComments(context.Context, string) ([]github.Comment, error) {
	return nil, nil
}
func (fakeImportGitHub) CreateComment(context.Context, string, string) (github.Comment, error) {
	return github.Comment{}, nil
}

type importFixture struct {
	client *client.Client
	mu     sync.Mutex
	issues map[string]core.Issue
	count  int
}

func newImportFixture(t *testing.T, gitDir string) *importFixture {
	t.Helper()
	fixture := &importFixture{issues: make(map[string]core.Issue)}
	socketPath := filepath.Join(testSocketDir(t), "import.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []core.Project{{ID: "project-uuid", Key: "demo"}}})
		case r.Method == "GET" && r.URL.Path == "/v1/repos":
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []core.Repository{{ID: "repo-uuid", ProjectID: "project-uuid", LogicalName: "app", CanonicalGitDir: gitDir}}})
		case r.Method == "GET" && r.URL.Path == "/v1/issues":
			fixture.mu.Lock()
			var matches []core.Issue
			for _, issue := range fixture.issues {
				if issue.ExternalKey == r.URL.Query().Get("external_key") {
					matches = append(matches, issue)
				}
			}
			fixture.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": matches})
		case r.Method == "POST" && r.URL.Path == "/v1/issues":
			var req core.CreateIssueRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create: %v", err)
			}
			fixture.mu.Lock()
			issue, ok := fixture.issues[req.OperationID]
			if !ok {
				fixture.count++
				issue = core.Issue{ID: "issue-uuid", ShortID: "demo-1", ProjectID: "project-uuid", ScopeKind: req.ScopeKind, RepositoryID: "repo-uuid", Title: req.Title, Description: req.Description, ExternalKey: req.ExternalKey, Status: "open", Tags: req.Tags}
				fixture.issues[req.OperationID] = issue
			}
			fixture.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"issue": issue})
		default:
			t.Errorf("unexpected API call %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	fixture.client = client.New(socketPath)
	return fixture
}

func TestIssueImportNewRepeatedAndConcurrent(t *testing.T) {
	t.Setenv("DIBS_ACTOR", "import-test")
	fixture := newImportFixture(t, "/tmp/unrelated-git")
	gh := fakeImportGitHub{issue: github.Issue{Title: "Fix login", Body: "![shot](https://example.test/img.png)", State: "open", HTMLURL: "https://github.com/Acme/App/issues/42"}}
	args := []string{"Acme/App#42", "--project", "demo", "--repo", "app", "--tag", "area/login"}
	first, _, err := importIssue(context.Background(), fixture.client, gh, args)
	if err != nil || !first.Imported || first.Issue.ScopeKind != "repository" || first.Issue.ExternalKey != "github:acme/app#42" || !strings.Contains(first.Issue.Description, "![shot]") {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	second, _, err := importIssue(context.Background(), fixture.client, fakeImportGitHub{err: errors.New("must not fetch again")}, args)
	if err != nil || second.Imported || second.Issue.ID != first.Issue.ID {
		t.Fatalf("repeated import = %+v, %v", second, err)
	}
	fixture.mu.Lock()
	if fixture.count != 1 {
		t.Fatalf("created %d issues", fixture.count)
	}
	fixture.mu.Unlock()

	concurrent := newImportFixture(t, "/tmp/unrelated-git")
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := importIssue(context.Background(), concurrent.client, gh, args); err != nil {
				t.Errorf("concurrent import: %v", err)
			}
		}()
	}
	wg.Wait()
	concurrent.mu.Lock()
	defer concurrent.mu.Unlock()
	if concurrent.count != 1 {
		t.Fatalf("concurrent imports created %d issues", concurrent.count)
	}
}

func TestIssueImportSourceRejections(t *testing.T) {
	t.Setenv("DIBS_ACTOR", "import-test")
	fixture := newImportFixture(t, "/tmp/unrelated-git")
	args := []string{"o/r#1", "--project", "demo"}
	for _, tc := range []struct {
		name  string
		issue github.Issue
		err   error
		want  string
	}{
		{"closed", github.Issue{State: "closed"}, nil, "--allow-closed"},
		{"pull request", github.Issue{State: "open", PullRequest: json.RawMessage(`{}`)}, nil, "pull requests"},
		{"auth", github.Issue{}, &github.Error{Code: "gh_auth", Remedy: "run gh auth login"}, "gh_auth"},
		{"missing", github.Issue{}, &github.Error{Code: "gh_missing", Remedy: "install gh"}, "gh_missing"},
		{"not found", github.Issue{}, &github.Error{Code: "not_found", Remedy: "check access"}, "not_found"},
		{"rate limited", github.Issue{}, &github.Error{Code: "rate_limited", Remedy: "retry"}, "rate_limited"},
		{"timeout", github.Issue{}, &github.Error{Code: "timeout", Remedy: "retry"}, "timeout"},
		{"github", github.Issue{}, &github.Error{Code: "github", Remedy: "retry"}, "github"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := importIssue(context.Background(), fixture.client, fakeImportGitHub{issue: tc.issue, err: tc.err}, args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.count != 0 {
		t.Fatalf("rejected imports created %d issues", fixture.count)
	}
}

func TestIssueImportCWDResolution(t *testing.T) {
	t.Setenv("DIBS_ACTOR", "import-test")
	gitDir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", gitDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	fixture := newImportFixture(t, filepath.Join(gitDir, ".git"))
	gh := fakeImportGitHub{issue: github.Issue{Title: "Task", State: "open", HTMLURL: "https://github.com/o/r/issues/1"}}
	got, _, err := importIssue(context.Background(), fixture.client, gh, []string{"o/r#1"})
	if err != nil || got.Issue.ScopeKind != "repository" {
		t.Fatalf("cwd import = %+v, %v", got, err)
	}
}

func TestIssueImportCWDLegacyCheckoutRegistration(t *testing.T) {
	root := t.TempDir()
	mainCheckout := filepath.Join(root, "main")
	linkedCheckout := filepath.Join(root, "linked")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", mainCheckout},
		{"-C", mainCheckout, "config", "user.name", "Test"},
		{"-C", mainCheckout, "config", "user.email", "test@example.invalid"},
		{"-C", mainCheckout, "commit", "-q", "--allow-empty", "-m", "initial"},
		{"-C", mainCheckout, "worktree", "add", "-q", "-b", "feature", linkedCheckout},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(linkedCheckout); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	// Legacy dibs registrations store the checkout path, not .git.
	fixture := newImportFixture(t, mainCheckout)
	gh := fakeImportGitHub{issue: github.Issue{Title: "Task", State: "open", HTMLURL: "https://github.com/o/r/issues/1"}}
	got, _, err := importIssue(context.Background(), fixture.client, gh, []string{"o/r#1"})
	if err != nil || !got.Imported || got.Issue.ScopeKind != "repository" {
		t.Fatalf("legacy checkout import = %+v, %v", got, err)
	}
}

func TestIssueImportRefRejectedBeforeDaemon(t *testing.T) {
	for _, ref := range []string{"https://github.com/o/r/pull/1", "https://elsewhere.test/o/r/issues/1", "o/r#0"} {
		if err := validateCommandArgs([]string{"issue", "import", ref}); err == nil {
			t.Fatalf("accepted %q before daemon", ref)
		}
	}
}

func TestImportOperationID(t *testing.T) {
	key := "github:acme/app#42"
	first := importOperationID("project-uuid", key)
	if first != importOperationID("project-uuid", key) || first == importOperationID("another-project", key) || first == importOperationID("project-uuid", "github:acme/app#43") {
		t.Fatalf("operation ID is not stable and source-specific: %s", first)
	}
}
