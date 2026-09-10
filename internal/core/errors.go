package core

import "fmt"

// ErrorCode constants used across the API.
const (
	ErrValidationFailed = "validation_failed"
	ErrNotFound         = "not_found"
	ErrConflict         = "version_conflict"
	ErrForbidden        = "forbidden"
	ErrLeaseHeld        = "lease_held"
	ErrLeaseExpired     = "lease_expired"
	ErrIssueNotReady    = "issue_not_ready"
	ErrDependencyCycle  = "dependency_cycle"
	ErrAlreadyLinked    = "already_linked"
	ErrAlreadyTagged    = "already_tagged"
	// ErrIdempotencyConflict is returned when an operation_id is reused with a
	// request that does not match the one it originally recorded
	// (AFC-SDD-0159). It always fails closed: the second mutation never runs.
	ErrIdempotencyConflict = "idempotency_conflict"
)

// APIError is the standard error envelope returned by the daemon.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIErrorResponse is the outer wrapper for an error response.
type APIErrorResponse struct {
	Error APIError `json:"error"`
}

func (e APIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewAPIError creates an APIError.
func NewAPIError(code, msg string) APIError {
	return APIError{Code: code, Message: msg}
}
