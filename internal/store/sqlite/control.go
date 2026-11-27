package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

const controlChangeOperation = "operator_control"
const controlAckOperation = "control_ack"

func (s *Store) ChangeControl(ctx context.Context, req core.ControlChangeRequest) (core.ControlChange, error) {
	if err := req.Normalize(); err != nil {
		return core.ControlChange{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.ControlChange{}, err
	}
	defer tx.Rollback()
	key := req.Kind + ":" + req.ID
	operationID := req.OperationID
	req.OperationID = ""
	fingerprint := requestFingerprint(req)
	if outcome, found, err := replayOperation[core.ControlChange](ctx, tx, operationID, controlChangeOperation, key, fingerprint); found || err != nil {
		return outcome, err
	}

	switch req.Kind {
	case "issue":
		req.ID, err = resolveIssueIDWith(ctx, tx, req.ID)
	case "repo":
		var repo core.Repository
		repo, err = getRepoWith(ctx, tx, req.ID)
		req.ID = repo.ID
	case "worktree":
		var wt controlWorktree
		wt, err = lookupControlWorktree(ctx, tx, req.ID)
		if err == nil && wt.id == "" {
			err = core.NewAPIError(core.ErrNotFound, "worktree target not found")
		}
		req.ID = wt.id
	}
	if err != nil {
		return core.ControlChange{}, err
	}

	now := time.Now().UTC()
	out := core.ControlChange{
		ControlRef: core.ControlRef{ControlTarget: req.ControlTarget},
		Action:     req.Action, Reason: req.Reason, SetBy: "operator", SetAt: now.Format(time.RFC3339),
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO control_changes(target_kind, target_id, action, reason, actor, set_at) VALUES (?, ?, ?, ?, ?, ?)`,
		req.Kind, req.ID, req.Action, req.Reason, req.Actor, out.SetAt)
	if err != nil {
		return core.ControlChange{}, err
	}
	out.Revision, err = result.LastInsertId()
	if err != nil {
		return core.ControlChange{}, err
	}
	if operationID != "" {
		if err := recordOperation(ctx, tx, operationID, controlChangeOperation, key, req.Actor, fingerprint, out, now); err != nil {
			return core.ControlChange{}, err
		}
	}
	return out, tx.Commit()
}

type queryContext interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type controlWorktree struct {
	id, repo, path, branch string
}

func lookupControlWorktree(ctx context.Context, db queryContext, ref string) (controlWorktree, error) {
	if ref == "" {
		return controlWorktree{}, nil
	}
	// Preserve registered aliases for existing worktrees while matching the
	// canonical root supplied by the hook. Treat duplicate aliases as ambiguous.
	if !filepath.IsAbs(ref) {
		var wt controlWorktree
		err := db.QueryRowContext(ctx, `SELECT id, repository_id, absolute_path, branch FROM worktrees WHERE id = ?`, ref).
			Scan(&wt.id, &wt.repo, &wt.path, &wt.branch)
		if errors.Is(err, sql.ErrNoRows) {
			return controlWorktree{}, nil
		}
		return wt, err
	}

	root, err := filepath.EvalSymlinks(ref)
	if err != nil {
		root = filepath.Clean(ref)
	}
	rows, err := db.QueryContext(ctx, `SELECT id, repository_id, absolute_path, branch FROM worktrees`)
	if err != nil {
		return controlWorktree{}, err
	}
	defer rows.Close()

	var match controlWorktree
	for rows.Next() {
		var wt controlWorktree
		if err := rows.Scan(&wt.id, &wt.repo, &wt.path, &wt.branch); err != nil {
			return controlWorktree{}, err
		}
		path, err := filepath.EvalSymlinks(wt.path)
		if err != nil {
			path = filepath.Clean(wt.path)
		}
		if path == root {
			if match.id != "" {
				return controlWorktree{}, core.NewAPIError(core.ErrValidationFailed, "ambiguous worktree path")
			}
			match = wt
		}
	}
	return match, rows.Err()
}

func controlTargets(ctx context.Context, tx *sql.Tx, issueID string, generation int64) ([]core.ControlTarget, error) {
	targets := []core.ControlTarget{{Kind: "global"}}
	add := func(kind, id string) {
		target := core.ControlTarget{Kind: kind, ID: id}
		if id != "" && !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	if issueID != "" {
		var issueRepo, issueWorktree, bound, boundRepo string
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(i.repository_id,''), COALESCE(i.worktree_id,''),
			       COALESCE(b.worktree_id,''), COALESCE(w.repository_id,'')
			FROM issues i
			LEFT JOIN control_bindings b ON b.issue_id=i.id AND b.lease_generation=?
			LEFT JOIN worktrees w ON w.id=b.worktree_id
			WHERE i.id=?`, generation, issueID).Scan(&issueRepo, &issueWorktree, &bound, &boundRepo)
		if err != nil {
			return nil, err
		}
		if bound != "" && boundRepo == "" {
			return nil, core.NewAPIError(core.ErrValidationFailed, "bound worktree is no longer registered")
		}
		add("issue", issueID)
		add("repo", issueRepo)
		add("worktree", issueWorktree)
		add("repo", boundRepo)
		add("worktree", bound)
	}
	return targets, nil
}

func scopedControlTargets(bound []core.ControlTarget, actual controlWorktree) []core.ControlTarget {
	targets := slices.Clone(bound)
	for _, target := range []core.ControlTarget{{Kind: "repo", ID: actual.repo}, {Kind: "worktree", ID: actual.id}} {
		if target.ID != "" && !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	slices.SortFunc(targets, core.CompareControlTargets)
	return targets
}

func effectiveControl(ctx context.Context, tx *sql.Tx, targets []core.ControlTarget) (core.ControlSignal, error) {
	var effective core.ControlSignal
	for _, target := range targets {
		ref := core.ControlRef{ControlTarget: target}
		signal := core.ControlSignal{Ref: &ref, SetBy: "operator"}
		err := tx.QueryRowContext(ctx, `SELECT revision, action, reason, set_at FROM control_changes
			WHERE target_kind=? AND target_id=? ORDER BY revision DESC LIMIT 1`, target.Kind, target.ID).
			Scan(&ref.Revision, &signal.Control, &signal.Reason, &signal.SetAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return core.ControlSignal{}, err
		}
		if core.ControlSeverity(signal.Control) > core.ControlSeverity(effective.Control) {
			effective = signal
		}
	}
	return effective, nil
}

func controlLease(ctx context.Context, tx *sql.Tx, issueID, token string, generation int64, now time.Time) (string, error) {
	var holder string
	err := tx.QueryRowContext(ctx, `SELECT holder FROM leases WHERE issue_id=? AND lease_token=? AND lease_generation=? AND expires_at>?`,
		issueID, token, generation, now.Format(time.RFC3339)).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", controlOwnershipError(ctx, tx, issueID, token, generation)
	}
	return holder, err
}

func controlOwnershipError(ctx context.Context, tx *sql.Tx, issueID, token string, generation int64) error {
	var retired bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM control_retired_attempts WHERE issue_id=? AND lease_generation=?)`,
		issueID, generation).Scan(&retired); err != nil {
		return err
	}
	if retired {
		return core.NewAPIError(core.ErrControlStopped, "attempt stopped by operator")
	}
	return leaseOwnershipError(ctx, tx, issueID, token, generation)
}

func controlContext(ctx context.Context, tx *sql.Tx, req core.ControlStatusRequest, now time.Time) (string, string, controlWorktree, error) {
	var id, holder string
	if req.IssueID != "" {
		if req.LeaseToken == "" || req.LeaseGeneration <= 0 {
			return "", "", controlWorktree{}, core.NewAPIError(core.ErrValidationFailed, "claimed control lookup requires token and generation")
		}
		var err error
		id, err = resolveIssueIDWith(ctx, tx, req.IssueID)
		if err != nil {
			return "", "", controlWorktree{}, err
		}
		holder, err = controlLease(ctx, tx, id, req.LeaseToken, req.LeaseGeneration, now)
		if err != nil {
			return "", "", controlWorktree{}, err
		}
	} else if req.LeaseToken != "" || req.LeaseGeneration != 0 {
		return "", "", controlWorktree{}, core.NewAPIError(core.ErrValidationFailed, "issue_id is required for a lease")
	}
	if req.WorktreePath != "" && !filepath.IsAbs(req.WorktreePath) {
		return "", "", controlWorktree{}, core.NewAPIError(core.ErrValidationFailed, "worktree_path must be absolute")
	}

	actual, err := lookupControlWorktree(ctx, tx, req.WorktreePath)
	if err == nil && id == "" && actual.id == "" {
		err = core.NewAPIError(core.ErrValidationFailed, "registered checkout is required without an issue")
	}
	return id, holder, actual, err
}

func (s *Store) ControlStatus(ctx context.Context, req core.ControlStatusRequest, enroll bool) (core.ControlStatus, error) {
	if len(req.Observed) > 64 {
		return core.ControlStatus{}, core.NewAPIError(core.ErrValidationFailed, "too many observed targets")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.ControlStatus{}, err
	}
	defer tx.Rollback()
	id, _, actual, err := controlContext(ctx, tx, req, time.Now().UTC())
	if err != nil {
		return core.ControlStatus{}, err
	}
	if enroll {
		if id == "" || actual.id == "" {
			return core.ControlStatus{}, core.NewAPIError(core.ErrValidationFailed, "enrollment requires a claim and registered checkout")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO control_bindings(issue_id, lease_generation, worktree_id)
			VALUES (?, ?, ?) ON CONFLICT(issue_id, lease_generation) DO NOTHING`, id, req.LeaseGeneration, actual.id)
		if err != nil {
			return core.ControlStatus{}, err
		}
		var bound string
		if err := tx.QueryRowContext(ctx, `SELECT worktree_id FROM control_bindings WHERE issue_id=? AND lease_generation=?`,
			id, req.LeaseGeneration).Scan(&bound); err != nil {
			return core.ControlStatus{}, err
		}
		if bound != actual.id {
			return core.ControlStatus{}, core.NewAPIError(core.ErrConflict, "lease is already bound to another checkout")
		}
	}

	bound, err := controlTargets(ctx, tx, id, req.LeaseGeneration)
	if err != nil {
		return core.ControlStatus{}, err
	}
	targets := scopedControlTargets(bound, actual)
	signal, err := effectiveControl(ctx, tx, targets)
	if err != nil {
		return core.ControlStatus{}, err
	}
	out := core.ControlStatus{
		ControlSignal: signal, Targets: targets, BoundTargets: bound,
		WorktreeID: actual.id, RepositoryID: actual.repo,
	}
	if id != "" {
		if err := tx.QueryRowContext(ctx, `SELECT expires_at, heartbeat_count FROM leases WHERE issue_id=?`, id).Scan(&out.ExpiresAt, &out.HeartbeatCount); err != nil {
			return core.ControlStatus{}, err
		}
	}
	for _, observed := range req.Observed {
		if !slices.Contains(targets, observed.ControlTarget) {
			continue
		}
		ref := core.ControlRef{ControlTarget: observed.ControlTarget}
		err := tx.QueryRowContext(ctx, `SELECT revision FROM control_changes
			WHERE target_kind=? AND target_id=? AND action='resume' AND revision>? ORDER BY revision DESC LIMIT 1`,
			ref.Kind, ref.ID, observed.Revision).Scan(&ref.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return core.ControlStatus{}, err
		}
		out.Resumes = append(out.Resumes, ref)
	}
	return out, tx.Commit()
}

func (s *Store) AcknowledgeControl(ctx context.Context, req core.ControlAckRequest) (core.ControlAck, error) {
	if req.IssueID == "" || req.LeaseGeneration <= 0 || req.LeaseToken == "" || req.Revision <= 0 || len(req.Observed) != 0 {
		return core.ControlAck{}, core.NewAPIError(core.ErrValidationFailed, "acknowledgement requires a lease and control revision")
	}
	if err := core.ValidateOperationID(req.OperationID); err != nil {
		return core.ControlAck{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.ControlAck{}, err
	}
	defer tx.Rollback()
	id, err := resolveIssueIDWith(ctx, tx, req.IssueID)
	if err != nil {
		return core.ControlAck{}, err
	}
	opID := req.OperationID
	req.OperationID = ""
	req.IssueID = id
	fingerprint := requestFingerprint(req)
	if out, found, err := replayOperation[core.ControlAck](ctx, tx, opID, controlAckOperation, id, fingerprint); found || err != nil {
		return out, err
	}

	now := time.Now().UTC()
	_, holder, actual, err := controlContext(ctx, tx, req.ControlStatusRequest, now)
	if err != nil {
		return core.ControlAck{}, err
	}
	targets, err := controlTargets(ctx, tx, id, req.LeaseGeneration)
	if err != nil {
		return core.ControlAck{}, err
	}
	signal, err := effectiveControl(ctx, tx, scopedControlTargets(targets, actual))
	if err != nil {
		return core.ControlAck{}, err
	}
	if signal.Ref == nil || signal.Ref.Revision != req.Revision {
		return core.ControlAck{}, core.NewAPIError(core.ErrControlChanged, "effective control changed")
	}

	out := core.ControlAck{State: signal.Control}
	switch signal.Control {
	case core.ControlStop:
		out.State = "stop_pending"
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO control_observations(issue_id, lease_generation, revision) VALUES (?, ?, ?)`,
			id, req.LeaseGeneration, req.Revision)
		if err != nil {
			return core.ControlAck{}, err
		}
		if req.Quiescent {
			note := "HANDOFF: stopped by operator (" + signal.Reason + ")"
			if actual.id != "" {
				note += "\nWorktree " + strconv.Quote(actual.path) + ", branch " + strconv.Quote(actual.branch) + "."
			}
			handoff := core.HandoffRequest{LeaseToken: req.LeaseToken, LeaseGeneration: req.LeaseGeneration, Note: note}
			if _, err := handoffLeaseInTx(ctx, tx, id, handoff, "unknown", now, "operator_stop"); err != nil {
				return core.ControlAck{}, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO control_retired_attempts(issue_id, lease_generation) VALUES (?, ?)`,
				id, req.LeaseGeneration); err != nil {
				return core.ControlAck{}, err
			}
			out.State = "stopped"
		}
	case core.ControlPause, core.ControlAskOwner:
		if err := acknowledgeControlNote(ctx, tx, id, req, signal.Control, holder, now); err != nil {
			return core.ControlAck{}, err
		}
	}
	if opID != "" {
		if err := recordOperation(ctx, tx, opID, controlAckOperation, id, holder, fingerprint, out, now); err != nil {
			return core.ControlAck{}, err
		}
	}
	return out, tx.Commit()
}

func acknowledgeControlNote(ctx context.Context, tx *sql.Tx, id string, req core.ControlAckRequest, action, holder string, now time.Time) error {
	rec, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO control_observations(issue_id, lease_generation, revision) VALUES (?, ?, ?)`,
		id, req.LeaseGeneration, req.Revision)
	if err != nil {
		return err
	}
	inserted, err := rec.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}

	stamp := now.Format(time.RFC3339)
	body := "paused at " + stamp
	if action == core.ControlAskOwner {
		body = "owner attention requested at " + stamp
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notes(id, issue_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), id, holder, body, stamp); err != nil {
		return err
	}
	return insertEvent(ctx, tx, id, holder, "note_added", map[string]any{
		"invocation_mode": "unknown", "control_revision": req.Revision,
	}, stamp)
}

func checkControlClose(ctx context.Context, tx *sql.Tx, issueID string, generation int64) error {
	var enrolled bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM control_bindings WHERE issue_id=? AND lease_generation=?)`,
		issueID, generation).Scan(&enrolled); err != nil || !enrolled {
		return err
	}
	targets, err := controlTargets(ctx, tx, issueID, generation)
	if err != nil {
		return err
	}
	signal, err := effectiveControl(ctx, tx, scopedControlTargets(targets, controlWorktree{}))
	if err != nil {
		return err
	}
	if signal.Ref != nil {
		return core.NewAPIError(core.ErrControlRestricted, fmt.Sprintf("attempt is restricted by operator control (%s)", signal.Control))
	}
	// A hook may have seen a restriction in a second checkout. Keep
	// finalization gated until the corresponding source has been resumed.
	var observed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM control_observations o JOIN control_changes c ON c.revision=o.revision
		WHERE o.issue_id=? AND o.lease_generation=? AND NOT EXISTS (
			SELECT 1 FROM control_changes r WHERE r.target_kind=c.target_kind AND r.target_id=c.target_id
			AND r.action='resume' AND r.revision>c.revision))`, issueID, generation).Scan(&observed)
	if err != nil {
		return err
	}
	if observed {
		return core.NewAPIError(core.ErrControlRestricted, "attempt has an observed operator restriction awaiting resume")
	}
	return nil
}
