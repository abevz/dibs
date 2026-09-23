package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
	_ "modernc.org/sqlite"
)

func TestOpenAppliesConnectionContractToEveryHandle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	const handles = 3
	dbs := make([]*sql.DB, 0, handles)
	for index := 0; index < handles; index++ {
		db, err := Open(dbPath)
		if err != nil {
			t.Fatalf("Open handle %d: %v", index, err)
		}
		dbs = append(dbs, db)
		t.Cleanup(func() { _ = db.Close() })
		if db.Stats().MaxOpenConnections != 1 {
			t.Fatalf("handle %d max open connections = %d, want 1", index, db.Stats().MaxOpenConnections)
		}
		if err := verifyConnectionSettings(context.Background(), db); err != nil {
			t.Fatalf("handle %d settings: %v", index, err)
		}
	}
	if err := Migrate(context.Background(), dbs[0], migrations.FS); err != nil {
		t.Fatal(err)
	}
	for index, db := range dbs {
		_, err := db.Exec(`INSERT INTO issue_tags (issue_id, tag, created_at) VALUES ('missing-issue', 'test/tag', '2026-07-13T20:00:00Z')`)
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("handle %d foreign key error = %v", index, err)
		}
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o, want 600", info.Mode().Perm())
	}
}

func TestOpenSerializesConcurrentClaimers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "One winner"})
	if err != nil {
		t.Fatal(err)
	}

	const claimants = 8
	start := make(chan struct{})
	results := make(chan error, claimants)
	var wg sync.WaitGroup
	for index := 0; index < claimants; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := ClaimIssue(context.Background(), db, issue.ID, fmt.Sprintf("worker-%d", index), 60)
			results <- err
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
			t.Fatalf("loser error = %v, want lease_held", err)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, want 1", winners)
	}
}

// newTestDB creates a temp-file-backed SQLite database with the schema applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "af-coordinator-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := f.Name()
	f.Close()
	_ = os.Remove(dbPath) // SQLite will keep the file handle open after open

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); os.Remove(dbPath) })

	// Enable foreign keys and set pragmas.
	for _, p := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Fatal(err)
		}
	}

	// Apply schema using real migrations.
	if err := Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}
	return db
}

func TestMigrateEventSequencePreservesLegacyOrder(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}

	legacyMigrations := embeddedMigrations(t, "0001_schema_v1.sql", "0002_issue_type.sql", "0003_acceptance_criteria.sql", "0004_issue_external_key.sql")
	if err := Migrate(context.Background(), db, legacyMigrations); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}

	const sameSecond = "2026-07-13T20:00:00Z"
	if _, err := db.Exec(`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at) VALUES
		('event-z', NULL, 'legacy', 'event_z', '{}', ?),
		('event-a', NULL, 'legacy', 'event_a', '{}', ?),
		('event-next', NULL, 'legacy', 'event_next', '{}', '2026-07-13T20:00:01Z')`, sameSecond, sameSecond); err != nil {
		t.Fatal(err)
	}

	sequenceMigration := embeddedMigrations(t, "0005_event_sequence.sql")
	if err := Migrate(context.Background(), db, sequenceMigration); err != nil {
		t.Fatalf("apply event sequence migration: %v", err)
	}

	rows, err := db.Query(`SELECT sequence, id, event_type FROM events ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []struct {
		sequence int64
		id       string
		event    string
	}
	for rows.Next() {
		var event struct {
			sequence int64
			id       string
			event    string
		}
		if err := rows.Scan(&event.sequence, &event.id, &event.event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		sequence int64
		id       string
		event    string
	}{
		{1, "event-a", "event_a"},
		{2, "event-z", "event_z"},
		{3, "event-next", "event_next"},
		{4, "", "event_ordering_enabled"},
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].sequence != want[i].sequence || got[i].event != want[i].event {
			t.Fatalf("event %d = %#v, want sequence=%d event=%q", i, got[i], want[i].sequence, want[i].event)
		}
		if want[i].id != "" && got[i].id != want[i].id {
			t.Fatalf("event %d id = %q, want %q", i, got[i].id, want[i].id)
		}
	}
}

func TestMigrateLeaseAttemptsBackfillsExistingLease(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}

	legacyMigrations := embeddedMigrations(t,
		"0001_schema_v1.sql", "0002_issue_type.sql", "0003_acceptance_criteria.sql",
		"0004_issue_external_key.sql", "0005_event_sequence.sql",
	)
	if err := Migrate(context.Background(), db, legacyMigrations); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Legacy lease",
	})
	if err != nil {
		t.Fatal(err)
	}
	const now = "2026-07-13T20:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO leases (issue_id, holder, lease_token, expires_at, created_at, updated_at)
		 VALUES (?, 'legacy-agent', 'legacy-token', '2026-07-13T21:00:00Z', ?, ?)`,
		issue.ID, now, now,
	); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(context.Background(), db, embeddedMigrations(t, "0006_lease_attempts.sql")); err != nil {
		t.Fatal(err)
	}
	var attemptID, sessionID string
	if err := db.QueryRow(`SELECT attempt_id, session_id FROM leases WHERE issue_id = ?`, issue.ID).Scan(&attemptID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if attemptID != "legacy-"+issue.ID || sessionID != "" {
		t.Fatalf("legacy lease telemetry = (%q, %q)", attemptID, sessionID)
	}
}

func TestMigrateLeaseGenerationBackfillsExistingLease(t *testing.T) {
	for _, tc := range []struct {
		name        string
		activeLease bool
		wantIssue   int64
		wantLease   int64
	}{
		{name: "without_active_lease", wantIssue: 0},
		{name: "with_active_lease", activeLease: true, wantIssue: 1, wantLease: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(1)
			if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
				t.Fatal(err)
			}

			legacyMigrations := embeddedMigrations(t,
				"0001_schema_v1.sql", "0002_issue_type.sql", "0003_acceptance_criteria.sql",
				"0004_issue_external_key.sql", "0005_event_sequence.sql",
				"0006_lease_attempts.sql", "0007_issue_tags.sql",
			)
			if err := Migrate(context.Background(), db, legacyMigrations); err != nil {
				t.Fatal(err)
			}
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
				ScopeKind: "project", Title: "Generation migration",
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.activeLease {
				const now = "2026-07-13T20:00:00Z"
				if _, err := db.Exec(
					`INSERT INTO leases (issue_id, holder, lease_token, expires_at, attempt_id, session_id, created_at, updated_at)
					 VALUES (?, 'legacy-agent', 'legacy-token', '2099-01-01T00:00:00Z', 'legacy-attempt', '', ?, ?)`,
					issue.ID, now, now,
				); err != nil {
					t.Fatal(err)
				}
			}

			if err := Migrate(context.Background(), db, embeddedMigrations(t, "0008_lease_generation.sql")); err != nil {
				t.Fatal(err)
			}
			var issueGeneration int64
			if err := db.QueryRow(`SELECT lease_generation FROM issues WHERE id = ?`, issue.ID).Scan(&issueGeneration); err != nil {
				t.Fatal(err)
			}
			if issueGeneration != tc.wantIssue {
				t.Fatalf("issue lease_generation = %d, want %d", issueGeneration, tc.wantIssue)
			}
			_, publicLease, err := GetIssue(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatalf("read upgraded issue: %v", err)
			}
			if tc.activeLease {
				var leaseGeneration int64
				if err := db.QueryRow(`SELECT lease_generation FROM leases WHERE issue_id = ?`, issue.ID).Scan(&leaseGeneration); err != nil {
					t.Fatal(err)
				}
				if leaseGeneration != tc.wantLease {
					t.Fatalf("lease lease_generation = %d, want %d", leaseGeneration, tc.wantLease)
				}
				if publicLease == nil || publicLease.LeaseGeneration != tc.wantLease {
					t.Fatalf("public upgraded lease = %+v, want generation %d", publicLease, tc.wantLease)
				}
			} else if publicLease != nil {
				t.Fatalf("unexpected lease after no-active-lease upgrade: %+v", publicLease)
			}
		})
	}
}

func embeddedMigrations(t *testing.T, names ...string) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	for _, name := range names {
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read embedded migration %s: %v", name, err)
		}
		result[name] = &fstest.MapFile{Data: data}
	}
	return result
}
