package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// operationJournalDir is where afctl records an operation_id before sending
// the mutation it identifies.
//
// The failure this exists for is a lost response: the client sends a claim,
// the daemon commits it, and the reply never arrives (timeout, closed pipe,
// lost stdout). The operation_id is the only thing that can retrieve that
// committed outcome afterwards, so it must be durable BEFORE the request goes
// out — recording it only on success would lose it in exactly the case it is
// needed (AFC-SDD-0159 client persistence model).
const operationJournalDir = "~/.local/state/af-coordinator/operations"

func expandOperationJournalDir() (string, error) {
	dir := operationJournalDir
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, dir[2:])
	}
	return dir, nil
}

// journalOperationID writes the operation ID for a pending mutation. It
// returns the file path and whatever the file held before, so a caller that
// learns the new operation definitively did not commit can put the previous
// key back — overwriting it unconditionally would discard the recovery key of
// an earlier operation whose outcome is still unknown.
//
// The directory is 0700 and the file 0600: an operation_id can replay a claim
// outcome that contains a lease token, so it is a capability and never
// world-readable.
func journalOperationID(kind, target, operationID string) (string, string, error) {
	dir, err := expandOperationJournalDir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create operation journal: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.op", kind, sanitizeJournalName(target)))

	previous, _, err := readJournaledOperationID(kind, target)
	if err != nil {
		return "", "", err
	}

	// Write then fsync before the request is sent, so a crash between the
	// send and the reply cannot lose the key.
	if err := writeJournalFile(path, operationID); err != nil {
		return "", "", err
	}
	return path, previous, nil
}

func writeJournalFile(path, operationID string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write operation journal: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(operationID + "\n"); err != nil {
		return fmt.Errorf("write operation journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync operation journal: %w", err)
	}
	return nil
}

// restoreJournaledOperationID puts back a previously journaled key after an
// attempt that the daemon definitively rejected. A rejected request never
// committed, so its key is worthless — while the key it displaced may still be
// the only way to recover a committed operation.
func restoreJournaledOperationID(path, previous string) {
	if previous == "" {
		_ = os.Remove(path)
		return
	}
	_ = writeJournalFile(path, previous)
}

// readJournaledOperationID returns a previously recorded operation ID, if any.
func readJournaledOperationID(kind, target string) (string, string, error) {
	dir, err := expandOperationJournalDir()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.op", kind, sanitizeJournalName(target)))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", path, nil
	}
	if err != nil {
		return "", path, fmt.Errorf("read operation journal: %w", err)
	}
	return strings.TrimSpace(string(data)), path, nil
}

// sanitizeJournalName keeps a caller-supplied issue reference safe as a file
// name component; the journal path must not be steerable by its input.
func sanitizeJournalName(target string) string {
	var b strings.Builder
	for _, r := range target {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" {
		return "unknown"
	}
	return name
}

// newOperationID generates the opaque idempotency key. UUIDv4 matches the
// repository's existing identifier convention and carries enough entropy that
// it cannot be guessed by an unrelated worker.
func newOperationID() string {
	return uuid.New().String()
}
