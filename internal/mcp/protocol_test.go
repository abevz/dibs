package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/api"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/internal/testsocket"
	"github.com/abevz/dibs/migrations"
)

func TestRunNewlineJSONAgainstTestDaemon(t *testing.T) {
	c := startMCPTestDaemon(t)
	if _, err := c.CreateProject(context.Background(), "afc", "Test project", ""); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateIssue(context.Background(), core.CreateIssueRequest{
		Project: "afc", ScopeKind: "project", Title: "Ready via MCP", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		"", // Blank lines are ignored.
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/unknown"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_ready_issues","arguments":{"project":"afc"}}}`,
		"",
	}, "\n")
	var stdout bytes.Buffer
	if err := NewServer(c, "", "test").Run(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("Content-Length:")) {
		t.Fatalf("stdout has LSP framing: %q", stdout.String())
	}
	lines := bytes.Split(stdout.Bytes(), []byte("\n"))
	if len(lines) != 4 || len(lines[3]) != 0 {
		t.Fatalf("want exactly three newline-delimited responses, got %q", stdout.String())
	}
	for i, line := range lines[:3] {
		if bytes.ContainsAny(line, "\r\n") {
			t.Fatalf("response %d has embedded newline: %q", i, line)
		}
		var response struct {
			ID     int `json:"id"`
			Result struct {
				ServerInfo struct {
					Name string `json:"name"`
				} `json:"serverInfo"`
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
				StructuredContent struct {
					Issues []core.Issue `json:"issues"`
				} `json:"structuredContent"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("response %d is not JSON: %v: %q", i, err, line)
		}
		if response.ID != i+1 || response.Error != nil {
			t.Fatalf("response %d id/error = %d/%v", i, response.ID, response.Error)
		}
		switch i {
		case 0:
			if response.Result.ServerInfo.Name != "dibs-mcp" {
				t.Fatalf("initialize server = %q", response.Result.ServerInfo.Name)
			}
		case 1:
			if len(response.Result.Tools) == 0 {
				t.Fatal("tools/list returned no tools")
			}
		case 2:
			if len(response.Result.StructuredContent.Issues) != 1 || response.Result.StructuredContent.Issues[0].ShortID != created.ShortID {
				t.Fatalf("ready issues = %+v, want %s", response.Result.StructuredContent.Issues, created.ShortID)
			}
		}
	}
}

func TestRunHandlesLargeNewlineRequest(t *testing.T) {
	name := strings.Repeat("x", 70*1024)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"` + name + `"}}}` + "\n"
	if len(input) <= 64*1024 {
		t.Fatal("fixture must exceed Scanner's default limit")
	}
	var stdout bytes.Buffer
	if err := NewServer(&fakeClient{}, "", "test").Run(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID     int `json:"id"`
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSuffix(stdout.Bytes(), []byte("\n")), &response); err != nil {
		t.Fatalf("large response is not newline JSON: %v", err)
	}
	if response.ID != 1 || response.Result.ServerInfo.Name != "dibs-mcp" || !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) {
		t.Fatalf("large request failed: %q", stdout.String())
	}
}

func startMCPTestDaemon(t *testing.T) *client.Client {
	t.Helper()
	dir := testsocket.Dir(t)
	dbPath, socketPath := filepath.Join(dir, "test.db"), filepath.Join(dir, "dibs.sock")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- api.RunDaemon(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{DBPath: dbPath, SocketPath: socketPath}, sqlite.NewStore(db))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("test daemon: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("test daemon did not stop")
		}
	})
	c := client.New(socketPath)
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := c.Health(probe)
		stop()
		if err == nil {
			return c
		}
		select {
		case daemonErr := <-done:
			t.Fatalf("test daemon exited: %v", daemonErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("test daemon not ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
