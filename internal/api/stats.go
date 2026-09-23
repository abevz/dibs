package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
	"github.com/abevz/dibs/internal/store"
)

func handleStats(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reportResult, err := report.Build(r.Context(), st, report.Query{
			Project: r.URL.Query().Get("project"),
			Repo:    r.URL.Query().Get("repo"),
			Since:   r.URL.Query().Get("since"),
			Until:   r.URL.Query().Get("until"),
		}, time.Now().UTC())
		if err != nil {
			if apiErr, ok := errAsAPIError(err); ok {
				switch apiErr.Code {
				case core.ErrNotFound:
					writeError(w, http.StatusNotFound, apiErr.Code, apiErr.Message)
					return
				case core.ErrValidationFailed:
					writeError(w, http.StatusBadRequest, apiErr.Code, apiErr.Message)
					return
				}
			}
			logger.Error("build stats report", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to build statistics report")
			return
		}
		writeJSON(w, http.StatusOK, map[string]report.Report{"report": reportResult})
	}
}
