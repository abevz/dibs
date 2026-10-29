package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
	"github.com/abevz/dibs/internal/testsocket"
	"github.com/abevz/dibs/internal/watch"
	tea "github.com/charmbracelet/bubbletea"
)

func watchTestClient(t *testing.T) *client.Client {
	t.Helper()
	socket := testsocket.Path(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("watch issued write: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		var body any
		issue := core.Issue{ID: "i", ShortID: "demo-1", ProjectID: "p", Title: "Example issue", Status: "in_progress", Description: "Long description", UpdatedAt: "2026-10-01T10:00:00Z", Dependencies: []core.Dependency{{Kind: "related", DependsOnShortID: "demo-2"}}}
		switch r.URL.Path {
		case "/v1/projects":
			body = map[string]any{"projects": []core.Project{{ID: "p", Key: "demo"}}}
		case "/v1/issues":
			body = map[string]any{"issues": []core.Issue{issue}}
		case "/v1/issues/ready":
			body = map[string]any{"issues": []core.Issue{}}
		case "/v1/stats":
			body = map[string]any{"report": report.Report{ByProject: map[string]report.ProjectStats{"demo": {InProgress: 1, LastEventAt: "2026-10-01T10:00:00Z"}}}}
		case "/v1/events/recent", "/v1/events":
			body = core.EventPage{NextSince: "cursor"}
		case "/v1/issues/i":
			body = map[string]any{"issue": issue}
		case "/v1/issues/i/notes":
			body = map[string]any{"notes": []core.Note{{Body: "Last note", CreatedAt: "2026-10-01T10:00:00Z"}}}
		default:
			t.Errorf("unexpected GET: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(body)
	})
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return client.New(socket)
}

func TestWatchOnceGoldenIncludesSummaryStaleAndSingleNewline(t *testing.T) {
	c := watchTestClient(t)
	oldJSON, oldStdout := jsonOutput, os.Stdout
	defer func() { jsonOutput, os.Stdout = oldJSON, oldStdout }()
	jsonOutput = false
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := runWatch(context.Background(), c, []string{"--once"})
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	os.Stdout = oldStdout
	if runErr != nil || err != nil {
		t.Fatalf("run=%v read=%v", runErr, err)
	}
	for _, part := range []string{"PROJECT SUMMARY", "IN_PROGRESS", "STALE (1)", "demo-1", "since update"} {
		if !strings.Contains(string(out), part) {
			t.Fatalf("missing %q in %s", part, out)
		}
	}
	if !strings.HasSuffix(string(out), "\n") || strings.HasSuffix(string(out), "\n\n") {
		t.Fatalf("bad trailing newlines: %q", out)
	}
	// Only elapsed wall time varies; preserve every whitespace byte so the
	// golden catches terminal padding and extra trailing blank lines.
	normalized := regexp.MustCompile(`demo-1  \S+ since update`).ReplaceAllString(
		string(out), "demo-1  <age> since update",
	)
	want, err := os.ReadFile(filepath.Join("testdata", "watch-once.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if normalized != string(want) {
		t.Fatalf("watch --once output mismatch\ngot:  %q\nwant: %q", normalized, want)
	}
}

func TestWatchNavigationProjectIssueDetailsAndBack(t *testing.T) {
	service := watch.New(watchTestClient(t), "")
	snapshot, err := service.Refresh(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m := watchModel{ctx: context.Background(), service: service, snapshot: snapshot, width: 100, height: 28, now: time.Now()}
	press := func(key tea.KeyMsg) tea.Cmd { model, cmd := m.Update(key); m = model.(watchModel); return cmd }
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	back := tea.KeyMsg{Type: tea.KeyEsc}
	cmd := press(enter)
	if cmd == nil || m.snapshot.Project != "demo" {
		t.Fatal("project did not open")
	}
	model, _ := m.Update(cmd())
	m = model.(watchModel)
	cmd = press(enter)
	if cmd == nil || m.detailID != "i" {
		t.Fatal("issue did not open")
	}
	model, _ = m.Update(cmd())
	m = model.(watchModel)
	for _, part := range []string{"Long description", "related demo-2", "Last note"} {
		if !strings.Contains(m.View(), part) {
			t.Fatalf("detail missing %q: %s", part, m.View())
		}
	}
	press(back)
	if m.detailID != "" || m.snapshot.Project != "demo" {
		t.Fatal("detail back failed")
	}
	cmd = press(back)
	if cmd == nil || m.snapshot.Project != "" {
		t.Fatal("project back failed")
	}
	model, _ = m.Update(cmd())
	m = model.(watchModel)
	if cmd = press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd == nil {
		t.Fatal("r no longer refreshes")
	}
	if cmd = press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q no longer quits")
	}
}

func TestWatchSelectionSurvivesRefresh(t *testing.T) {
	m := watchModel{service: watch.New(&countingWatchSource{}, "demo"), snapshot: watch.Snapshot{Project: "demo", Issues: []core.Issue{{ID: "a"}, {ID: "b"}}}, selected: 1}
	model, _ := m.Update(watchSnapshotMsg{snapshot: watch.Snapshot{Project: "demo", Issues: []core.Issue{{ID: "b"}, {ID: "a"}}}})
	if model.(watchModel).selected != 0 {
		t.Fatal("selection switched issues on reorder")
	}
}

// countingWatchSource counts full snapshot reads and event long-polls so
// tests can prove an idle board stops re-reading the coordinator.
type countingWatchSource struct {
	snapshots int
	waits     []int
	events    []core.Event
	active    []core.Issue
}

func (s *countingWatchSource) GetStats(context.Context, report.Query) (report.Report, error) {
	return report.Report{}, nil
}
func (s *countingWatchSource) GetIssue(context.Context, string) (core.Issue, *core.IssueLease, error) {
	return core.Issue{}, nil, nil
}
func (s *countingWatchSource) ListNotes(context.Context, string) ([]core.Note, error) {
	return nil, nil
}
func (s *countingWatchSource) ListProjects(context.Context) ([]core.Project, error) {
	return []core.Project{{ID: "p", Key: "demo"}}, nil
}
func (s *countingWatchSource) ListIssuesWithFilters(context.Context, core.IssueListParams) ([]core.Issue, error) {
	s.snapshots++
	return s.active, nil
}
func (s *countingWatchSource) ListReadyIssues(context.Context, string, string, []string) ([]core.Issue, error) {
	return nil, nil
}
func (s *countingWatchSource) RecentEvents(context.Context, int) (core.EventPage, error) {
	return core.EventPage{NextSince: "cursor"}, nil
}
func (s *countingWatchSource) WatchEvents(_ context.Context, since string, limit, waitMS int) (core.EventPage, error) {
	if limit == 1 {
		s.waits = append(s.waits, waitMS)
		return core.EventPage{Events: s.events, NextSince: since}, nil
	}
	return core.EventPage{NextSince: since}, nil
}

func TestWatchRefreshesOnlyOnEventOrDeadline(t *testing.T) {
	source := &countingWatchSource{}
	m := watchModel{ctx: context.Background(), service: watch.New(source, "demo"), interval: time.Minute, snapshot: watch.Snapshot{Project: "demo"}, loading: true}
	step := func(cmd tea.Cmd) tea.Msg {
		t.Helper()
		if cmd == nil {
			t.Fatal("no follow-up command")
		}
		msg := cmd()
		model, next := m.Update(msg)
		m = model.(watchModel)
		if next != nil {
			return next
		}
		return nil
	}

	next := step(m.fetch())
	if source.snapshots != 1 || m.loading {
		t.Fatalf("initial snapshot: reads=%d loading=%v", source.snapshots, m.loading)
	}
	for i := 0; i < 3; i++ {
		next = step(next.(tea.Cmd))
	}
	if source.snapshots != 1 {
		t.Fatalf("idle board re-read the snapshot %d times", source.snapshots-1)
	}
	if len(source.waits) != 3 || source.waits[0] != int(watchLongPoll/time.Millisecond) {
		t.Fatalf("waits = %v, want three %v long-polls", source.waits, watchLongPoll)
	}

	source.events = []core.Event{{Sequence: 1}}
	next = step(next.(tea.Cmd))
	if !m.loading {
		t.Fatal("event did not start a refresh")
	}
	step(next.(tea.Cmd))
	if source.snapshots != 2 || m.loading {
		t.Fatalf("after event: reads=%d loading=%v", source.snapshots, m.loading)
	}

	source.events = nil
	m.dueAt = time.Now().Add(-time.Second)
	waitsBefore := len(source.waits)
	model, cmd := m.Update(watchChangeMsg{gen: m.waitGen})
	m = model.(watchModel)
	if !m.loading || cmd == nil || len(source.waits) != waitsBefore {
		t.Fatal("deadline did not start a refresh")
	}
}

func TestWatchIgnoresStaleChangeReplies(t *testing.T) {
	m := watchModel{ctx: context.Background(), service: watch.New(&countingWatchSource{}, "demo"), waitGen: 3, dueAt: time.Now().Add(time.Minute)}
	if model, cmd := m.Update(watchChangeMsg{gen: 2, changed: true}); cmd != nil || model.(watchModel).loading {
		t.Fatal("stale long-poll reply started a refresh")
	}
	m.loading = true
	if _, cmd := m.Update(watchChangeMsg{gen: 3, changed: true}); cmd != nil {
		t.Fatal("reply during a running refresh started another one")
	}
	m.loading = false
	model, cmd := m.Update(watchChangeMsg{gen: 3, err: context.DeadlineExceeded})
	if cmd == nil || model.(watchModel).lastErr == nil || model.(watchModel).loading {
		t.Fatal("long-poll error was not surfaced and retried")
	}
}

func TestWatchSchedulesRefreshAtLeaseExpiry(t *testing.T) {
	now := time.Date(2026, 10, 29, 16, 0, 0, 0, time.UTC)
	expiry := now.Add(30 * time.Second)
	tests := []struct {
		name     string
		interval time.Duration
		active   []core.Issue
		want     time.Time
	}{
		{name: "fallback interval", interval: time.Minute, want: now.Add(time.Minute)},
		{name: "default interval", want: now.Add(defaultWatchInterval)},
		{name: "lease expires first", interval: time.Minute, active: []core.Issue{{LeaseExpiresAt: expiry.Format(time.RFC3339)}}, want: expiry.Add(time.Second)},
		{name: "lease expires after interval", interval: 10 * time.Second, active: []core.Issue{{LeaseExpiresAt: expiry.Format(time.RFC3339)}}, want: now.Add(10 * time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := watchModel{interval: tt.interval, snapshot: watch.Snapshot{Active: tt.active}}
			m.scheduleRefresh(now)
			if !m.dueAt.Equal(tt.want) {
				t.Fatalf("dueAt = %v, want %v", m.dueAt, tt.want)
			}
		})
	}
}

func TestParseWatchInterval(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: "30s", want: 30 * time.Second},
		{value: "5m", want: 5 * time.Minute},
		{value: "2s", want: 2 * time.Second},
		{value: "1s", wantErr: true},
		{value: "0", wantErr: true},
		{value: "-1m", wantErr: true},
		{value: "soon", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseWatchInterval(tt.value)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("parseWatchInterval(%q) = %v,%v", tt.value, got, err)
			}
		})
	}
}
