package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// UpsertWorktree creates or updates a worktree by absolute_path.
// If a worktree with the given absolute_path already exists, it updates the
// record and returns the existing ID. Otherwise it inserts a new row.
func UpsertWorktree(ctx context.Context, db *sql.DB, repoID string, req core.CreateWorktreeRequest) (core.Worktree, bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Check if a worktree with this absolute_path already exists.
	var existingID string
	err := db.QueryRowContext(ctx, `SELECT id FROM worktrees WHERE absolute_path = ?`, req.AbsolutePath).Scan(&existingID)
	isNew := false

	if err == sql.ErrNoRows {
		// Insert new.
		isNew = true
		existingID = uuid.New().String()
		_, err := db.ExecContext(ctx,
			`INSERT INTO worktrees (id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch, is_main, is_ephemeral, last_seen_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			existingID, repoID, req.AbsolutePath, req.Branch, req.HeadCommit,
			req.RemoteName, req.RemoteBranch, boolInt(req.IsMain), boolInt(req.IsEphemeral),
			now, now, now,
		)
		if err != nil {
			return core.Worktree{}, false, fmt.Errorf("insert worktree: %w", err)
		}
	} else if err != nil {
		return core.Worktree{}, false, fmt.Errorf("check existing worktree: %w", err)
	} else {
		// Update existing.
		_, err := db.ExecContext(ctx,
			`UPDATE worktrees SET
				repository_id = ?, branch = ?, head_commit = ?, remote_name = ?,
				remote_branch = ?, is_main = ?, is_ephemeral = ?, last_seen_at = ?, updated_at = ?
			 WHERE id = ?`,
			repoID, req.Branch, req.HeadCommit, req.RemoteName,
			req.RemoteBranch, boolInt(req.IsMain), boolInt(req.IsEphemeral),
			now, now, existingID,
		)
		if err != nil {
			return core.Worktree{}, false, fmt.Errorf("update worktree: %w", err)
		}
	}

	// Read back the full row.
	row := db.QueryRowContext(ctx,
		`SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch,
		        is_main, is_ephemeral, last_seen_at, created_at, updated_at
		 FROM worktrees WHERE id = ?`, existingID,
	)
	wt, err := scanWorktree(row)
	if err != nil {
		return core.Worktree{}, false, err
	}
	return wt, isNew, nil
}

// GetWorktree retrieves a worktree by ID.
func GetWorktree(ctx context.Context, db *sql.DB, id string) (core.Worktree, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch,
		        is_main, is_ephemeral, last_seen_at, created_at, updated_at
		 FROM worktrees WHERE id = ?`, id,
	)
	return scanWorktree(row)
}

// ListWorktrees lists worktrees, optionally filtered by repository ID.
func ListWorktrees(ctx context.Context, db *sql.DB, repoID string) ([]core.Worktree, error) {
	var rows *sql.Rows
	var err error

	if repoID != "" {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch,
			        is_main, is_ephemeral, last_seen_at, created_at, updated_at
			 FROM worktrees WHERE repository_id = ? ORDER BY absolute_path`, repoID,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch,
			        is_main, is_ephemeral, last_seen_at, created_at, updated_at
			 FROM worktrees ORDER BY absolute_path`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}
	defer rows.Close()

	var worktrees []core.Worktree
	for rows.Next() {
		wt, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		worktrees = append(worktrees, wt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate worktrees: %w", err)
	}
	if worktrees == nil {
		worktrees = []core.Worktree{}
	}
	return worktrees, nil
}

// DeleteWorktree removes a non-main worktree that is no longer referenced by
// issues or artifacts.
func DeleteWorktree(ctx context.Context, db *sql.DB, id string) (core.Worktree, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Worktree{}, fmt.Errorf("begin delete worktree tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`SELECT id, repository_id, absolute_path, branch, head_commit, remote_name, remote_branch,
		        is_main, is_ephemeral, last_seen_at, created_at, updated_at
		 FROM worktrees WHERE id = ?`, id,
	)
	wt, err := scanWorktree(row)
	if err != nil {
		return core.Worktree{}, err
	}

	if wt.IsMain {
		return core.Worktree{}, core.NewAPIError(core.ErrConflict, "main worktree cannot be unregistered")
	}

	var issueRefs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM issues WHERE worktree_id = ?`, id).Scan(&issueRefs); err != nil {
		return core.Worktree{}, fmt.Errorf("count issue worktree references: %w", err)
	}
	if issueRefs > 0 {
		return core.Worktree{}, core.NewAPIError(core.ErrConflict, "worktree is still referenced by issues")
	}

	var artifactRefs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM artifacts WHERE worktree_id = ?`, id).Scan(&artifactRefs); err != nil {
		return core.Worktree{}, fmt.Errorf("count artifact worktree references: %w", err)
	}
	if artifactRefs > 0 {
		return core.Worktree{}, core.NewAPIError(core.ErrConflict, "worktree is still referenced by artifacts")
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM worktrees WHERE id = ?`, id); err != nil {
		return core.Worktree{}, fmt.Errorf("delete worktree: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return core.Worktree{}, fmt.Errorf("commit delete worktree tx: %w", err)
	}

	return wt, nil
}

func scanWorktree(s scanner) (core.Worktree, error) {
	var wt core.Worktree
	var isMain, isEphemeral int
	err := s.Scan(&wt.ID, &wt.RepositoryID, &wt.AbsolutePath, &wt.Branch, &wt.HeadCommit,
		&wt.RemoteName, &wt.RemoteBranch, &isMain, &isEphemeral,
		&wt.LastSeenAt, &wt.CreatedAt, &wt.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Worktree{}, core.NewAPIError(core.ErrNotFound, "worktree not found")
		}
		return core.Worktree{}, fmt.Errorf("scan worktree: %w", err)
	}
	wt.IsMain = isMain != 0
	wt.IsEphemeral = isEphemeral != 0
	return wt, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
