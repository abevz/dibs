package core

import (
	"fmt"
	"strings"
)

// Repository represents a logical repository inside a project.
type Repository struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	LogicalName     string `json:"logical_name"`
	CanonicalGitDir string `json:"canonical_git_dir"`
	DefaultBranch   string `json:"default_branch"`
	HostingKind     string `json:"hosting_kind,omitempty"`
	HostingSlug     string `json:"hosting_slug,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// RepoRemote represents a tracked remote for a repository.
type RepoRemote struct {
	ID           string `json:"id"`
	RepositoryID string `json:"repository_id"`
	RemoteName   string `json:"remote_name"`
	FetchURL     string `json:"fetch_url"`
	PushURL      string `json:"push_url,omitempty"`
	IsPrimary    bool   `json:"is_primary"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// CreateRepoRequest is the JSON body for POST /v1/repos.
type CreateRepoRequest struct {
	Project         string                `json:"project"`
	LogicalName     string                `json:"logical_name"`
	CanonicalGitDir string                `json:"canonical_git_dir"`
	DefaultBranch   string                `json:"default_branch"`
	Remotes         []CreateRemoteRequest `json:"remotes,omitempty"`
}

// CreateRemoteRequest is a remote entry inside CreateRepoRequest.
type CreateRemoteRequest struct {
	RemoteName string `json:"remote_name"`
	FetchURL   string `json:"fetch_url"`
	PushURL    string `json:"push_url,omitempty"`
	IsPrimary  bool   `json:"is_primary,omitempty"`
}

// RelocateRepoRequest moves the registered paths for an existing repository.
type RelocateRepoRequest struct {
	NewCanonicalGitDir string `json:"new_canonical_git_dir"`
	OperationID        string `json:"operation_id"`
	Actor              string `json:"actor"`
}

type WorktreePathChange struct {
	ID      string `json:"id"`
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

type RelocateRepoResult struct {
	OperationID string     `json:"operation_id"`
	Repository  Repository `json:"repository"`
	Worktrees   []Worktree `json:"worktrees"`
}

// ValidateCreateRepo checks required fields for a new repository.
func ValidateCreateRepo(req CreateRepoRequest) error {
	var errs []string
	if req.Project == "" {
		errs = append(errs, "project is required")
	}
	if req.LogicalName == "" {
		errs = append(errs, "logical_name is required")
	}
	if req.CanonicalGitDir == "" {
		errs = append(errs, "canonical_git_dir is required")
	}
	for i, r := range req.Remotes {
		if r.RemoteName == "" {
			errs = append(errs, fmt.Sprintf("remotes[%d].remote_name is required", i))
		}
		if r.FetchURL == "" {
			errs = append(errs, fmt.Sprintf("remotes[%d].fetch_url is required", i))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("validation_failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
