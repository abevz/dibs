package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/abevz/af-coordinator/internal/core"
)

// OperationRetention is how long a committed operation outcome stays
// replayable (AFC-SDD-0159 retention policy). It bounds how long a claim
// outcome — which contains a lease token — remains stored, while staying far
// longer than any realistic client retry window. Rows past retain_until are
// eligible for deletion; no automatic reaper runs yet, so growth is bounded by
// operation volume rather than by time.
const OperationRetention = 30 * 24 * time.Hour

// errOperationRace signals that another transaction committed this
// operation_id (or the underlying row) first. The caller retries once; the
// ledger lookup on that second attempt observes the winner and replays it.
var errOperationRace = errors.New("operation raced a concurrent commit")

// operationRecord is a committed ledger row: one client-identified logical
// mutation and the public outcome it produced.
type operationRecord struct {
	Kind        string
	TargetID    string
	Fingerprint string
	OutcomeJSON string
}

// lookupOperation reads a committed operation inside the caller's transaction.
//
// It deliberately runs before any state validation. A replay must return the
// original outcome even when current state has moved on — the lease may have
// been released or the issue closed since — because the caller is asking what
// its own committed operation did, not what is true now (AFC-SDD-0159).
func lookupOperation(ctx context.Context, tx *sql.Tx, operationID string) (*operationRecord, error) {
	var rec operationRecord
	err := tx.QueryRowContext(ctx,
		`SELECT operation_kind, target_id, fingerprint, outcome_json
		   FROM operations WHERE operation_id = ?`,
		operationID,
	).Scan(&rec.Kind, &rec.TargetID, &rec.Fingerprint, &rec.OutcomeJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup operation: %w", err)
	}
	return &rec, nil
}

// authorizeReplay decides whether a presented request may replay a stored
// operation. Replay requires knowledge of the operation_id AND an exact match
// of kind, target, and canonical fingerprint.
//
// Holder, session_id, and actor are NOT consulted: they are attribution, not
// authentication (AFC-SDD-0154), so they can neither grant nor deny replay on
// their own. They participate only inside the fingerprint, where their effect
// is to make replay stricter — a request that differs in holder is a different
// request and fails closed rather than replaying someone else's outcome.
func authorizeReplay(rec *operationRecord, kind, targetID, fingerprint string) error {
	if rec.Kind != kind {
		return core.NewAPIError(core.ErrIdempotencyConflict,
			"operation_id was already used for a different operation kind: "+rec.Kind)
	}
	if rec.TargetID != targetID {
		return core.NewAPIError(core.ErrIdempotencyConflict,
			"operation_id was already used for a different target")
	}
	if rec.Fingerprint != fingerprint {
		return core.NewAPIError(core.ErrIdempotencyConflict,
			"operation_id was already used with different request arguments")
	}
	return nil
}

// recordOperation writes the committed outcome inside the mutation's own
// transaction. There is no pending/in-flight ledger state by construction: if
// the surrounding transaction rolls back, this row disappears with the
// mutation it describes, so the ledger can never report an effect that did not
// commit (AFC-SDD-0159 crash model).
//
// A primary-key violation means a concurrent transaction committed this
// operation_id first; that surfaces as errOperationRace so the caller can
// retry into the replay path instead of producing a second outcome.
func recordOperation(ctx context.Context, tx *sql.Tx, operationID, kind, targetID, actor, fingerprint string, outcome any, now time.Time) error {
	payload, err := json.Marshal(outcome)
	if err != nil {
		return fmt.Errorf("marshal operation outcome: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO operations
		   (operation_id, operation_kind, target_id, actor, fingerprint, status, outcome_json, created_at, retain_until)
		 VALUES (?, ?, ?, ?, ?, 'completed', ?, ?, ?)`,
		operationID, kind, targetID, actor, fingerprint, string(payload),
		now.UTC().Format(time.RFC3339),
		now.UTC().Add(OperationRetention).Format(time.RFC3339),
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return errOperationRace
		}
		return fmt.Errorf("record operation: %w", err)
	}
	return nil
}

// decodeClaimOutcome rebuilds the original ClaimResponse from a stored
// outcome. The stored payload includes the lease token, which is why no
// list/read/diagnostic API reads this table: an exact replay is the only way
// to retrieve it.
func decodeClaimOutcome(rec *operationRecord) (core.ClaimResponse, error) {
	var resp core.ClaimResponse
	if err := json.Unmarshal([]byte(rec.OutcomeJSON), &resp); err != nil {
		return core.ClaimResponse{}, fmt.Errorf("decode claim outcome: %w", err)
	}
	return resp, nil
}
