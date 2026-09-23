package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// CreateArtifactRoot inserts a new artifact root for the given repository.
func CreateArtifactRoot(ctx context.Context, db *sql.DB, repoID string, req core.CreateArtifactRootRequest) (core.ArtifactRoot, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	kind := req.Kind
	if kind == "" {
		kind = "sdd"
	}

	if !core.ValidateArtifactKind(kind) {
		return core.ArtifactRoot{}, core.NewAPIError(core.ErrValidationFailed,
			fmt.Sprintf("invalid artifact root kind: %q", kind))
	}

	isPrimary := 0
	if req.Primary {
		isPrimary = 1
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO artifact_roots (id, repository_id, root_path, kind, is_primary, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, repoID, req.RootPath, kind, isPrimary, now, now,
	)
	if err != nil {
		return core.ArtifactRoot{}, fmt.Errorf("insert artifact root: %w", err)
	}

	return core.ArtifactRoot{
		ID:           id,
		RepositoryID: repoID,
		RootPath:     req.RootPath,
		Kind:         kind,
		IsPrimary:    req.Primary,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// GetArtifactRoot retrieves an artifact root by ID.
func GetArtifactRoot(ctx context.Context, db *sql.DB, id string) (core.ArtifactRoot, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repository_id, root_path, kind, is_primary, created_at, updated_at
		 FROM artifact_roots WHERE id = ?`, id,
	)
	return scanArtifactRoot(row)
}

// ListArtifactRoots returns artifact roots, optionally filtered by repository ID.
func ListArtifactRoots(ctx context.Context, db *sql.DB, repoID string) ([]core.ArtifactRoot, error) {
	var rows *sql.Rows
	var err error

	if repoID != "" {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, root_path, kind, is_primary, created_at, updated_at
			 FROM artifact_roots WHERE repository_id = ? ORDER BY root_path`, repoID,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, root_path, kind, is_primary, created_at, updated_at
			 FROM artifact_roots ORDER BY root_path`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list artifact roots: %w", err)
	}
	defer rows.Close()

	var roots []core.ArtifactRoot
	for rows.Next() {
		r, err := scanArtifactRoot(rows)
		if err != nil {
			return nil, err
		}
		roots = append(roots, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact roots: %w", err)
	}
	if roots == nil {
		roots = []core.ArtifactRoot{}
	}
	return roots, nil
}

// CreateArtifact inserts a new artifact.
func CreateArtifact(ctx context.Context, db *sql.DB, repoID string, req core.CreateArtifactRequest) (core.Artifact, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	if !core.ValidateArtifactKind(req.Kind) {
		return core.Artifact{}, core.NewAPIError(core.ErrValidationFailed,
			fmt.Sprintf("invalid artifact kind: %q", req.Kind))
	}

	var actualID, createdAt, updatedAt string
	err := db.QueryRowContext(ctx,
		`INSERT INTO artifacts (id, repository_id, worktree_id, artifact_root_id, kind, relative_path, title, external_key, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repository_id, relative_path) DO UPDATE SET
		   updated_at = excluded.updated_at,
		   title = CASE WHEN excluded.title != '' THEN excluded.title ELSE artifacts.title END,
		   kind = CASE WHEN excluded.kind != '' THEN excluded.kind ELSE artifacts.kind END
		 RETURNING id, created_at, updated_at`,
		id, repoID, nullableUUID(req.Worktree), nullableUUID(req.ArtifactRootID),
		req.Kind, req.RelativePath, req.Title, req.ExternalKey, req.Status, now, now,
	).Scan(&actualID, &createdAt, &updatedAt)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("upsert artifact: %w", err)
	}

	return core.Artifact{
		ID:             actualID,
		RepositoryID:   repoID,
		WorktreeID:     req.Worktree,
		ArtifactRootID: req.ArtifactRootID,
		Kind:           req.Kind,
		RelativePath:   req.RelativePath,
		Title:          req.Title,
		ExternalKey:    req.ExternalKey,
		Status:         req.Status,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

// GetArtifact retrieves an artifact by ID.
func GetArtifact(ctx context.Context, db *sql.DB, id string) (core.Artifact, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repository_id, worktree_id, artifact_root_id, kind, relative_path,
		        title, external_key, status, created_at, updated_at
		 FROM artifacts WHERE id = ?`, id,
	)
	return scanArtifact(row)
}

// ListArtifacts returns artifacts, optionally filtered by repository ID.
func ListArtifacts(ctx context.Context, db *sql.DB, repoID string) ([]core.Artifact, error) {
	var rows *sql.Rows
	var err error

	if repoID != "" {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, worktree_id, artifact_root_id, kind, relative_path,
			        title, external_key, status, created_at, updated_at
			 FROM artifacts WHERE repository_id = ? ORDER BY relative_path`, repoID,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT id, repository_id, worktree_id, artifact_root_id, kind, relative_path,
			        title, external_key, status, created_at, updated_at
			 FROM artifacts ORDER BY relative_path`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []core.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	if artifacts == nil {
		artifacts = []core.Artifact{}
	}
	return artifacts, nil
}

// scanArtifactRoot scans a scanner result into an ArtifactRoot.
func scanArtifactRoot(s scanner) (core.ArtifactRoot, error) {
	var r core.ArtifactRoot
	var isPrimary int
	err := s.Scan(&r.ID, &r.RepositoryID, &r.RootPath, &r.Kind, &isPrimary, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.ArtifactRoot{}, core.NewAPIError(core.ErrNotFound, "artifact root not found")
		}
		return core.ArtifactRoot{}, fmt.Errorf("scan artifact root: %w", err)
	}
	r.IsPrimary = isPrimary != 0
	return r, nil
}

// scanArtifact scans a scanner result into an Artifact.
func scanArtifact(s scanner) (core.Artifact, error) {
	var a core.Artifact
	var worktreeID, artifactRootID sql.NullString
	err := s.Scan(&a.ID, &a.RepositoryID, &worktreeID, &artifactRootID,
		&a.Kind, &a.RelativePath, &a.Title, &a.ExternalKey, &a.Status,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Artifact{}, core.NewAPIError(core.ErrNotFound, "artifact not found")
		}
		return core.Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}
	if worktreeID.Valid {
		a.WorktreeID = worktreeID.String
	}
	if artifactRootID.Valid {
		a.ArtifactRootID = artifactRootID.String
	}
	return a, nil
}

// nullableUUID returns a *string for an empty string, used for nullable foreign keys.
func nullableUUID(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ResolveArtifactID resolves an artifact by UUID or relative path within a repository.
func ResolveArtifactID(ctx context.Context, db *sql.DB, repoID, idOrPath string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE id = ?`, idOrPath).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("resolve artifact by id: %w", err)
	}

	if repoID == "" {
		return "", core.NewAPIError(core.ErrNotFound, "artifact not found or missing repository scope for path resolution")
	}

	err = db.QueryRowContext(ctx, `SELECT id FROM artifacts WHERE repository_id = ? AND relative_path = ?`, repoID, idOrPath).Scan(&id)
	if err == sql.ErrNoRows {
		return "", core.NewAPIError(core.ErrNotFound, "artifact not found: "+idOrPath)
	}
	if err != nil {
		return "", fmt.Errorf("resolve artifact by path: %w", err)
	}
	return id, nil
}
