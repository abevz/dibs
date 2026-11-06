package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/migrations"
)

// Exercise the real migration runner, including its ledger, on the complete
// previous schema. Re-running the raw CREATE TABLE SQL is not the upgrade API.
func TestEvidenceMigrationPreservesLegacyDataAndRejectsOldBinary(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy := fstest.MapFS{}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "0012_issue_evidence.sql" {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		legacy[name] = &fstest.MapFile{Data: data}
	}
	if err = Migrate(ctx, db, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateProject(ctx, db, "legacy", "Legacy", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(ctx, db, "legacy", core.CreateIssueRequest{ScopeKind: "project", Title: "Existing work", Tags: []string{"queue/v2"}})
	if err != nil {
		t.Fatal(err)
	}
	// These SQL inserts only seed the isolated prior-schema fixture; current
	// GetIssue now requires migration 12, just as production startup does.
	const timestamp = "2026-11-01T00:00:00Z"
	if _, err = db.Exec(`INSERT INTO notes(id,issue_id,author,body,created_at) VALUES('legacy-note',?,'owner','HANDOFF: existing work',?)`, issue.ID, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err = ClaimIssue(ctx, db, issue.ID, "legacy-owner", 900); err != nil {
		t.Fatal(err)
	}
	before := legacyTableSnapshot(t, db, nil)
	if err = VerifyKnownMigrations(ctx, db, legacy); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	after := legacyTableSnapshot(t, db, before)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed legacy table definitions or rows")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM _migrations WHERE name='0012_issue_evidence.sql'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger count=%d err=%v", count, err)
	}
	// Both old and current VerifyKnownMigrations implement this filename check.
	// Supplying the prior binary's embedded FS models its startup refusal.
	if err = VerifyKnownMigrations(ctx, db, legacy); err == nil || !strings.Contains(err.Error(), `unknown applied migration "0012_issue_evidence.sql"`) {
		t.Fatalf("old binary accepted upgraded DB: %v", err)
	}
	if err = VerifyKnownMigrations(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	all := legacyTableSnapshot(t, db, nil)
	if err = Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("second startup: %v", err)
	}
	if again := legacyTableSnapshot(t, db, nil); !reflect.DeepEqual(all, again) {
		t.Fatal("repeat migration changed schema or data")
	}
	violations, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer violations.Close()
	if violations.Next() {
		t.Fatal("foreign key violation after migration")
	}
	if err = violations.Err(); err != nil {
		t.Fatal(err)
	}
}

// Snapshot all prior application tables and their definitions. The migration
// ledger is expected to gain exactly one row and is asserted separately.
func legacyTableSnapshot(t *testing.T, db *sql.DB, only map[string]string) map[string]string {
	t.Helper()
	result := map[string]string{}
	rows, err := db.Query(`SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err = rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		if only == nil {
			definitions[name] = definition
		} else if _, ok := only[name]; ok {
			definitions[name] = definition
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for name, definition := range definitions {
		r, err := db.Query(fmt.Sprintf(`SELECT * FROM "%s" ORDER BY rowid`, strings.ReplaceAll(name, `"`, `""`)))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := r.Columns()
		if err != nil {
			r.Close()
			t.Fatal(err)
		}
		data := [][]any{}
		for r.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = r.Scan(pointers...); err != nil {
				r.Close()
				t.Fatal(err)
			}
			data = append(data, values)
		}
		err = r.Err()
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = definition + "\n" + string(encoded)
	}
	return result
}
