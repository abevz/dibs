package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/abevz/dibs/internal/core"
)

// recordStaleRejection runs after the rejected mutation's transaction has
// rolled back. A telemetry failure never changes the original API result.
func (s *Store) recordStaleRejection(issueID, kind, holder, mode string, generation int64, mutationErr error) {
	var apiErr core.APIError
	if !errors.As(mutationErr, &apiErr) ||
		(apiErr.Code != core.ErrLeaseExpired && apiErr.Code != core.ErrConflict &&
			apiErr.Code != core.ErrLeaseHeld && apiErr.Code != core.ErrIssueNotReady) {
		return
	}
	if holder == "" {
		holder = "unknown"
	}
	if mode == "" {
		mode = core.InvocationModeUnknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.upsertStaleRejection(ctx, issueID, kind, holder, apiErr.Code, mode, generation); err != nil {
		slog.Warn("record stale rejection failed", "kind", kind, "reason_code", apiErr.Code, "error", err)
	}
}

func (s *Store) upsertStaleRejection(ctx context.Context, issueID, kind, holder, reason, mode string, generation int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rejection audit: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT lease_generation FROM issues WHERE id = ?`, issueID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("read current generation: %w", err)
	}
	if kind != "claim" && generation > 0 {
		// Lifecycle requests do not carry a trusted holder field. Resolve the
		// presented generation to its original claim actor, even after reclaim.
		var originalHolder string
		err := tx.QueryRowContext(ctx, `SELECT actor FROM events
			WHERE issue_id = ? AND event_type = 'issue_claimed'
			  AND CAST(json_extract(payload_json, '$.lease_generation') AS INTEGER) = ?
			ORDER BY sequence DESC LIMIT 1`, issueID, generation).Scan(&originalHolder)
		if err == nil {
			holder = originalHolder
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read original claim holder: %w", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO rejection_counts
		(issue_id, kind, holder, reason_code, count, first_seen_at, last_seen_at,
		 last_presented_generation, current_generation_at_last_rejection, last_invocation_mode)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(issue_id, kind, holder, reason_code) DO UPDATE SET
		 count = count + 1, last_seen_at = excluded.last_seen_at,
		 last_presented_generation = excluded.last_presented_generation,
		 current_generation_at_last_rejection = excluded.current_generation_at_last_rejection,
		 last_invocation_mode = excluded.last_invocation_mode`,
		issueID, kind, holder, reason, now, now, generation, current, mode)
	if err != nil {
		return fmt.Errorf("upsert rejection audit: %w", err)
	}
	return tx.Commit()
}

// SafetySnapshot derives current lease state from SQLite and returns a bounded
// view of durable rejection counters. It never reads lease tokens.
func (s *Store) SafetySnapshot(ctx context.Context, now time.Time) (core.SafetySnapshot, error) {
	var out core.SafetySnapshot
	rows := s.db.QueryRowContext(ctx, `SELECT
		coalesce(sum(case when expires_at > ? then 1 else 0 end), 0),
		coalesce(sum(case when expires_at <= ? then 1 else 0 end), 0)
		FROM leases`, now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	if err := rows.Scan(&out.ActiveLeases, &out.ExpiredLeases); err != nil {
		return out, fmt.Errorf("count leases: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(count), 0) FROM rejection_counts
		WHERE reason_code IN ('lease_expired', 'version_conflict')`).Scan(&out.StaleRejections); err != nil {
		return out, fmt.Errorf("count stale rejections: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(count), 0) FROM rejection_counts
		WHERE kind = 'claim' AND reason_code IN ('lease_held', 'issue_not_ready')`).Scan(&out.ClaimConflicts); err != nil {
		return out, fmt.Errorf("count claim conflicts: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(name), '') FROM _migrations`).Scan(&out.LatestMigration); err != nil {
		return out, fmt.Errorf("read latest migration: %w", err)
	}
	entries, err := s.db.QueryContext(ctx, `SELECT issue_id, kind, holder, reason_code, count,
		first_seen_at, last_seen_at, last_presented_generation,
		current_generation_at_last_rejection, last_invocation_mode
		FROM rejection_counts WHERE reason_code IN ('lease_expired', 'version_conflict')
		ORDER BY count DESC, last_seen_at DESC, issue_id LIMIT 10`)
	if err != nil {
		return out, fmt.Errorf("list stale holders: %w", err)
	}
	defer entries.Close()
	out.TopStaleHolders = make([]core.StaleRejectionCount, 0)
	for entries.Next() {
		var entry core.StaleRejectionCount
		if err := entries.Scan(&entry.IssueID, &entry.Kind, &entry.Holder, &entry.ReasonCode,
			&entry.Count, &entry.FirstSeenAt, &entry.LastSeenAt,
			&entry.LastPresentedGeneration, &entry.CurrentGenerationAtLastRejection,
			&entry.LastInvocationMode); err != nil {
			return out, fmt.Errorf("scan stale holder: %w", err)
		}
		out.TopStaleHolders = append(out.TopStaleHolders, entry)
	}
	return out, entries.Err()
}
