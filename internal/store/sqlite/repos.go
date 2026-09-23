package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

// CreateRepo inserts a new repository and its remotes in a transaction.
// The projectKey is resolved to a project ID internally.
func CreateRepo(ctx context.Context, db *sql.DB, projectKey string, req core.CreateRepoRequest) (core.Repository, []core.RepoRemote, error) {
	// Resolve project key to ID.
	proj, err := GetProjectByKey(ctx, db, projectKey)
	if err != nil {
		return core.Repository{}, nil, fmt.Errorf("resolve project: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	repoID := uuid.New().String()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return core.Repository{}, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO repositories (id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', '', ?, ?)`,
		repoID, proj.ID, req.LogicalName, req.CanonicalGitDir, req.DefaultBranch, now, now,
	)
	if err != nil {
		return core.Repository{}, nil, fmt.Errorf("insert repo: %w", err)
	}

	var remotes []core.RepoRemote
	for _, r := range req.Remotes {
		remoteID := uuid.New().String()
		isPrimary := 0
		if r.IsPrimary {
			isPrimary = 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO repo_remotes (id, repository_id, remote_name, fetch_url, push_url, is_primary, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			remoteID, repoID, r.RemoteName, r.FetchURL, r.PushURL, isPrimary, now, now,
		)
		if err != nil {
			return core.Repository{}, nil, fmt.Errorf("insert remote %s: %w", r.RemoteName, err)
		}
		remotes = append(remotes, core.RepoRemote{
			ID:           remoteID,
			RepositoryID: repoID,
			RemoteName:   r.RemoteName,
			FetchURL:     r.FetchURL,
			PushURL:      r.PushURL,
			IsPrimary:    r.IsPrimary,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}

	if err := tx.Commit(); err != nil {
		return core.Repository{}, nil, fmt.Errorf("commit tx: %w", err)
	}

	repo := core.Repository{
		ID:              repoID,
		ProjectID:       proj.ID,
		LogicalName:     req.LogicalName,
		CanonicalGitDir: req.CanonicalGitDir,
		DefaultBranch:   req.DefaultBranch,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	return repo, remotes, nil
}

// GetRepo retrieves a repository by ID or logical name.
func GetRepo(ctx context.Context, db *sql.DB, idOrName string) (core.Repository, error) {
	if repo, ok, err := lookupRepoByID(ctx, db, idOrName); err != nil {
		return core.Repository{}, err
	} else if ok {
		return repo, nil
	}

	return lookupRepoByLogicalName(ctx, db, "", idOrName)
}

// GetRepoInProject retrieves a repository by exact UUID or by logical name scoped to a project.
func GetRepoInProject(ctx context.Context, db *sql.DB, projectID, idOrName string) (core.Repository, error) {
	if projectID == "" {
		return GetRepo(ctx, db, idOrName)
	}

	if repo, ok, err := lookupRepoByID(ctx, db, idOrName); err != nil {
		return core.Repository{}, err
	} else if ok {
		return repo, nil
	}

	return lookupRepoByLogicalName(ctx, db, projectID, idOrName)
}

// ListRepos lists repositories optionally filtered by project ID.
func ListRepos(ctx context.Context, db *sql.DB, projectID string) ([]core.Repository, error) {
	var rows *sql.Rows
	var err error

	if projectID != "" {
		rows, err = db.QueryContext(ctx,
			`SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at
			 FROM repositories WHERE project_id = ? ORDER BY logical_name`, projectID,
		)
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at
			 FROM repositories ORDER BY logical_name`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()

	var repos []core.Repository
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repos: %w", err)
	}
	if repos == nil {
		repos = []core.Repository{}
	}
	return repos, nil
}

// ListReposByProjectKey lists repositories by project key (resolved internally).
func ListReposByProjectKey(ctx context.Context, db *sql.DB, projectKey string) ([]core.Repository, error) {
	proj, err := GetProjectByKey(ctx, db, projectKey)
	if err != nil {
		return nil, err
	}
	return ListRepos(ctx, db, proj.ID)
}

func lookupRepoByID(ctx context.Context, db *sql.DB, id string) (core.Repository, bool, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at
		 FROM repositories WHERE id = ?`, id,
	)
	repo, err := scanRepoFields(row)
	if err == sql.ErrNoRows {
		return core.Repository{}, false, nil
	}
	if err != nil {
		return core.Repository{}, false, fmt.Errorf("scan repo by id: %w", err)
	}
	return repo, true, nil
}

func lookupRepoByLogicalName(ctx context.Context, db *sql.DB, projectID, logicalName string) (core.Repository, error) {
	query := `SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at
		FROM repositories WHERE logical_name = ?`
	args := []any{logicalName}
	if projectID != "" {
		query = `SELECT id, project_id, logical_name, canonical_git_dir, default_branch, hosting_kind, hosting_slug, created_at, updated_at
			FROM repositories WHERE project_id = ? AND logical_name = ?`
		args = []any{projectID, logicalName}
	}
	query += ` ORDER BY id`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return core.Repository{}, fmt.Errorf("query repo by logical_name: %w", err)
	}
	defer rows.Close()

	var repos []core.Repository
	for rows.Next() {
		repo, err := scanRepoFields(rows)
		if err != nil {
			return core.Repository{}, fmt.Errorf("scan repo by logical_name: %w", err)
		}
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		return core.Repository{}, fmt.Errorf("iterate repos by logical_name: %w", err)
	}
	if len(repos) == 0 {
		return core.Repository{}, core.NewAPIError(core.ErrNotFound, "repository not found: "+logicalName)
	}
	if len(repos) > 1 {
		return core.Repository{}, core.NewAPIError(core.ErrValidationFailed,
			"repository name is ambiguous; specify the repository UUID or add a project filter: "+logicalName)
	}
	return repos[0], nil
}

func scanRepo(s scanner) (core.Repository, error) {
	r, err := scanRepoFields(s)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Repository{}, core.NewAPIError(core.ErrNotFound, "repository not found")
		}
		return core.Repository{}, fmt.Errorf("scan repo: %w", err)
	}
	return r, nil
}

func scanRepoFields(s scanner) (core.Repository, error) {
	var r core.Repository
	err := s.Scan(&r.ID, &r.ProjectID, &r.LogicalName, &r.CanonicalGitDir, &r.DefaultBranch, &r.HostingKind, &r.HostingSlug, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return core.Repository{}, err
	}
	return r, nil
}
