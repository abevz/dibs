package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/abevz/dibs/internal/core"
)

// populateTags batch-loads issue_tags rows for the given issues and appends
// them into each issue's Tags, mirroring populateDependencies' batch-load
// shape.
func populateTags(ctx context.Context, db *sql.DB, issues []core.Issue) ([]core.Issue, error) {
	if len(issues) == 0 {
		return issues, nil
	}

	ids := make([]string, len(issues))
	idMap := make(map[string]int, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
		idMap[issue.ID] = i
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT issue_id, tag FROM issue_tags WHERE issue_id IN (%s) ORDER BY tag`,
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issueID, tag string
		if err := rows.Scan(&issueID, &tag); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		if idx, ok := idMap[issueID]; ok {
			issues[idx].Tags = append(issues[idx].Tags, tag)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tags: %w", err)
	}

	return issues, nil
}

// AddTag applies a namespaced tag to an issue, emitting an issue_tagged
// audit event.
func AddTag(ctx context.Context, db *sql.DB, issueID string, req core.AddTagRequest) error {
	if _, _, err := GetIssue(ctx, db, issueID); err != nil {
		return err
	}
	if err := core.ValidateTag(req.Tag); err != nil {
		return core.NewAPIError(core.ErrValidationFailed, err.Error())
	}

	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO issue_tags (issue_id, tag, created_at) VALUES (?, ?, ?)`,
		issueID, req.Tag, now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return core.NewAPIError(core.ErrAlreadyTagged, "tag already applied")
		}
		return fmt.Errorf("insert tag: %w", err)
	}

	if err := insertEvent(ctx, tx, issueID, req.Actor, "issue_tagged", map[string]any{"tag": req.Tag}, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// RemoveTag removes a tag from an issue, emitting an issue_untagged audit
// event.
func RemoveTag(ctx context.Context, db *sql.DB, issueID, tag, actor string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `DELETE FROM issue_tags WHERE issue_id = ? AND tag = ?`, issueID, tag)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return core.NewAPIError(core.ErrNotFound, "tag not found")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := insertEvent(ctx, tx, issueID, actor, "issue_untagged", map[string]any{"tag": tag}, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
