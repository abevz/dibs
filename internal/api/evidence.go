package api

import (
	"encoding/json"
	"net/http"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store"
)

func handleRegisterEvidence(st store.CoordinatorStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveIssueID(st, w, r)
		if !ok {
			return
		}
		var req core.RegisterEvidenceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		e, err := st.RegisterEvidence(r.Context(), id, req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				writeError(w, 400, apiErr.Code, apiErr.Message)
			} else {
				writeInternalError(w, err, "register evidence")
			}
			return
		}
		writeJSON(w, 201, map[string]any{"evidence": e})
	}
}
func handleListEvidence(st store.CoordinatorStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if r.PathValue("issue_id") != "" {
			var ok bool
			id, ok = resolveIssueID(st, w, r)
			if !ok {
				return
			}
		}
		items, err := st.ListEvidence(r.Context(), id)
		if err != nil {
			writeInternalError(w, err, "list evidence")
			return
		}
		writeJSON(w, 200, map[string]any{"evidence": items})
	}
}
func handlePruneEvidence(st store.CoordinatorStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req core.EvidenceMaintenanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, core.ErrValidationFailed, "invalid JSON body")
			return
		}
		if req.IssueID != "" {
			id, err := st.ResolveIssueID(r.Context(), req.IssueID)
			if err != nil {
				writeError(w, 404, core.ErrNotFound, "issue not found")
				return
			}
			req.IssueID = id
		}
		items, err := st.PruneEvidence(r.Context(), req)
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				writeError(w, 400, apiErr.Code, apiErr.Message)
			} else {
				writeInternalError(w, err, "prune evidence")
			}
			return
		}
		writeJSON(w, 200, map[string]any{"evidence": items})
	}
}
