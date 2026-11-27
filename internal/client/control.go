package client

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/abevz/dibs/internal/core"
)

type httpStatusError struct {
	code   int
	status string
}

func (e *httpStatusError) Error() string { return "unexpected status: " + e.status }

// ControlUnavailable identifies transport failures and the plain 404 emitted
// by a daemon that does not implement the control endpoint. A typed 404, a
// wrong credential, and a malformed reply still fail the gate.
func ControlUnavailable(err error) bool {
	var status *httpStatusError
	var network *net.OpError
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &network) ||
		(errors.As(err, &status) && status.code == http.StatusNotFound)
}

func (c *Client) ChangeControl(ctx context.Context, req core.ControlChangeRequest) (core.ControlChange, error) {
	var result core.ControlChange
	err := c.doJSON(ctx, http.MethodPost, "/v1/control/change", req, &result)
	return result, err
}

func (c *Client) ControlStatus(ctx context.Context, req core.ControlStatusRequest) (core.ControlStatus, error) {
	var result core.ControlStatus
	err := c.doJSON(ctx, http.MethodPost, "/v1/control/status", req, &result)
	return result, err
}

func (c *Client) EnrollControl(ctx context.Context, req core.ControlStatusRequest) (core.ControlStatus, error) {
	var result core.ControlStatus
	err := c.doJSON(ctx, http.MethodPost, "/v1/control/enroll", req, &result)
	return result, err
}

func (c *Client) AcknowledgeControl(ctx context.Context, req core.ControlAckRequest) (core.ControlAck, error) {
	var result core.ControlAck
	err := c.doJSON(ctx, http.MethodPost, "/v1/control/acknowledge", req, &result)
	return result, err
}
