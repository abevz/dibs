package client

import (
	"context"
	"net/http"

	"github.com/abevz/dibs/internal/core"
)

func (c *Client) RegisterEvidence(ctx context.Context, id string, req core.RegisterEvidenceRequest) (core.Evidence, error) {
	var result struct {
		Evidence core.Evidence `json:"evidence"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+id+"/evidence", req, &result)
	return result.Evidence, err
}
func (c *Client) ListEvidence(ctx context.Context, id string) ([]core.Evidence, error) {
	path := "/v1/evidence"
	if id != "" {
		path = "/v1/issues/" + id + "/evidence"
	}
	var result struct {
		Evidence []core.Evidence `json:"evidence"`
	}
	err := c.doJSON(ctx, http.MethodGet, path, nil, &result)
	return result.Evidence, err
}
func (c *Client) PruneEvidence(ctx context.Context, req core.EvidenceMaintenanceRequest) ([]core.Evidence, error) {
	var result struct {
		Evidence []core.Evidence `json:"evidence"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/v1/evidence/prune", req, &result)
	return result.Evidence, err
}
