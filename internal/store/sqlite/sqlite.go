// Package sqlite implements SQLite-backed persistence for the coordinator.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteBusyTimeoutMillis = 5000

// Open opens (or creates) a SQLite database with one physical connection.
// The daemon's single sql.DB is therefore its serialized mutation boundary.
// Correctness-affecting PRAGMAs live in the DSN so the driver applies them to
// every physical connection, including any future pool-size increase.
func Open(dbPath string) (*sql.DB, error) {
	dsn, err := connectionDSN(dbPath)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect sqlite: %w", err)
	}
	if err := verifyConnectionSettings(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := VerifyIntegrity(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := restrictSQLiteFileModes(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// VerifyIntegrity fails startup when SQLite reports structural corruption.
func VerifyIntegrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("SQLite integrity check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	return nil
}

// VerifyKnownMigrations rejects a database written by a newer or different
// migration set before this binary can serve or alter it.
func VerifyKnownMigrations(ctx context.Context, db *sql.DB, migrationsFS fs.FS) error {
	entries, err := fs.Glob(migrationsFS, "*.sql")
	if err != nil {
		return fmt.Errorf("list migration files: %w", err)
	}
	known := make(map[string]bool, len(entries))
	for _, entry := range entries {
		known[entry] = true
	}
	var tableCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='_migrations'`).Scan(&tableCount); err != nil {
		return fmt.Errorf("check migration ledger: %w", err)
	}
	if tableCount == 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM _migrations`)
	if err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("read migration ledger: %w", err)
		}
		if !known[name] {
			return fmt.Errorf("unknown applied migration %q; use a compatible dibsd binary or restore a verified backup", name)
		}
	}
	return rows.Err()
}

func restrictSQLiteFileModes(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil {
			if os.IsNotExist(err) && path != dbPath {
				continue
			}
			return fmt.Errorf("chmod sqlite runtime file %s: %w", path, err)
		}
	}
	return nil
}

func connectionDSN(dbPath string) (string, error) {
	if dbPath == "" {
		return "", fmt.Errorf("sqlite database path is required")
	}
	if dbPath == ":memory:" {
		return "", fmt.Errorf("sqlite WAL contract requires a file-backed database")
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite path: %w", err)
	}
	u := url.URL{Scheme: "file", Path: absPath}
	query := u.Query()
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMillis))
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func verifyConnectionSettings(ctx context.Context, db *sql.DB) error {
	var journalMode string
	var foreignKeys, busyTimeout, synchronous int
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		return fmt.Errorf("read busy_timeout: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		return fmt.Errorf("read synchronous: %w", err)
	}
	if journalMode != "wal" || foreignKeys != 1 || busyTimeout != sqliteBusyTimeoutMillis || synchronous != 1 {
		return fmt.Errorf("unexpected sqlite settings: journal_mode=%s foreign_keys=%d busy_timeout=%d synchronous=%d",
			journalMode, foreignKeys, busyTimeout, synchronous)
	}
	return nil
}

// Migrate applies embedded SQL migration files that have not yet been applied.
// Migrations are sorted lexicographically and applied in a single transaction
// each. Applied migrations are tracked in the _migrations table.
func Migrate(ctx context.Context, db *sql.DB, migrationsFS fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _migrations (
		name       text primary key,
		applied_at text not null
	)`); err != nil {
		return fmt.Errorf("create _migrations table: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "*.sql")
	if err != nil {
		return fmt.Errorf("list migration files: %w", err)
	}
	sort.Strings(entries)

	for _, name := range entries {
		var already int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM _migrations WHERE name = ?", name).Scan(&already); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if already > 0 {
			continue
		}

		data, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, string(data)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO _migrations (name, applied_at) VALUES (?, ?)", name, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}
