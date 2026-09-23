package export_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
	coordinatorexport "github.com/abevz/dibs/internal/export"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/migrations"

	_ "modernc.org/sqlite"
)

func TestWriteJSONL(t *testing.T) {
	db := newExportTestDB(t)
	seedExportFixture(t, db)
	st := sqlite.NewStore(db)

	var buf bytes.Buffer
	if err := coordinatorexport.WriteJSONL(context.Background(), st, &buf); err != nil {
		t.Fatalf("WriteJSONL() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected 12 records, got %d\n%s", len(lines), buf.String())
	}

	counts := map[string]int{}
	issueShortIDs := map[string]bool{}
	var sawDependency coordinatorexport.Dependency
	var sawReference coordinatorexport.Reference
	var sawEvent core.Event

	for _, line := range lines {
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		counts[record.Type]++

		switch record.Type {
		case "issue":
			var issue core.Issue
			if err := json.Unmarshal(record.Payload, &issue); err != nil {
				t.Fatalf("decode issue payload: %v", err)
			}
			issueShortIDs[issue.ShortID] = true
			if len(issue.Dependencies) != 0 {
				t.Fatalf("issue payload should be normalized without embedded dependencies: %+v", issue.Dependencies)
			}
		case "dependency":
			if err := json.Unmarshal(record.Payload, &sawDependency); err != nil {
				t.Fatalf("decode dependency payload: %v", err)
			}
		case "reference":
			if err := json.Unmarshal(record.Payload, &sawReference); err != nil {
				t.Fatalf("decode reference payload: %v", err)
			}
		case "event":
			if err := json.Unmarshal(record.Payload, &sawEvent); err != nil {
				t.Fatalf("decode event payload: %v", err)
			}
		}
	}

	for _, recordType := range []string{
		"project",
		"repository",
		"repo_remote",
		"worktree",
		"artifact_root",
		"artifact",
		"dependency",
		"reference",
		"note",
		"event",
	} {
		if counts[recordType] != 1 {
			t.Fatalf("expected one %s record, got %d", recordType, counts[recordType])
		}
	}
	if counts["issue"] != 2 {
		t.Fatalf("expected two issue records, got %d", counts["issue"])
	}

	if !issueShortIDs["afc-1"] || !issueShortIDs["afc-2"] {
		t.Fatalf("expected both exported issues, got %v", issueShortIDs)
	}
	if sawDependency.DependsOnShortID != "afc-1" || sawDependency.Kind != "blocks" {
		t.Fatalf("unexpected dependency payload: %+v", sawDependency)
	}
	if sawReference.ArtifactPath != "docs/specs/005/tasks.md" || sawReference.Relation != "implements" {
		t.Fatalf("unexpected reference payload: %+v", sawReference)
	}
	if sawEvent.EventType != "issue_created" || sawEvent.PayloadJSON != `{"title":"Ship export"}` {
		t.Fatalf("unexpected event payload: %+v", sawEvent)
	}
	if sawEvent.Sequence == 0 {
		t.Fatalf("exported event sequence = 0, want daemon-assigned sequence")
	}
}

func TestWriteJSONLOrdersEventsBySequence(t *testing.T) {
	db := newExportTestDB(t)
	const createdAt = "2026-07-13T20:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at) VALUES
		 ('event-z', NULL, 'agent', 'first_inserted', '{}', ?),
		 ('event-a', NULL, 'agent', 'second_inserted', '{}', ?)`,
		createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := coordinatorexport.WriteJSONL(context.Background(), sqlite.NewStore(db), &buf); err != nil {
		t.Fatalf("WriteJSONL() error = %v", err)
	}

	var events []core.Event
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Type != "event" {
			continue
		}
		var event core.Event
		if err := json.Unmarshal(record.Payload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].ID != "event-z" || events[1].ID != "event-a" || events[0].Sequence >= events[1].Sequence {
		t.Fatalf("events not sequence ordered: %#v", events)
	}
}

func newExportTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.SetMaxOpenConns(1)
	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	return db
}

func seedExportFixture(t *testing.T, db *sql.DB) {
	t.Helper()

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"proj-1", "afc", "AF Coordinator", "Coordinator", 3, "2026-07-08T16:00:00Z", "2026-07-08T16:00:00Z"},
		},
		{
			query: `INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"repo-1", "proj-1", "main", "/tmp/af-coordinator.git", "main", "", "", "2026-07-08T16:01:00Z", "2026-07-08T16:01:00Z"},
		},
		{
			query: `INSERT INTO repo_remotes (id, repository_id, remote_name, fetch_url, push_url, is_primary, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"remote-1", "repo-1", "origin", "git@github.com:abevz/af-coordinator.git", "git@github.com:abevz/af-coordinator.git", 1, "2026-07-08T16:01:30Z", "2026-07-08T16:01:30Z"},
		},
		{
			query: `INSERT INTO worktrees (id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch, is_main, is_ephemeral, last_seen_at, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"wt-1", "repo-1", "/tmp/af-coordinator", "codex/afc-29", "abc123", "origin", "codex/afc-29", 0, 0, "2026-07-08T16:02:00Z", "2026-07-08T16:02:00Z", "2026-07-08T16:02:00Z"},
		},
		{
			query: `INSERT INTO artifact_roots (id, repository_id, root_path, kind, is_primary, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"root-1", "repo-1", "docs/specs/005-aion-forge-integration-readiness", "sdd", 1, "2026-07-08T16:03:00Z", "2026-07-08T16:03:00Z"},
		},
		{
			query: `INSERT INTO artifacts (id, repository_id, worktree_id, artifact_root_id, kind, relative_path, title, external_key, status, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"artifact-1", "repo-1", "wt-1", "root-1", "tasks", "docs/specs/005/tasks.md", "Tasks", "", "active", "2026-07-08T16:04:00Z", "2026-07-08T16:04:00Z"},
		},
		{
			query: `INSERT INTO issues (id, short_id, project_id, repository_id, worktree_id, scope_kind, issue_type, title, external_key, description, acceptance_criteria, status, priority, assignee, version, claimed_at, closed_at, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"issue-1", "afc-1", "proj-1", "repo-1", "wt-1", "worktree", "feature", "Prep export", "", "first issue", "", "done", 2, "", 1, nil, nil, "2026-07-08T16:05:00Z", "2026-07-08T16:05:00Z"},
		},
		{
			query: `INSERT INTO issues (id, short_id, project_id, repository_id, worktree_id, scope_kind, issue_type, title, external_key, description, acceptance_criteria, status, priority, assignee, version, claimed_at, closed_at, created_at, updated_at)
			        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"issue-2", "afc-2", "proj-1", "repo-1", "wt-1", "worktree", "feature", "Ship export", "bridge:42", "second issue", "emit jsonl", "in_progress", 2, "codex", 3, "2026-07-08T16:06:00Z", nil, "2026-07-08T16:06:00Z", "2026-07-08T16:06:30Z"},
		},
		{
			query: `INSERT INTO dependencies (issue_id, depends_on_issue_id, kind, created_at)
			        VALUES (?, ?, ?, ?)`,
			args: []any{"issue-2", "issue-1", "blocks", "2026-07-08T16:07:00Z"},
		},
		{
			query: `INSERT INTO issue_artifacts (issue_id, artifact_id, relation, created_at)
			        VALUES (?, ?, ?, ?)`,
			args: []any{"issue-2", "artifact-1", "implements", "2026-07-08T16:08:00Z"},
		},
		{
			query: `INSERT INTO notes (id, issue_id, author, body, created_at)
			        VALUES (?, ?, ?, ?, ?)`,
			args: []any{"note-1", "issue-2", "codex", "export in progress", "2026-07-08T16:09:00Z"},
		},
		{
			query: `INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
			        VALUES (?, ?, ?, ?, ?, ?)`,
			args: []any{"event-1", "issue-2", "codex", "issue_created", `{"title":"Ship export"}`, "2026-07-08T16:10:00Z"},
		},
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed fixture %q: %v", stmt.query, err)
		}
	}
}
