package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store"
)

func handleCreateProject(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key         string `json:"key"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if err := core.ValidateCreateProject(body.Key, body.Name); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		project, err := st.CreateProject(r.Context(), body.Key, body.Name, body.Description)
		if err != nil {
			logger.Error("failed to create project", "key", body.Key, "error", err)
			// Detect unique constraint violation on key.
			if isUniqueConstraintError(err) {
				writeError(w, http.StatusConflict, "project_key_taken",
					"a project with this key already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to create project")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]core.Project{"project": project})
	}
}

func handleListProjects(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects, err := st.ListProjects(r.Context())
		if err != nil {
			logger.Error("failed to list projects", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list projects")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.Project{"projects": projects})
	}
}

// isUniqueConstraintError returns true if the error is a SQLite UNIQUE constraint violation.
// modernc.org/sqlite returns "UNIQUE constraint failed: <table>.<column>".
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint")
}
