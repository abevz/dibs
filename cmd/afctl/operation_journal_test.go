package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJournalOperationIDPersistsBeforeSend covers the property the journal
// exists for: the key is on disk, readable back, and 0600 because it can
// replay an outcome containing a lease token.
func TestJournalOperationIDPersistsBeforeSend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, previous, err := journalOperationID("claim", "demo-1", "op-first-0000-1111-2222-333344445555")
	if err != nil {
		t.Fatal(err)
	}
	if previous != "" {
		t.Errorf("previous = %q, want empty for a fresh journal", previous)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal mode = %o, want 600", perm)
	}

	got, _, err := readJournaledOperationID("claim", "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "op-first-0000-1111-2222-333344445555" {
		t.Errorf("journaled id = %q", got)
	}
}

// TestRejectedClaimRestoresPreviousOperationID is a regression test for a
// journal bug found during end-to-end validation: a later claim attempt that
// the daemon definitively rejected overwrote — and so destroyed — the
// operation ID of an earlier claim that had actually committed, leaving the
// committed outcome unrecoverable.
func TestRejectedClaimRestoresPreviousOperationID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const committed = "op-committed-1111-2222-3333-444455556666"
	const rejected = "op-rejected-9999-8888-7777-666655554444"

	if _, _, err := journalOperationID("claim", "demo-1", committed); err != nil {
		t.Fatal(err)
	}

	path, previous, err := journalOperationID("claim", "demo-1", rejected)
	if err != nil {
		t.Fatal(err)
	}
	if previous != committed {
		t.Fatalf("previous = %q, want the committed operation %q", previous, committed)
	}

	// The daemon answers with a typed rejection: the displaced key must return.
	restoreJournaledOperationID(path, previous)

	got, _, err := readJournaledOperationID("claim", "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != committed {
		t.Errorf("journaled id = %q, want the committed operation %q restored", got, committed)
	}
}

// TestRestoreRemovesJournalWhenNothingWasDisplaced keeps a rejected first
// attempt from leaving a useless key behind.
func TestRestoreRemovesJournalWhenNothingWasDisplaced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, previous, err := journalOperationID("claim", "demo-1", "op-only-0000-1111-2222-333344445555")
	if err != nil {
		t.Fatal(err)
	}
	restoreJournaledOperationID(path, previous)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("journal still present after restoring an empty predecessor: %v", err)
	}
}

// TestSanitizeJournalNameContainsPathTraversal keeps a caller-supplied issue
// reference from steering the journal path.
func TestSanitizeJournalNameContainsPathTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, _, err := journalOperationID("claim", "../../escape", "op-traversal-1111-2222-3333-444455556666")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := expandOperationJournalDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("journal escaped its directory: %s", path)
	}
}
