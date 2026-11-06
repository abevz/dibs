package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/evidence"
	"github.com/google/uuid"
)

func (s *Store) RegisterEvidence(ctx context.Context, issueID string, req core.RegisterEvidenceRequest) (core.Evidence, error) {
	if err := core.ValidateRegisterEvidence(req); err != nil {
		return core.Evidence{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	var projectID string
	if err := s.db.QueryRowContext(ctx, `SELECT project_id FROM issues WHERE id=?`, issueID).Scan(&projectID); err != nil {
		return core.Evidence{}, err
	}
	e, err := evidence.Copy(req.SourcePath, projectID, issueID, uuid.NewString())
	if err != nil {
		return core.Evidence{}, core.NewAPIError(core.ErrValidationFailed, err.Error())
	}
	e.Kind = req.Kind
	e.CommitSHA = req.CommitSHA
	e.DiffHash = req.DiffHash
	e.Status = req.Status
	e.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `INSERT INTO issue_evidence(id,issue_id,kind,path,source_path,sha256,size,commit_sha,diff_hash,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, e.ID, e.IssueID, e.Kind, e.Path, e.SourcePath, e.SHA256, e.Size, e.CommitSHA, e.DiffHash, e.Status, e.CreatedAt)
	if err != nil {
		_ = evidence.Remove(e.Path)
		return core.Evidence{}, err
	}
	return e, nil
}

type evidenceQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listEvidence(ctx context.Context, q evidenceQuerier, issueID string) ([]core.Evidence, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,issue_id,kind,path,source_path,sha256,size,commit_sha,diff_hash,status,created_at,pruned_at FROM issue_evidence WHERE (?='' OR issue_id=?) ORDER BY created_at,id`, issueID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []core.Evidence{}
	for rows.Next() {
		var e core.Evidence
		if err = rows.Scan(&e.ID, &e.IssueID, &e.Kind, &e.Path, &e.SourcePath, &e.SHA256, &e.Size, &e.CommitSHA, &e.DiffHash, &e.Status, &e.CreatedAt, &e.PrunedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (s *Store) ListEvidence(ctx context.Context, issueID string) ([]core.Evidence, error) {
	return listEvidence(ctx, s.db, issueID)
}

func (s *Store) PruneEvidence(ctx context.Context, req core.EvidenceMaintenanceRequest) ([]core.Evidence, error) {
	if req.Days < 1 {
		return nil, core.NewAPIError(core.ErrValidationFailed, "days must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Acquire the SQLite writer lock before selecting terminal candidates. A
	// concurrent reopen cannot turn a candidate active during file deletion.
	if _, err = tx.ExecContext(ctx, `UPDATE issue_evidence SET pruned_at=pruned_at WHERE 0`); err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -req.Days).Format(time.RFC3339)
	rows, err := tx.QueryContext(ctx, `SELECT e.id FROM issue_evidence e JOIN issues i ON i.id=e.issue_id WHERE e.pruned_at='' AND i.status IN ('done','cancelled') AND i.closed_at < ? AND (?='' OR i.id=?)`, cutoff, req.IssueID, req.IssueID)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items, err := listEvidence(ctx, tx, req.IssueID)
	if err != nil {
		return nil, err
	}
	selected := []core.Evidence{}
	for _, e := range items {
		if !ids[e.ID] {
			continue
		}
		if req.Apply {
			if err = evidence.Remove(e.Path); err != nil {
				return nil, err
			}
			e.PrunedAt = time.Now().UTC().Format(time.RFC3339)
			if _, err = tx.ExecContext(ctx, `UPDATE issue_evidence SET pruned_at=? WHERE id=?`, e.PrunedAt, e.ID); err != nil {
				return nil, err
			}
		}
		selected = append(selected, e)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return selected, nil
}
