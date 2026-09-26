package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// RelocateRepo atomically changes the repository and all its worktree paths.
// Filesystem identity is checked by the API before this transaction; expected
// paths protect that check against concurrent registration changes.
func RelocateRepo(ctx context.Context, db *sql.DB, repoID, expectedPath string, req core.RelocateRepoRequest, changes []core.WorktreePathChange) (core.RelocateRepoResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		result, err := relocateRepoOnce(ctx, db, repoID, expectedPath, req, changes)
		if err != errOperationRace {
			return result, err
		}
	}
	return core.RelocateRepoResult{}, fmt.Errorf("relocate operation raced repeatedly")
}

// ReplayRepoRelocate checks an already committed result before filesystem
// validation. A later move may have removed the original destination path.
func ReplayRepoRelocate(ctx context.Context, db *sql.DB, repoID string, req core.RelocateRepoRequest) (core.RelocateRepoResult, bool, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return core.RelocateRepoResult{}, false, err
	}
	defer tx.Rollback()
	fingerprintReq := req
	fingerprintReq.OperationID = ""
	return replayOperation[core.RelocateRepoResult](ctx, tx, req.OperationID, core.OperationKindRepoRelocate, repoID, requestFingerprint(fingerprintReq))
}

func relocateRepoOnce(ctx context.Context, db *sql.DB, repoID, expectedPath string, req core.RelocateRepoRequest, changes []core.WorktreePathChange) (core.RelocateRepoResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.RelocateRepoResult{}, fmt.Errorf("begin relocate tx: %w", err)
	}
	defer tx.Rollback()
	fingerprintReq := req
	fingerprintReq.OperationID = ""
	fingerprint := requestFingerprint(fingerprintReq)
	if replay, ok, err := replayOperation[core.RelocateRepoResult](ctx, tx, req.OperationID, core.OperationKindRepoRelocate, repoID, fingerprint); err != nil || ok {
		return replay, err
	}
	row := tx.QueryRowContext(ctx, `SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at FROM repositories WHERE id = ?`, repoID)
	repo, err := scanRepoFields(row)
	if err != nil {
		return core.RelocateRepoResult{}, err
	}
	if repo.CanonicalGitDir != expectedPath {
		return core.RelocateRepoResult{}, core.NewAPIError(core.ErrConflict, "repository path changed; retry with current registration")
	}
	var otherRepo string
	err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE canonical_git_dir = ? AND id != ?`, req.NewCanonicalGitDir, repoID).Scan(&otherRepo)
	if err == nil {
		return core.RelocateRepoResult{}, core.NewAPIError(core.ErrConflict, "new repository path is already registered")
	}
	if err != sql.ErrNoRows {
		return core.RelocateRepoResult{}, fmt.Errorf("check repository target path: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch, is_main, is_ephemeral, last_seen_at, created_at, updated_at FROM worktrees WHERE repository_id = ? ORDER BY absolute_path`, repoID)
	if err != nil {
		return core.RelocateRepoResult{}, fmt.Errorf("list worktrees for relocate: %w", err)
	}
	current := make(map[string]core.Worktree)
	for rows.Next() {
		wt, scanErr := scanWorktree(rows)
		if scanErr != nil {
			rows.Close()
			return core.RelocateRepoResult{}, scanErr
		}
		current[wt.ID] = wt
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return core.RelocateRepoResult{}, err
	}
	rows.Close()
	if len(current) != len(changes) {
		return core.RelocateRepoResult{}, core.NewAPIError(core.ErrConflict, "worktree registrations changed; retry")
	}
	seen := make(map[string]bool)
	for _, change := range changes {
		wt, ok := current[change.ID]
		if !ok || wt.AbsolutePath != change.OldPath || seen[change.ID] {
			return core.RelocateRepoResult{}, core.NewAPIError(core.ErrConflict, "worktree registrations changed; retry")
		}
		seen[change.ID] = true
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT id FROM worktrees WHERE absolute_path = ? AND id != ?`, change.NewPath, change.ID).Scan(&owner)
		if err == nil {
			return core.RelocateRepoResult{}, core.NewAPIError(core.ErrConflict, "new worktree path is already registered: "+change.NewPath)
		}
		if err != sql.ErrNoRows {
			return core.RelocateRepoResult{}, fmt.Errorf("check target path: %w", err)
		}
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE repositories SET canonical_git_dir = ?, updated_at = ? WHERE id = ?`, req.NewCanonicalGitDir, stamp, repoID); err != nil {
		return core.RelocateRepoResult{}, fmt.Errorf("update repository path: %w", err)
	}
	repo.CanonicalGitDir = req.NewCanonicalGitDir
	repo.UpdatedAt = stamp
	updated := make([]core.Worktree, 0, len(changes))
	for _, change := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE worktrees SET absolute_path = ?, updated_at = ? WHERE id = ?`, change.NewPath, stamp, change.ID); err != nil {
			return core.RelocateRepoResult{}, fmt.Errorf("update worktree path: %w", err)
		}
		wt := current[change.ID]
		wt.AbsolutePath = change.NewPath
		wt.UpdatedAt = stamp
		updated = append(updated, wt)
	}
	result := core.RelocateRepoResult{OperationID: req.OperationID, Repository: repo, Worktrees: updated}
	if expectedPath != req.NewCanonicalGitDir {
		payload, err := json.Marshal(map[string]any{"repository_id": repoID, "old_path": expectedPath, "new_path": req.NewCanonicalGitDir, "worktrees": changes})
		if err != nil {
			return core.RelocateRepoResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events (id, actor, event_type, payload_json, created_at) VALUES (?, ?, 'repo_relocated', ?, ?)`, uuid.NewString(), req.Actor, string(payload), stamp); err != nil {
			return core.RelocateRepoResult{}, fmt.Errorf("insert relocate event: %w", err)
		}
	}
	if err := recordOperation(ctx, tx, req.OperationID, core.OperationKindRepoRelocate, repoID, req.Actor, fingerprint, result, now); err != nil {
		return core.RelocateRepoResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.RelocateRepoResult{}, fmt.Errorf("commit relocate tx: %w", err)
	}
	return result, nil
}
