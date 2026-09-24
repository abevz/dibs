package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store"
)

func handleCreateIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req core.CreateIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if err := core.ValidateCreateIssue(req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		if req.Actor == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "actor is required")
			return
		}

		issue, err := st.CreateIssue(r.Context(), req.Project, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				if apiErr.Code == core.ErrIdempotencyConflict {
					writeError(w, http.StatusConflict, core.ErrIdempotencyConflict, apiErr.Message)
					return
				}
				if apiErr.Code == core.ErrNotFound {
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				}
				if apiErr.Code == core.ErrValidationFailed {
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			if isUniqueConstraintError(err) {
				writeError(w, http.StatusConflict, "short_id_taken",
					"an issue with this short_id already exists")
				return
			}
			logger.Error("failed to create issue", "project", req.Project, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to create issue")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]core.Issue{"issue": issue})
	}
}

// resolveIssueID resolves the issue_id path parameter (supports both UUID and short_id)
// and writes an error response if the issue is not found. Returns the UUID id and true on success.
func resolveIssueID(st store.CoordinatorStore, w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := st.ResolveIssueID(r.Context(), r.PathValue("issue_id"))
	if err != nil {
		if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
			writeError(w, http.StatusNotFound, core.ErrNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to resolve issue")
		}
		return "", false
	}
	return id, true
}

func handleGetIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		issue, lease, err := st.GetIssue(r.Context(), issueID)
		if err != nil {
			logger.Error("failed to get issue", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get issue")
			return
		}

		resp := map[string]interface{}{
			"issue": issue,
		}
		if lease != nil {
			resp["lease"] = lease
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handleListIssues(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projects, err := core.NormalizeIssueListValues(r.URL.Query()["project"])
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "project filter values must not contain empty elements")
			return
		}
		statuses, err := core.NormalizeIssueListValues(r.URL.Query()["status"])
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "status filter values must not contain empty elements")
			return
		}
		issueTypes, err := core.NormalizeIssueListValues(r.URL.Query()["type"])
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "type filter values must not contain empty elements")
			return
		}
		tags, err := core.NormalizeIssueListValues(r.URL.Query()["tag"])
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "tag filter values must not contain empty elements")
			return
		}

		params := core.IssueListParams{
			Repo:        r.URL.Query().Get("repo"),
			Worktree:    r.URL.Query().Get("worktree"),
			Assignee:    r.URL.Query().Get("assignee"),
			ExternalKey: r.URL.Query().Get("external_key"),
			Projects:    projects,
			Statuses:    statuses,
			IssueTypes:  issueTypes,
			Tags:        tags,
		}
		for _, issueType := range params.IssueTypes {
			if !core.ValidIssueType(issueType) {
				writeError(w, http.StatusBadRequest, core.ErrValidationFailed,
					"type must be one of: task, bug, feature, epic, chore")
				return
			}
		}

		issues, err := st.ListIssues(r.Context(), params)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			logger.Error("failed to list issues", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list issues")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.Issue{"issues": issues})
	}
}

func handleClaimIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.ClaimRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Holder == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "holder is required")
			return
		}
		if req.TTLSeconds <= 0 {
			req.TTLSeconds = 3600
		}
		invocationMode, err := core.NormalizeInvocationMode(req.InvocationMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		req.InvocationMode = invocationMode
		resp, err := st.ClaimIssueWithOperation(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict:
					writeError(w, http.StatusConflict, core.ErrIdempotencyConflict, apiErr.Message)
					return
				case core.ErrLeaseHeld:
					writeError(w, http.StatusConflict, core.ErrLeaseHeld, apiErr.Message)
					return
				case core.ErrIssueNotReady:
					writeError(w, http.StatusConflict, core.ErrIssueNotReady, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				}
			}
			logger.Error("failed to claim issue", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to claim issue")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handleHeartbeatLease(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.LeaseToken == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "lease_token is required")
			return
		}
		if req.LeaseGeneration <= 0 {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "lease_generation is required")
			return
		}
		if req.TTLSeconds <= 0 {
			req.TTLSeconds = 3600
		}

		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		newExpiresAt, err := st.HeartbeatLeaseWithOperation(r.Context(), issueID, req, time.Now().UTC())
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict, core.ErrConflict:
					writeError(w, http.StatusConflict, apiErr.Code, apiErr.Message)
					return
				case core.ErrLeaseExpired:
					writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				}
			}
			logger.Error("failed to heartbeat lease", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to heartbeat lease")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"expires_at": newExpiresAt})
	}
}

func handleReleaseLease(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.ReleaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.LeaseToken == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "lease_token is required")
			return
		}
		if req.LeaseGeneration <= 0 {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "lease_generation is required")
			return
		}

		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		err := st.ReleaseLeaseWithOperation(r.Context(), issueID, req, time.Now().UTC())
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict, core.ErrConflict:
					writeError(w, http.StatusConflict, apiErr.Code, apiErr.Message)
					return
				case core.ErrLeaseExpired:
					writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				}
			}
			logger.Error("failed to release lease", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to release lease")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleHandoffLease(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.HandoffRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.LeaseToken == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "lease_token is required")
			return
		}
		if err := core.ValidateHandoffRequest(req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		normalizedMode, err := core.NormalizeInvocationMode(req.InvocationMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		req.InvocationMode = normalizedMode
		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		resp, err := st.HandoffLease(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict, core.ErrConflict:
					writeError(w, http.StatusConflict, apiErr.Code, apiErr.Message)
					return
				case core.ErrLeaseExpired:
					writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			logger.Error("failed to hand off lease", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to hand off lease")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handleListReadyIssues(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectFilter := r.URL.Query().Get("project")
		repoFilter := r.URL.Query().Get("repo")

		// Resolve project key to ID if provided.
		var projectID string
		if projectFilter != "" {
			proj, err := st.GetProjectByKey(r.Context(), projectFilter)
			if err != nil {
				if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
					writeError(w, http.StatusNotFound, core.ErrNotFound,
						"project not found: "+projectFilter)
					return
				}
				logger.Error("failed to resolve project for ready issues", "project", projectFilter, "error", err)
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to resolve project")
				return
			}
			projectID = proj.ID
		}

		// Resolve repo logical name to ID if provided.
		var repoID string
		if repoFilter != "" {
			var (
				rp  core.Repository
				err error
			)
			if projectID != "" {
				rp, err = st.GetRepoInProject(r.Context(), projectID, repoFilter)
			} else {
				rp, err = st.GetRepo(r.Context(), repoFilter)
			}
			if err != nil {
				if writeRepoLookupError(w, err, repoFilter) {
					return
				}
				logger.Error("failed to resolve repository for ready issues", "repo", repoFilter, "error", err)
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to resolve repository")
				return
			}
			repoID = rp.ID
		}

		tags, err := core.NormalizeIssueListValues(r.URL.Query()["tag"])
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "tag filter values must not contain empty elements")
			return
		}

		issues, err := st.ListReadyIssues(r.Context(), projectID, repoID, tags)
		if err != nil {
			logger.Error("failed to list ready issues", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list ready issues")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.Issue{"issues": issues})
	}
}

func handleUpdateIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.UpdateIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Actor == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "actor is required")
			return
		}
		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		updated, err := st.UpdateIssue(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict:
					writeError(w, http.StatusConflict, core.ErrIdempotencyConflict, apiErr.Message)
					return
				case core.ErrConflict:
					writeError(w, http.StatusConflict, core.ErrConflict, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrLeaseExpired:
					writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			logger.Error("failed to update issue", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to update issue")
			return
		}

		writeJSON(w, http.StatusOK, map[string]core.Issue{"issue": updated})
	}
}

func handleCloseIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.CloseIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Actor == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "actor is required")
			return
		}
		normalizedMode, err := core.NormalizeInvocationMode(req.InvocationMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		req.InvocationMode = normalizedMode
		if err := core.ValidateOperationID(req.OperationID); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}

		result, err := st.CloseIssue(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrIdempotencyConflict:
					writeError(w, http.StatusConflict, core.ErrIdempotencyConflict, apiErr.Message)
					return
				case core.ErrConflict:
					writeError(w, http.StatusConflict, core.ErrConflict, apiErr.Message)
					return
				case core.ErrLeaseExpired:
					writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			logger.Error("failed to close issue", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to close issue")
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}

func handleOperatorCloseIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkOperatorToken(w, r) {
			return
		}

		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.OperatorCloseIssueRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.Actor == "" || req.Reason == "" || req.ExpectedVersion <= 0 {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed,
				"actor, reason, and expected_version are required")
			return
		}
		normalizedMode, err := core.NormalizeInvocationMode(req.InvocationMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		req.InvocationMode = normalizedMode

		result, err := st.OperatorCloseIssue(r.Context(), issueID, req)
		if err != nil {
			writeIssueMutationError(w, logger, "operator close issue", issueID, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleOperatorReopenIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkOperatorToken(w, r) {
			return
		}

		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.OperatorReopenIssueRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.Actor == "" || req.Reason == "" || req.ExpectedVersion <= 0 {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed,
				"actor, reason, and expected_version are required")
			return
		}

		issue, err := st.OperatorReopenIssue(r.Context(), issueID, req)
		if err != nil {
			writeIssueMutationError(w, logger, "operator reopen issue", issueID, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]core.Issue{"issue": issue})
	}
}

func handleOperatorReleaseIssue(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkOperatorToken(w, r) {
			return
		}

		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.OperatorReleaseIssueRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.Actor == "" || req.Reason == "" || req.ExpectedVersion <= 0 {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed,
				"actor, reason, and expected_version are required")
			return
		}

		issue, err := st.OperatorReleaseIssue(r.Context(), issueID, req)
		if err != nil {
			writeIssueMutationError(w, logger, "operator release issue", issueID, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]core.Issue{"issue": issue})
	}
}

// checkOperatorToken validates the configured operator token against the
// Authorization header. Returns false and writes a 403 response on failure.
func checkOperatorToken(w http.ResponseWriter, r *http.Request) bool {
	token := config.EnvOrDefault("DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN", "")
	if token == "" {
		writeError(w, http.StatusForbidden, core.ErrForbidden, "DIBS_OPERATOR_TOKEN not configured on server")
		return false
	}
	if got := r.Header.Get("Authorization"); got != fmt.Sprintf("Bearer %s", token) {
		writeError(w, http.StatusForbidden, core.ErrForbidden, "invalid or missing operator token")
		return false
	}
	return true
}

func writeIssueMutationError(w http.ResponseWriter, logger *slog.Logger, operation, issueID string, err error) {
	if apiErr, ok := errAsAPIError(err); ok {
		switch apiErr.Code {
		case core.ErrConflict:
			writeError(w, http.StatusConflict, core.ErrConflict, apiErr.Message)
			return
		case core.ErrLeaseExpired:
			writeError(w, http.StatusGone, core.ErrLeaseExpired, apiErr.Message)
			return
		case core.ErrNotFound:
			writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
			return
		case core.ErrValidationFailed:
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
			return
		}
	}
	logger.Error("failed to "+operation, "issue_id", issueID, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "failed to "+operation)
}

func handleAddDependency(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.DependsOn == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "depends_on is required")
			return
		}
		if req.Kind == "" {
			req.Kind = "blocks"
		}

		if req.Actor == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "actor is required")
			return
		}

		err := st.AddDependency(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrDependencyCycle:
					writeError(w, http.StatusUnprocessableEntity, core.ErrDependencyCycle, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrConflict:
					writeError(w, http.StatusConflict, core.ErrConflict, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
			}
			logger.Error("failed to add dependency", "issue_id", issueID, "depends_on", req.DependsOn, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to add dependency")
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

func handleRemoveDependency(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}
		dependsOn := r.PathValue("depends_on")
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			kind = "blocks"
		}

		actor := r.URL.Query().Get("actor")

		err := st.RemoveDependency(r.Context(), issueID, dependsOn, kind, actor)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
				return
			}
			logger.Error("failed to remove dependency", "issue_id", issueID, "depends_on", dependsOn, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to remove dependency")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleLinkArtifact(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.LinkArtifactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Artifact == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "artifact is required")
			return
		}

		createdAt, err := st.LinkArtifact(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrAlreadyLinked:
					writeError(w, http.StatusConflict, core.ErrAlreadyLinked, apiErr.Message)
					return
				}
			}
			logger.Error("failed to link artifact", "issue_id", issueID, "artifact", req.Artifact, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to link artifact")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"created_at": createdAt})
	}
}

func handleUnlinkArtifact(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		artifact := r.URL.Query().Get("artifact")
		if artifact == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "artifact is required")
			return
		}
		relation := r.URL.Query().Get("relation")
		actor := r.URL.Query().Get("actor")

		err := st.UnlinkArtifact(r.Context(), issueID, artifact, relation, actor)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
				return
			}
			logger.Error("failed to unlink artifact", "issue_id", issueID, "artifact", artifact, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to unlink artifact")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListIssueLinks(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		links, err := st.ListIssueArtifacts(r.Context(), issueID)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
				return
			}
			logger.Error("failed to list issue links", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list issue links")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.ArtifactRef{"links": links})
	}
}

func handleAddTag(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.AddTagRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Tag == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "tag is required")
			return
		}
		if req.Actor == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "actor is required")
			return
		}

		err := st.AddTag(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
					return
				case core.ErrAlreadyTagged:
					writeError(w, http.StatusConflict, core.ErrAlreadyTagged, apiErr.Message)
					return
				}
			}
			logger.Error("failed to add tag", "issue_id", issueID, "tag", req.Tag, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to add tag")
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

func handleRemoveTag(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		tag := r.URL.Query().Get("tag")
		if tag == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "tag is required")
			return
		}
		actor := r.URL.Query().Get("actor")

		err := st.RemoveTag(r.Context(), issueID, tag, actor)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, apiErr.Message)
				return
			}
			logger.Error("failed to remove tag", "issue_id", issueID, "tag", tag, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to remove tag")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCreateNote(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		var req core.CreateNoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid JSON body")
			return
		}

		if req.Author == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "author is required")
			return
		}
		if req.Body == "" {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "body is required")
			return
		}
		normalizedMode, err := core.NormalizeInvocationMode(req.InvocationMode)
		if err != nil {
			writeError(w, http.StatusBadRequest, core.ErrValidationFailed, err.Error())
			return
		}
		req.InvocationMode = normalizedMode

		note, err := st.CreateNote(r.Context(), issueID, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, "issue not found: "+issueID)
				return
			}
			logger.Error("failed to create note", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to create note")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]core.Note{"note": note})
	}
}

func handleListNotes(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		notes, err := st.ListNotes(r.Context(), issueID)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, "issue not found: "+issueID)
				return
			}
			logger.Error("failed to list notes", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list notes")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.Note{"notes": notes})
	}
}

func handleListEvents(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}

		events, err := st.ListEvents(r.Context(), issueID)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrNotFound {
				writeError(w, http.StatusNotFound, core.ErrNotFound, "issue not found: "+issueID)
				return
			}
			logger.Error("failed to list events", "issue_id", issueID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list events")
			return
		}

		writeJSON(w, http.StatusOK, map[string][]core.Event{"events": events})
	}
}

func handleWatchEvents(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")

		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "limit must be a positive integer")
				return
			}
			if n > 500 {
				n = 500
			}
			limit = n
		}

		waitMS := 0
		if raw := r.URL.Query().Get("wait_ms"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "wait_ms must be a non-negative integer")
				return
			}
			if n > 30000 {
				n = 30000
			}
			waitMS = n
		}

		deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
		for {
			page, err := st.ListGlobalEvents(r.Context(), since, limit)
			if err != nil {
				if apiErr, ok := errAsAPIError(err); ok && apiErr.Code == core.ErrValidationFailed {
					writeError(w, http.StatusBadRequest, core.ErrValidationFailed, apiErr.Message)
					return
				}
				logger.Error("failed to watch events", "since", since, "error", err)
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to list events")
				return
			}
			if len(page.Events) > 0 || waitMS == 0 || time.Now().After(deadline) {
				writeJSON(w, http.StatusOK, page)
				return
			}

			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}
