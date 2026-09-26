package watch

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/abevz/dibs/internal/core"
)

type eventResponse struct {
	page core.EventPage
	err  error
}

type sourceFixture struct {
	projects  []core.Project
	issues    []core.Issue
	ready     []core.Issue
	responses []eventResponse
	cursors   []string
	project   string
}

func (f *sourceFixture) ListProjects(context.Context) ([]core.Project, error) {
	return f.projects, nil
}

func (f *sourceFixture) ListIssuesWithFilters(context.Context, core.IssueListParams) ([]core.Issue, error) {
	return f.issues, nil
}

func (f *sourceFixture) ListReadyIssues(_ context.Context, project, _ string, _ []string) ([]core.Issue, error) {
	f.project = project
	return f.ready, nil
}

func (f *sourceFixture) WatchEvents(_ context.Context, since string, limit, waitMS int) (core.EventPage, error) {
	if limit != eventPageSize || waitMS != 0 {
		return core.EventPage{}, errors.New("unexpected event query")
	}
	f.cursors = append(f.cursors, since)
	index := len(f.cursors) - 1
	if index >= len(f.responses) {
		return core.EventPage{NextSince: since}, nil
	}
	return f.responses[index].page, f.responses[index].err
}

func (f *sourceFixture) RecentEvents(_ context.Context, limit int) (core.EventPage, error) {
	if limit != eventPageSize {
		return core.EventPage{}, errors.New("unexpected recent event query")
	}
	f.cursors = append(f.cursors, "<recent>")
	index := len(f.cursors) - 1
	if index >= len(f.responses) {
		return core.EventPage{NextSince: "cursor-empty"}, nil
	}
	return f.responses[index].page, f.responses[index].err
}

func TestRefreshClassifiesFromDaemonReads(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	ready := core.Issue{ID: "ready", ShortID: "demo-1", ProjectID: "project", Status: "open", Title: "Ready"}
	active := core.Issue{ID: "active", ShortID: "demo-2", ProjectID: "project", Status: "in_progress", Holder: "agent-a", LeasePID: 4567, LeaseHost: "host-a", LeaseExpiresAt: now.Add(90 * time.Second).Format(time.RFC3339)}
	blocked := core.Issue{ID: "blocked", ShortID: "demo-3", ProjectID: "project", Status: "open", Dependencies: []core.Dependency{{Kind: "blocks", DependsOnID: "other", DependsOnShortID: "other-1"}}}
	other := core.Issue{ID: "other", ShortID: "other-1", ProjectID: "other-project", Status: "open"}
	fixture := &sourceFixture{
		projects: []core.Project{{ID: "project", Key: "demo"}},
		issues:   []core.Issue{ready, active, blocked, other},
		ready:    []core.Issue{ready, other},
		responses: []eventResponse{{page: core.EventPage{
			Events:    []core.Event{{IssueID: "ready", EventType: "ISSUE_CREATED"}, {IssueID: "other", EventType: "ISSUE_CREATED"}},
			NextSince: "cursor-2",
		}}},
	}
	snapshot, err := New(fixture, "demo").Refresh(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.project != "demo" || len(snapshot.Ready) != 1 || snapshot.Ready[0].ShortID != "demo-1" {
		t.Fatalf("ready scope mismatch: project=%q ready=%#v", fixture.project, snapshot.Ready)
	}
	if len(snapshot.Active) != 1 || snapshot.Active[0].Holder != "agent-a" {
		t.Fatalf("active leases = %#v", snapshot.Active)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"lease_pid":4567`) || !strings.Contains(string(encoded), `"lease_host":"host-a"`) {
		t.Fatalf("watch JSON omits process metadata: %s", encoded)
	}
	if len(snapshot.Blocked) != 1 || snapshot.Blocked[0].BlockedBy[0] != "other-1" {
		t.Fatalf("blocked issues = %#v", snapshot.Blocked)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].IssueID != "ready" {
		t.Fatalf("project events = %#v", snapshot.Events)
	}
}

func TestRefreshFailureKeepsEventCursorAndLastSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	fixture := &sourceFixture{responses: []eventResponse{
		{page: core.EventPage{Events: []core.Event{{ID: "first"}}, NextSince: "cursor-1"}},
		{err: errors.New("daemon disconnected")},
		{page: core.EventPage{Events: []core.Event{{ID: "second"}}, NextSince: "cursor-2"}},
	}}
	service := New(fixture, "")
	first, err := service.Refresh(context.Background(), now)
	if err != nil || len(first.Events) != 1 {
		t.Fatalf("first refresh: snapshot=%#v err=%v", first, err)
	}
	if _, err := service.Refresh(context.Background(), now); err == nil {
		t.Fatal("disconnection was not reported")
	}
	third, err := service.Refresh(context.Background(), now)
	if err != nil || len(third.Events) != 2 {
		t.Fatalf("retry: snapshot=%#v err=%v", third, err)
	}
	if got := strings.Join(fixture.cursors, ","); got != "<recent>,cursor-1,cursor-1" {
		t.Fatalf("event cursors = %q", got)
	}
}

func TestRenderLabelsStaleDataAndFitsResize(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	snapshot := Snapshot{
		Project:   "demo",
		Ready:     []core.Issue{{ShortID: "demo-1", Title: "Document a long workflow with Unicode 🧭"}},
		Active:    []core.Issue{{ShortID: "demo-2", Holder: "agent-a", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339)}},
		Blocked:   []BlockedIssue{{Issue: core.Issue{ShortID: "demo-3"}, BlockedBy: []string{"demo-4"}}},
		UpdatedAt: now.Add(-time.Minute),
	}
	view := Render(snapshot, &url.Error{Op: "Get", URL: "unix socket", Err: errors.New("socket unavailable")}, now, 48, 16)
	if !strings.Contains(view, "DISCONNECTED") || !strings.Contains(view, "data is stale") || !strings.Contains(view, "demo-4") {
		t.Fatalf("stale board missing state or blocker:\n%s", view)
	}
	invalidProject := Render(snapshot, errors.New("project not found: missing"), now, 48, 16)
	if !strings.Contains(invalidProject, "ERROR") || strings.Contains(invalidProject, "DISCONNECTED") {
		t.Fatalf("non-transport failure mislabeled:\n%s", invalidProject)
	}
	lines := strings.Split(view, "\n")
	if len(lines) > 16 {
		t.Fatalf("rendered %d lines for 16-row terminal", len(lines))
	}
	for _, line := range lines {
		if runewidth.StringWidth(line) > 48 {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
	compact := Render(snapshot, nil, now, 25, 8)
	if !strings.Contains(compact, "Enlarge terminal") {
		t.Fatalf("compact view did not explain resize:\n%s", compact)
	}
}

func TestRenderActiveLeaseProcessMetadata(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	active := []core.Issue{
		{ShortID: "demo-1", Holder: "agent-a", LeasePID: 1234, LeaseHost: "host-a", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339)},
		{ShortID: "demo-2", Holder: "manual", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339)},
	}
	view := Render(Snapshot{Active: active, UpdatedAt: now}, nil, now, 100, 28)
	if !strings.Contains(view, "1234@host-a") || !strings.Contains(view, "PID ?") || !strings.Contains(view, "PID self-reported") {
		t.Fatalf("process metadata missing from active leases:\n%s", view)
	}
	narrow := Render(Snapshot{Active: active, UpdatedAt: now}, nil, now, 60, 16)
	seenActive := false
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "demo-1") {
			seenActive = true
			if !strings.Contains(line, "1m0s") || !strings.Contains(line, "1234@host-a") {
				t.Fatalf("narrow active row lost TTL or full PID@host: %q", line)
			}
		}
	}
	if !seenActive {
		t.Fatalf("narrow board lost active row:\n%s", narrow)
	}
}
