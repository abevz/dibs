package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/relocate"
	"github.com/abevz/dibs/internal/store"
)

func handleCreateRepo(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req core.CreateRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if err := core.ValidateCreateRepo(req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		repo, remotes, err := st.CreateRepo(r.Context(), req.Project, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				if apiErr.Code == core.ErrNotFound {
					writeError(w, http.StatusNotFound, core.ErrNotFound,
						"project not found: "+req.Project)
					return
				}
			}
			if isUniqueConstraintError(err) {
				writeError(w, http.StatusConflict, "repo_name_taken",
					"a repository with this name already exists in the project")
				return
			}
			logger.Error("failed to create repo", "logical_name", req.LogicalName, "error", err)
			writeInternalError(w, err, "failed to create repository")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"repository": repo,
			"remotes":    remotes,
		})
	}
}

func handleRelocateRepo(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req core.RelocateRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.NewCanonicalGitDir == "" || !filepath.IsAbs(req.NewCanonicalGitDir) || filepath.Clean(req.NewCanonicalGitDir) != req.NewCanonicalGitDir || req.Actor == "" || req.OperationID == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "new_canonical_git_dir must be a clean absolute path; actor and operation_id are required")
			return
		}
		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		repoID := r.PathValue("repo_id")
		repo, err := st.GetRepo(r.Context(), repoID)
		if err != nil {
			if writeRepoLookupError(w, err, repoID) {
				return
			}
			writeInternalError(w, err, "failed to find repository")
			return
		}
		worktrees, err := st.ListWorktrees(r.Context(), repo.ID)
		if err != nil {
			writeInternalError(w, err, "failed to list worktrees")
			return
		}
		changes, err := relocate.Plan(r.Context(), repo.CanonicalGitDir, req.NewCanonicalGitDir, worktrees)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		result, err := st.RelocateRepo(r.Context(), repo.ID, repo.CanonicalGitDir, req, changes)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrConflict, core.ErrIdempotencyConflict:
					writeError(w, http.StatusConflict, apiErr.Code, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, apiErr.Code, apiErr.Message)
					return
				}
			}
			logger.Error("failed to relocate repo", "repo", repo.ID, "error", err)
			writeInternalError(w, err, "failed to relocate repository")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleListRepos(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectFilter := r.URL.Query().Get("project")

		var repos []core.Repository
		var err error

		if projectFilter != "" {
			repos, err = st.ListReposByProjectKey(r.Context(), projectFilter)
			if err != nil {
				if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
					writeError(w, http.StatusNotFound, core.ErrNotFound,
						"project not found: "+projectFilter)
					return
				}
				logger.Error("failed to list repos by project", "project", projectFilter, "error", err)
				writeInternalError(w, err, "failed to list repositories")
				return
			}
		} else {
			repos, err = st.ListRepos(r.Context(), "")
			if err != nil {
				logger.Error("failed to list repos", "error", err)
				writeInternalError(w, err, "failed to list repositories")
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string][]core.Repository{"repositories": repos})
	}
}

// errAsAPIError checks if an error is an APIError from core.
func errAsAPIError(err error) (core.APIError, bool) {
	var apiErr core.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return core.APIError{}, false
}

func writeRepoLookupError(w http.ResponseWriter, err error, repo string) bool {
	apiErr, ok := errAsAPIError(err)
	if !ok {
		return false
	}

	switch apiErr.Code {
	case core.ErrNotFound:
		writeError(w, http.StatusNotFound, core.ErrNotFound, "repository not found: "+repo)
		return true
	case core.ErrValidationFailed:
		writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
		return true
	default:
		return false
	}
}
