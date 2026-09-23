package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/abevz/dibs/internal/core"
	coordinatorexport "github.com/abevz/dibs/internal/export"
)

func (s *Store) ListRepoRemotes(ctx context.Context) ([]core.RepoRemote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repository_id, remote_name, fetch_url, push_url, is_primary, created_at, updated_at
		 FROM repo_remotes
		 ORDER BY repository_id ASC, remote_name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query repo remotes: %w", err)
	}
	defer rows.Close()

	remotes := []core.RepoRemote{}
	for rows.Next() {
		var remote core.RepoRemote
		var isPrimary int
		if err := rows.Scan(
			&remote.ID,
			&remote.RepositoryID,
			&remote.RemoteName,
			&remote.FetchURL,
			&remote.PushURL,
			&isPrimary,
			&remote.CreatedAt,
			&remote.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan repo remote: %w", err)
		}
		remote.IsPrimary = isPrimary != 0
		remotes = append(remotes, remote)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repo remotes: %w", err)
	}
	return remotes, nil
}

func (s *Store) ListExportDependencies(ctx context.Context) ([]coordinatorexport.Dependency, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source.id, source.short_id, target.id, target.short_id, d.kind, d.created_at
		 FROM dependencies d
		 JOIN issues source ON source.id = d.issue_id
		 JOIN issues target ON target.id = d.depends_on_issue_id
		 ORDER BY d.created_at ASC, source.short_id ASC, target.short_id ASC, d.kind ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query dependencies: %w", err)
	}
	defer rows.Close()

	dependencies := []coordinatorexport.Dependency{}
	for rows.Next() {
		var dependency coordinatorexport.Dependency
		if err := rows.Scan(
			&dependency.IssueID,
			&dependency.IssueShortID,
			&dependency.DependsOnID,
			&dependency.DependsOnShortID,
			&dependency.Kind,
			&dependency.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		dependencies = append(dependencies, dependency)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dependencies: %w", err)
	}
	return dependencies, nil
}

func (s *Store) ListReferences(ctx context.Context) ([]coordinatorexport.Reference, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT i.id, i.short_id, a.id, a.relative_path, a.kind, ia.relation, ia.created_at
		 FROM issue_artifacts ia
		 JOIN issues i ON i.id = ia.issue_id
		 JOIN artifacts a ON a.id = ia.artifact_id
		 ORDER BY ia.created_at ASC, i.short_id ASC, a.relative_path ASC, ia.relation ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query references: %w", err)
	}
	defer rows.Close()

	references := []coordinatorexport.Reference{}
	for rows.Next() {
		var reference coordinatorexport.Reference
		if err := rows.Scan(
			&reference.IssueID,
			&reference.IssueShortID,
			&reference.ArtifactID,
			&reference.ArtifactPath,
			&reference.ArtifactKind,
			&reference.Relation,
			&reference.LinkCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan reference: %w", err)
		}
		references = append(references, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate references: %w", err)
	}
	return references, nil
}

func (s *Store) ListAllNotes(ctx context.Context) ([]core.Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, issue_id, author, body, created_at
		 FROM notes
		 ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	notes := []core.Note{}
	for rows.Next() {
		var note core.Note
		if err := rows.Scan(&note.ID, &note.IssueID, &note.Author, &note.Body, &note.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notes: %w", err)
	}
	return notes, nil
}

func (s *Store) ListAllEvents(ctx context.Context) ([]core.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sequence, id, issue_id, actor, event_type, payload_json, created_at
		 FROM events
		 ORDER BY sequence ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := []core.Event{}
	for rows.Next() {
		var event core.Event
		var issueID sql.NullString
		if err := rows.Scan(
			&event.Sequence,
			&event.ID,
			&issueID,
			&event.Actor,
			&event.EventType,
			&event.PayloadJSON,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if issueID.Valid {
			event.IssueID = issueID.String
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}
