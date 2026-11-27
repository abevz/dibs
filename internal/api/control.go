package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store"
)

func registerControlRoutes(mux *http.ServeMux, st store.CoordinatorStore, logger *slog.Logger) {
	mux.HandleFunc("POST /v1/control/change", func(w http.ResponseWriter, r *http.Request) {
		if !checkOperatorToken(w, r) {
			return
		}
		var req core.ControlChangeRequest
		if !decodeControlRequest(w, r, &req) {
			return
		}
		out, err := st.ChangeControl(r.Context(), req)
		writeControlResult(w, logger, out, err)
	})

	for path, enroll := range map[string]bool{"/v1/control/status": false, "/v1/control/enroll": true} {
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			var req core.ControlStatusRequest
			if !decodeControlRequest(w, r, &req) {
				return
			}
			out, err := st.ControlStatus(r.Context(), req, enroll)
			writeControlResult(w, logger, out, err)
		})
	}

	mux.HandleFunc("POST /v1/control/acknowledge", func(w http.ResponseWriter, r *http.Request) {
		var req core.ControlAckRequest
		if !decodeControlRequest(w, r, &req) {
			return
		}
		out, err := st.AcknowledgeControl(r.Context(), req)
		writeControlResult(w, logger, out, err)
	})
}

func decodeControlRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "invalid control request")
		return false
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeError(w, http.StatusBadRequest, core.ErrValidationFailed, "expected one control request")
		return false
	}
	return true
}

func writeControlResult(w http.ResponseWriter, logger *slog.Logger, result any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if apiErr, ok := errAsAPIError(err); ok {
		switch apiErr.Code {
		case core.ErrControlChanged, core.ErrControlRestricted, core.ErrIdempotencyConflict:
			writeError(w, http.StatusConflict, apiErr.Code, apiErr.Message)
			return
		case core.ErrControlStopped:
			writeError(w, http.StatusGone, apiErr.Code, apiErr.Message)
			return
		}
	}
	writeIssueMutationError(w, logger, "operator control", "", err)
}
