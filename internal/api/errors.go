package api

import (
	"net/http"

	"github.com/abevz/dibs/internal/core"
)

// writeError writes a JSON error response matching the API v1 error envelope.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, core.APIErrorResponse{
		Error: core.NewAPIError(code, msg),
	})
}
