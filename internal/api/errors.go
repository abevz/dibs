package api

import (
	"errors"
	"net/http"

	"github.com/abevz/dibs/internal/core"
	sqliteDriver "modernc.org/sqlite"
)

// writeError writes a JSON error response matching the API v1 error envelope.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("X-Dibs-Error-Code", code)
	writeJSON(w, status, core.APIErrorResponse{
		Error: core.NewAPIError(code, msg),
	})
}

// writeInternalError preserves the public internal_error envelope while
// classifying local store failures for mutation telemetry.
func writeInternalError(w http.ResponseWriter, err error, msg string) {
	result := "transaction_failure"
	var sqliteErr *sqliteDriver.Error
	if errors.As(err, &sqliteErr) && (sqliteErr.Code()&0xff == 5 || sqliteErr.Code()&0xff == 6) {
		result = "db_busy"
	}
	w.Header().Set("X-Dibs-Result-Code", result)
	writeError(w, http.StatusInternalServerError, "internal_error", msg)
}
