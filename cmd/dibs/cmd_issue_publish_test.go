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
	"testing"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/github"
)

type fakePublisher struct {
	source          github.Issue
	getErr          error
	comments        []github.Comment
	getCalls        int
	listCalls       int
	createCalls     int
	lastPostBody    string
	lastCommentsURL string
}

func (f *fakePublisher) GetIssue(context.Context, github.IssueRef) (github.Issue, error) {
	f.getCalls++
	return f.source, f.getErr
}

func (f *fakePublisher) ListComments(_ context.Context, commentsURL string) ([]github.Comment, error) {
	f.listCalls++
	f.lastCommentsURL = commentsURL
	return append([]github.Comment(nil), f.comments...), nil
}

func (f *fakePublisher) CreateComment(_ context.Context, commentsURL, body string) (github.Comment, error) {
	f.createCalls++
	f.lastCommentsURL = commentsURL
	f.lastPostBody = body
	comment := github.Comment{Body: body, HTMLURL: "https://github.com/o/r/issues/1#issuecomment-1"}
	f.comments = append(f.comments, comment)
	return comment, nil
}

type publishFixture struct {
	client     *client.Client
	socketPath string
	dbPath     string
	issue      core.Issue
	events     []core.Event
	notes      []core.Note
	claimCalls int
	closeCalls int
	allowClose bool
	allowClaim bool
}

func newPublishFixture(t *testing.T) *publishFixture {
	t.Helper()
	t.Setenv("DIBS_LEASE_TOKEN", "")
	t.Setenv("DIBS_OPERATOR_TOKEN", "")
	t.Setenv("AF_OPERATOR_TOKEN", "")
	fixture := &publishFixture{
		dbPath: filepath.Join(t.TempDir(), "dibs.db"),
		issue:  core.Issue{ID: "issue-uuid", ShortID: "app-7", Status: "done", ExternalKey: "github:o/r#1"},
		events: []core.Event{
			{Sequence: 10, EventType: "note_added", Actor: "agent", CreatedAt: "2026-09-26T16:45:00Z"},
			{ID: "close-event-one", Sequence: 11, EventType: "issue_closed", Actor: "agent", CreatedAt: "2026-09-26T16:45:00Z", PayloadJSON: `{"resolution":"done","branch":"fix/login","pr_url":"https://github.com/o/r/pull/3","commit_sha":"abc123"}`},
		},
		notes: []core.Note{{Author: "agent", CreatedAt: "2026-09-26T16:45:00Z", Body: "done"}},
	}
	socketPath := filepath.Join(testSocketDir(t), "publish.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "db_path": fixture.dbPath})
		case "/v1/issues/app-7/claim":
			fixture.claimCalls++
			if !fixture.allowClaim {
				w.WriteHeader(http.StatusInternalServerError)
				break
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"lease_token": "test-token", "lease_generation": 1, "expires_at": "2099-01-01T00:00:00Z", "attempt_id": "attempt-one", "version": 3})
		case "/v1/issues/app-7/close":
			fixture.closeCalls++
			if !fixture.allowClose {
				w.WriteHeader(http.StatusInternalServerError)
				break
			}
			fixture.issue.Status = "done"
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "closed", "resolution": "done"})
		case "/v1/issues/app-7", "/v1/issues/issue-uuid":
			_ = json.NewEncoder(w).Encode(map[string]any{"issue": fixture.issue})
		case "/v1/issues/issue-uuid/events":
			_ = json.NewEncoder(w).Encode(map[string]any{"events": fixture.events})
		case "/v1/issues/issue-uuid/notes":
			_ = json.NewEncoder(w).Encode(map[string]any{"notes": fixture.notes})
		default:
			t.Errorf("unexpected daemon request %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	fixture.client = client.New(socketPath)
	fixture.socketPath = socketPath
	return fixture
}

func TestClosePublishFailureKeepsLocalCloseAndJSON(t *testing.T) {
	fixture := newPublishFixture(t)
	fixture.allowClose = true
	bin := buildAfctlForRunTest(t)
	fakeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeDir, "gh"), []byte("#!/bin/sh\necho 'gh: Not Found (HTTP 404)' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--json", "issue", "close", "app-7", "--resolution", "done", "--expected-version", "3", "--lease-generation", "1", "--publish")
	cmd.Env = append(os.Environ(), "DIBS_SOCKET="+fixture.socketPath, "DIBS_LEASE_TOKEN=test-token", "PATH="+fakeDir+":"+os.Getenv("PATH"), "HOME="+t.TempDir(), "DIBS_DB="+fixture.dbPath)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("local close must succeed despite publish failure: %v; stdout=%s; stderr=%s", err, output, err.(*exec.ExitError).Stderr)
	}
	var result struct {
		Status  string        `json:"status"`
		Publish publishResult `json:"publish"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid JSON: %s: %v", output, err)
	}
	if result.Status != "closed" || result.Publish.OK || result.Publish.Error == nil || result.Publish.Error.Code != "not_found" || fixture.closeCalls != 1 {
		t.Fatalf("result=%+v closeCalls=%d", result, fixture.closeCalls)
	}
	cmd = exec.Command(bin, "--json", "issue", "publish", "app-7")
	cmd.Env = append(os.Environ(), "DIBS_SOCKET="+fixture.socketPath, "PATH="+fakeDir+":"+os.Getenv("PATH"), "HOME="+t.TempDir(), "DIBS_DB="+fixture.dbPath)
	output, err = cmd.Output()
	if err == nil {
		t.Fatal("explicit publish must fail nonzero")
	}
	var explicit struct {
		Issue string `json:"issue"`
		publishResult
	}
	if err := json.Unmarshal(output, &explicit); err != nil || explicit.Issue != "app-7" || explicit.Error == nil || explicit.Error.Code != "not_found" {
		t.Fatalf("explicit publish output=%s err=%v decoded=%+v", output, err, explicit)
	}
}

func TestRunPublishFailureKeepsLocalCloseAndJSON(t *testing.T) {
	fixture := newPublishFixture(t)
	fixture.allowClaim, fixture.allowClose = true, true
	fixture.issue.Status = "open"
	bin := buildAfctlForRunTest(t)
	fakeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeDir, "gh"), []byte("#!/bin/sh\necho 'gh: Not Found (HTTP 404)' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--json", "issue", "run", "app-7", "--actor", "agent", "--publish", "--", "true")
	cmd.Env = append(os.Environ(), "DIBS_SOCKET="+fixture.socketPath, "PATH="+fakeDir+":"+os.Getenv("PATH"), "HOME="+t.TempDir(), "DIBS_DB="+fixture.dbPath)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run close must succeed despite publish failure: %v; stdout=%s; stderr=%s", err, output, err.(*exec.ExitError).Stderr)
	}
	var result struct {
		Status  string        `json:"status"`
		Publish publishResult `json:"publish"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid JSON: %s: %v", output, err)
	}
	if result.Status != "closed" || result.Publish.OK || result.Publish.Error == nil || result.Publish.Error.Code != "not_found" || fixture.claimCalls != 1 || fixture.closeCalls != 1 {
		t.Fatalf("result=%+v claim=%d close=%d", result, fixture.claimCalls, fixture.closeCalls)
	}
}

func TestPublishMissingKeyStopsBeforeCloseOrClaim(t *testing.T) {
	fixture := newPublishFixture(t)
	fixture.issue.ExternalKey = ""
	err := requireGitHubExternalKey(t.Context(), fixture.client, "app-7")
	if err == nil || !strings.Contains(err.Error(), "no GitHub external key") {
		t.Fatalf("preflight = %v", err)
	}
	// The close handler reaches this check after argument validation, before
	// the close API request. The run handler checks before claim.
	t.Setenv("DIBS_LEASE_TOKEN", "test-token")
	if err := runIssueClose(t.Context(), fixture.client, []string{"app-7", "--resolution", "done", "--expected-version", "3", "--lease-generation", "1", "--publish"}); err == nil {
		t.Fatal("close accepted issue without GitHub key")
	}
	if err := runIssueRun(t.Context(), fixture.client, []string{"app-7", "--actor", "agent", "--publish", "--", "true"}); err == nil {
		t.Fatal("run accepted issue without GitHub key")
	}
	if fixture.closeCalls != 0 || fixture.claimCalls != 0 {
		t.Fatalf("close=%d claim=%d", fixture.closeCalls, fixture.claimCalls)
	}
}

func TestPublishRecloseUsesNewEventID(t *testing.T) {
	fixture := newPublishFixture(t)
	gh := &fakePublisher{source: github.Issue{CommentsURL: "https://api.github.com/repos/new/repo/issues/1/comments"}}
	if _, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue); err != nil {
		t.Fatal(err)
	}
	fixture.events = append(fixture.events, core.Event{ID: "close-event-two", Sequence: 13, EventType: "issue_closed", Actor: "agent", CreatedAt: "2026-09-26T16:45:00Z", PayloadJSON: `{"resolution":"cancelled"}`})
	result, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	if err != nil || result.Already || gh.createCalls != 2 {
		t.Fatalf("reclose = %+v, %v, calls=%d", result, err, gh.createCalls)
	}
	if !strings.Contains(gh.lastPostBody, "closed as **cancelled**") || !strings.Contains(gh.lastPostBody, "close_event=close-event-two") || strings.Contains(gh.lastPostBody, "> done") {
		t.Fatalf("wrong reclose comment: %s", gh.lastPostBody)
	}
}

func TestPublishResultJSONShape(t *testing.T) {
	for _, result := range []publishResult{
		{OK: true, CommentURL: "https://github.com/o/r/issues/1#issuecomment-1"},
		{OK: true, Already: true, CommentURL: "https://github.com/o/r/issues/1#issuecomment-1"},
		failedPublish(&github.Error{Code: "locked", Remedy: "unlock the issue"}),
	} {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"ok", "already", "comment_url", "error"} {
			if _, ok := got[key]; !ok {
				t.Fatalf("missing %s: %s", key, data)
			}
		}
		if result.Error != nil && got["error"].(map[string]any)["code"] != "locked" {
			t.Fatalf("error: %s", data)
		}
	}
}

func TestPublishPreconditionsAvoidGitHub(t *testing.T) {
	fixture := newPublishFixture(t)
	gh := &fakePublisher{source: github.Issue{Locked: false}}
	fixture.issue.Status = "in_progress"
	if _, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue); err == nil || gh.getCalls != 0 {
		t.Fatalf("in-progress publish: %v, GitHub calls %d", err, gh.getCalls)
	}
	fixture.issue.Status = "done"
	fixture.issue.ExternalKey = ""
	if _, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue); err == nil || gh.getCalls != 0 {
		t.Fatalf("no-key publish: %v, GitHub calls %d", err, gh.getCalls)
	}
}

func TestPublishPreflightLockedAndNotFound(t *testing.T) {
	fixture := newPublishFixture(t)
	gh := &fakePublisher{source: github.Issue{Locked: true}}
	_, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	var ghErr *github.Error
	if !errors.As(err, &ghErr) || ghErr.Code != "locked" || gh.listCalls != 0 || gh.createCalls != 0 {
		t.Fatalf("locked preflight = %v, calls %+v", err, gh)
	}
	gh = &fakePublisher{getErr: &github.Error{Code: "not_found", Remedy: "check access"}}
	_, err = publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	if !errors.As(err, &ghErr) || ghErr.Code != "not_found" || gh.listCalls != 0 || gh.createCalls != 0 {
		t.Fatalf("missing preflight = %v, calls %+v", err, gh)
	}
}

func TestPublishRepeatAndSecretGuard(t *testing.T) {
	fixture := newPublishFixture(t)
	gh := &fakePublisher{source: github.Issue{CommentsURL: "https://api.github.com/repos/new/repo/issues/1/comments"}}
	first, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	if err != nil || !first.OK || first.Already || gh.createCalls != 1 || gh.lastCommentsURL != gh.source.CommentsURL {
		t.Fatalf("first publish = %+v, %v, calls %d", first, err, gh.createCalls)
	}
	for _, part := range []string{"app-7", "done", "fix/login", "abc123", "https://github.com/o/r/pull/3", "> done", "<!-- dibs:publish issue=issue-uuid close_event=close-event-one -->"} {
		if !strings.Contains(gh.lastPostBody, part) {
			t.Fatalf("comment lacks %q: %s", part, gh.lastPostBody)
		}
	}
	second, err := publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	if err != nil || !second.OK || !second.Already || second.CommentURL != first.CommentURL || gh.createCalls != 1 {
		t.Fatalf("repeat publish = %+v, %v, calls %d", second, err, gh.createCalls)
	}
	t.Setenv("DIBS_OPERATOR_TOKEN", "synthetic-secret")
	fixture.notes[0].Body = "contains synthetic-secret"
	_, err = publishForIssue(t.Context(), fixture.client, gh, fixture.issue)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") || gh.createCalls != 1 {
		t.Fatalf("secret guard = %v, calls %d", err, gh.createCalls)
	}
}
