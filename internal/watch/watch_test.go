package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
)

type eventResponse struct {
	page core.EventPage
	err  error
}

type sourceFixture struct {
	stats     map[string]report.ProjectStats
	notes     []core.Note
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

func (f *sourceFixture) GetStats(context.Context, report.Query) (report.Report, error) {
	return report.Report{ByProject: f.stats}, nil
}
func (f *sourceFixture) GetIssue(_ context.Context, id string) (core.Issue, *core.IssueLease, error) {
	for _, issue := range f.issues {
		if issue.ID == id {
			return issue, nil, nil
		}
	}
	return core.Issue{}, nil, errors.New("missing issue")
}
func (f *sourceFixture) ListNotes(context.Context, string) ([]core.Note, error) { return f.notes, nil }

func TestSummaryIncludesDormantProjectsAndCounts(t *testing.T) {
	fixture := &sourceFixture{stats: map[string]report.ProjectStats{
		"empty": {}, "deferred": {Deferred: 4}, "busy": {Open: 3, Ready: 2, Blocked: 1, InProgress: 1, Deferred: 2, LastEventAt: "2026-10-01T12:00:00Z"},
	}}
	snapshot, err := New(fixture, "").Refresh(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projects) != 3 || snapshot.Projects[0].Key != "busy" || snapshot.Projects[1].Key != "deferred" || snapshot.Projects[2].Key != "empty" {
		t.Fatalf("summary order: %#v", snapshot.Projects)
	}
	row := snapshot.Projects[0]
	if row.Ready != 2 || row.Blocked != 1 || row.InProgress != 1 || row.Deferred != 2 || row.LastEventAt != "2026-10-01T12:00:00Z" {
		t.Fatalf("counts/activity lost: %#v", row)
	}
}

func TestStaleDetectionAndClockExpiry(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fixture := &sourceFixture{issues: []core.Issue{
		{ID: "stale", ShortID: "demo-1", Status: "in_progress", UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "expired", ShortID: "demo-2", Status: "in_progress", Holder: "agent", LeaseExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)},
		{ID: "live", ShortID: "demo-3", Status: "in_progress", Holder: "agent", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339)},
		{ID: "deferred", Status: "deferred", LeaseExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "done", Status: "done", LeaseExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "open", Status: "open"},
	}}
	snapshot, err := New(fixture, "").Refresh(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Stale) != 2 || len(snapshot.Active) != 1 {
		t.Fatalf("classification: %#v", snapshot)
	}
	if snapshot.Stale[0].Reason != "since update (no active lease)" || snapshot.Stale[1].Reason != "since expiry" {
		t.Fatalf("age basis: %#v", snapshot.Stale)
	}
	later := snapshot.At(now.Add(time.Minute))
	if len(later.Active) != 0 || len(later.Stale) != 3 {
		t.Fatalf("clock failed to expire lease: %#v", later)
	}
	if len(snapshot.Active) != 1 || len(snapshot.Stale) != 2 {
		t.Fatal("clock mutated previous snapshot")
	}
	output := RenderOnce(snapshot, now)
	if !strings.Contains(output, "1h0m0s since update") || !strings.Contains(output, "1m0s since expiry") {
		t.Fatalf("stale age missing: %s", output)
	}
}

func TestOnceOutputIsCompleteAndUnpadded(t *testing.T) {
	snapshot := Snapshot{Projects: []ProjectSummary{{Key: "demo", ProjectStats: report.ProjectStats{Ready: 40, InProgress: 1}}}}
	for i := 0; i < 40; i++ {
		issue := core.Issue{ID: fmt.Sprint(i), ShortID: fmt.Sprintf("demo-%d", i), Status: "open", Title: "ready"}
		snapshot.Ready = append(snapshot.Ready, issue)
		snapshot.Issues = append(snapshot.Issues, issue)
	}
	snapshot.Issues = append(snapshot.Issues, core.Issue{ShortID: "demo-stale", Status: "in_progress"})
	output := RenderOnce(snapshot, time.Now())
	for _, part := range []string{"PROJECT SUMMARY", "IN_PROGRESS", "STALE (1)", "demo-stale", "READY (40)", "demo-39"} {
		if !strings.Contains(output, part) {
			t.Fatalf("missing %q in %s", part, output)
		}
	}
	if strings.HasSuffix(output, "\n") || strings.Contains(output, "q quit") {
		t.Fatal("one-shot contains terminal padding/footer")
	}
}

func TestDetailsShowLastFiveNotes(t *testing.T) {
	fixture := &sourceFixture{issues: []core.Issue{{ID: "issue", Description: "description"}}}
	for i := 6; i >= 0; i-- {
		fixture.notes = append(fixture.notes, core.Note{Body: fmt.Sprint(i), CreatedAt: fmt.Sprintf("2026-10-01T12:00:0%dZ", i)})
	}
	detail, err := New(fixture, "").Detail(context.Background(), "issue")
	if err != nil || detail.Issue.Description != "description" || len(detail.Notes) != 5 || detail.Notes[0].Body != "2" || detail.Notes[4].Body != "6" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	for _, width := range []int{38, 80} {
		view := RenderDetail(detail, nil, false, width, 12, 1000)
		for _, line := range strings.Split(view, "\n") {
			if runewidth.StringWidth(line) > width {
				t.Fatalf("too wide: %q", line)
			}
		}
	}
}

func TestOnceActiveLeaseUnknownProcessMetadata(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snapshot := Snapshot{Issues: []core.Issue{{ShortID: "demo-1", Status: "in_progress", Holder: "agent", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339)}}}
	output := RenderOnce(snapshot, now)
	if !strings.Contains(output, "PID ?") || strings.Contains(output, "PID 0@") {
		t.Fatalf("invalid unknown process output: %s", output)
	}
	snapshot.Issues[0].LeasePID = 1234
	snapshot.Issues[0].LeaseHost = "host"
	if output = RenderOnce(snapshot, now); !strings.Contains(output, "PID 1234@host") {
		t.Fatalf("missing process output: %s", output)
	}
}

type waitFixture struct {
	*sourceFixture
	page  core.EventPage
	err   error
	calls []string
}

func (f *waitFixture) WatchEvents(_ context.Context, since string, limit, waitMS int) (core.EventPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s/%d/%d", since, limit, waitMS))
	return f.page, f.err
}

func TestWaitForEventsLongPollsOneEventAfterCursor(t *testing.T) {
	tests := []struct {
		name      string
		cursor    string
		page      core.EventPage
		err       error
		want      bool
		wantErr   bool
		wantCalls []string
	}{
		{name: "no snapshot yet", cursor: "", want: true},
		{name: "event arrived", cursor: "c1", page: core.EventPage{Events: []core.Event{{Sequence: 2}}, NextSince: "c2"}, want: true, wantCalls: []string{"c1/1/4000"}},
		{name: "timed out quietly", cursor: "c1", page: core.EventPage{NextSince: "c1"}, want: false, wantCalls: []string{"c1/1/4000"}},
		{name: "daemon error", cursor: "c1", err: errors.New("down"), wantErr: true, wantCalls: []string{"c1/1/4000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &waitFixture{sourceFixture: &sourceFixture{}, page: tt.page, err: tt.err}
			changed, err := New(fixture, "").WaitForEvents(context.Background(), tt.cursor, 4*time.Second)
			if (err != nil) != tt.wantErr || changed != tt.want {
				t.Fatalf("changed=%v err=%v, want changed=%v err=%v", changed, err, tt.want, tt.wantErr)
			}
			if fmt.Sprint(fixture.calls) != fmt.Sprint(tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", fixture.calls, tt.wantCalls)
			}
		})
	}
}

func TestRefreshExposesCursorForChangeWaits(t *testing.T) {
	fixture := &sourceFixture{responses: []eventResponse{{page: core.EventPage{NextSince: "cursor-1"}}}}
	service := New(fixture, "")
	if service.Cursor() != "" {
		t.Fatalf("cursor before refresh = %q", service.Cursor())
	}
	if _, err := service.Refresh(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if service.Cursor() != "cursor-1" {
		t.Fatalf("cursor after refresh = %q, want cursor-1", service.Cursor())
	}
}

func TestNextLeaseExpiry(t *testing.T) {
	now := time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC)
	lease := func(offset time.Duration) core.Issue {
		return core.Issue{Holder: "agent", LeaseExpiresAt: now.Add(offset).Format(time.RFC3339)}
	}
	tests := []struct {
		name   string
		active []core.Issue
		want   time.Time
		ok     bool
	}{
		{name: "no active leases"},
		{name: "earliest future expiry", active: []core.Issue{lease(10 * time.Minute), lease(90 * time.Second), lease(time.Hour)}, want: now.Add(90 * time.Second), ok: true},
		{name: "ignores expired and malformed", active: []core.Issue{lease(-time.Second), {LeaseExpiresAt: "soon"}, lease(time.Minute)}, want: now.Add(time.Minute), ok: true},
		{name: "only expired", active: []core.Issue{lease(0), lease(-time.Minute)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Snapshot{Active: tt.active}.NextLeaseExpiry(now)
			if ok != tt.ok || !got.Equal(tt.want) {
				t.Fatalf("NextLeaseExpiry = %v,%v want %v,%v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
