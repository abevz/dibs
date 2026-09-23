package api

import (
	"log/slog"
	"net/http"

	"github.com/abevz/dibs/internal/store"
)

func handleExportJSONL(st store.CoordinatorStore, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		if err := st.ExportJSONL(r.Context(), w); err != nil {
			logger.Error("failed to export jsonl", "error", err)
		}
	}
}
