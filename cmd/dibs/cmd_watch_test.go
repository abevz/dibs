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
		case "/v1/events/recent", "/v1/events/watch":
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
	m := watchModel{snapshot: watch.Snapshot{Project: "demo", Issues: []core.Issue{{ID: "a"}, {ID: "b"}}}, selected: 1}
	model, _ := m.Update(watchSnapshotMsg{snapshot: watch.Snapshot{Project: "demo", Issues: []core.Issue{{ID: "b"}, {ID: "a"}}}})
	if model.(watchModel).selected != 0 {
		t.Fatal("selection switched issues on reorder")
	}
}
