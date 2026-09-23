package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// CreateProject inserts a new project and returns it.
func CreateProject(ctx context.Context, db *sql.DB, key, name, description string) (core.Project, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	_, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, key, name, description, next_issue_seq, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		id, key, name, description, now, now,
	)
	if err != nil {
		return core.Project{}, fmt.Errorf("insert project: %w", err)
	}

	return core.Project{
		ID:           id,
		Key:          key,
		Name:         name,
		Description:  description,
		NextIssueSeq: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// GetProject retrieves a project by its ID.
func GetProject(ctx context.Context, db *sql.DB, id string) (core.Project, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, key, name, description, next_issue_seq, created_at, updated_at
		 FROM projects WHERE id = ?`, id,
	)
	return scanProject(row)
}

// GetProjectByKey retrieves a project by its key.
func GetProjectByKey(ctx context.Context, db *sql.DB, key string) (core.Project, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, key, name, description, next_issue_seq, created_at, updated_at
		 FROM projects WHERE key = ?`, key,
	)
	return scanProject(row)
}

// ListProjects returns all projects ordered by created_at descending.
func ListProjects(ctx context.Context, db *sql.DB) ([]core.Project, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, key, name, description, next_issue_seq, created_at, updated_at
		 FROM projects ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []core.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	if projects == nil {
		projects = []core.Project{}
	}
	return projects, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanProject(s scanner) (core.Project, error) {
	var p core.Project
	err := s.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.NextIssueSeq, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Project{}, core.NewAPIError(core.ErrNotFound, "project not found")
		}
		return core.Project{}, fmt.Errorf("scan project: %w", err)
	}
	return p, nil
}
