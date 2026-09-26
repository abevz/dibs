package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"strings"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
)

// ClientError is a structured error returned when the daemon responds with an API error envelope.
type ClientError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Client is an HTTP client that connects to the daemon over a Unix socket.
type Client struct {
	socketPath    string
	httpClient    *http.Client
	operatorToken string
}

// New creates a new Client connected to the given Unix socket path.
func New(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}

	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: transport,
		},
	}
}

// Health sends a GET /healthz request.
func (c *Client) Health(ctx context.Context) (core.Health, error) {
	var result core.Health
	if err := c.doJSON(ctx, http.MethodGet, "/healthz", nil, &result); err != nil {
		return core.Health{}, err
	}
	return result, nil
}

// GetStats sends a GET /v1/stats request with optional scope and time filters.
func (c *Client) GetStats(ctx context.Context, query report.Query) (report.Report, error) {
	path := "/v1/stats"
	values := url.Values{}
	if query.Project != "" {
		values.Set("project", query.Project)
	}
	if query.Repo != "" {
		values.Set("repo", query.Repo)
	}
	if query.Since != "" {
		values.Set("since", query.Since)
	}
	if query.Until != "" {
		values.Set("until", query.Until)
	}
	if len(values) > 0 {
		path += "?" + values.Encode()
	}

	var result struct {
		Report report.Report `json:"report"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return report.Report{}, err
	}
	return result.Report, nil
}

// CreateProject sends a POST /v1/projects request.
func (c *Client) CreateProject(ctx context.Context, key, name, description string) (core.Project, error) {
	body := map[string]string{
		"key":         key,
		"name":        name,
		"description": description,
	}

	var result struct {
		Project core.Project `json:"project"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/projects", body, &result); err != nil {
		return core.Project{}, err
	}
	return result.Project, nil
}

// ListProjects sends a GET /v1/projects request.
func (c *Client) ListProjects(ctx context.Context) ([]core.Project, error) {
	var result struct {
		Projects []core.Project `json:"projects"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/projects", nil, &result); err != nil {
		return nil, err
	}
	return result.Projects, nil
}

// CreateRepo sends a POST /v1/repos request.
func (c *Client) CreateRepo(ctx context.Context, req core.CreateRepoRequest) (core.Repository, []core.RepoRemote, error) {
	var result struct {
		Repository core.Repository   `json:"repository"`
		Remotes    []core.RepoRemote `json:"remotes"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/repos", req, &result); err != nil {
		return core.Repository{}, nil, err
	}
	return result.Repository, result.Remotes, nil
}

// ListRepos sends a GET /v1/repos request with an optional project filter.
func (c *Client) ListRepos(ctx context.Context, project string) ([]core.Repository, error) {
	path := "/v1/repos"
	if project != "" {
		path += "?project=" + project
	}

	var result struct {
		Repositories []core.Repository `json:"repositories"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Repositories, nil
}

// RelocateRepo moves a repository registration and its worktree paths.
func (c *Client) RelocateRepo(ctx context.Context, repoID string, req core.RelocateRepoRequest) (core.RelocateRepoResult, error) {
	var result core.RelocateRepoResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/repos/"+url.PathEscape(repoID)+"/relocate", req, &result); err != nil {
		return core.RelocateRepoResult{}, err
	}
	return result, nil
}

// RegisterWorktree sends a POST /v1/worktrees request.
func (c *Client) RegisterWorktree(ctx context.Context, req core.CreateWorktreeRequest) (core.Worktree, error) {
	var result struct {
		Worktree core.Worktree `json:"worktree"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/worktrees", req, &result); err != nil {
		return core.Worktree{}, err
	}
	return result.Worktree, nil
}

// ListWorktrees sends a GET /v1/worktrees request with an optional repo filter.
func (c *Client) ListWorktrees(ctx context.Context, repo string) ([]core.Worktree, error) {
	path := "/v1/worktrees"
	if repo != "" {
		path += "?repo=" + repo
	}

	var result struct {
		Worktrees []core.Worktree `json:"worktrees"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Worktrees, nil
}

// DeleteWorktree sends a DELETE /v1/worktrees/{worktreeID} request.
func (c *Client) DeleteWorktree(ctx context.Context, worktreeID string) (core.Worktree, error) {
	var result struct {
		Worktree core.Worktree `json:"worktree"`
	}
	if err := c.doJSON(ctx, http.MethodDelete, "/v1/worktrees/"+worktreeID, nil, &result); err != nil {
		return core.Worktree{}, err
	}
	return result.Worktree, nil
}

// CreateArtifactRoot sends a POST /v1/artifact-roots request.
func (c *Client) CreateArtifactRoot(ctx context.Context, req core.CreateArtifactRootRequest) (core.ArtifactRoot, error) {
	var result struct {
		ArtifactRoot core.ArtifactRoot `json:"artifact_root"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/artifact-roots", req, &result); err != nil {
		return core.ArtifactRoot{}, err
	}
	return result.ArtifactRoot, nil
}

// ListArtifactRoots sends a GET /v1/artifact-roots request with an optional repo filter.
func (c *Client) ListArtifactRoots(ctx context.Context, repo string) ([]core.ArtifactRoot, error) {
	path := "/v1/artifact-roots"
	if repo != "" {
		path += "?repo=" + repo
	}

	var result struct {
		ArtifactRoots []core.ArtifactRoot `json:"artifact_roots"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.ArtifactRoots, nil
}

// CreateArtifact sends a POST /v1/artifacts request.
func (c *Client) CreateArtifact(ctx context.Context, req core.CreateArtifactRequest) (core.Artifact, error) {
	var result struct {
		Artifact core.Artifact `json:"artifact"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/artifacts", req, &result); err != nil {
		return core.Artifact{}, err
	}
	return result.Artifact, nil
}

// ListArtifacts sends a GET /v1/artifacts request with an optional repo filter.
func (c *Client) ListArtifacts(ctx context.Context, repo string) ([]core.Artifact, error) {
	path := "/v1/artifacts"
	if repo != "" {
		path += "?repo=" + repo
	}

	var result struct {
		Artifacts []core.Artifact `json:"artifacts"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// CreateIssue sends a POST /v1/issues request.
func (c *Client) CreateIssue(ctx context.Context, req core.CreateIssueRequest) (core.Issue, error) {
	var result struct {
		Issue core.Issue `json:"issue"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues", req, &result); err != nil {
		return core.Issue{}, err
	}
	return result.Issue, nil
}

// GetIssue sends a GET /v1/issues/{issueID} request.
func (c *Client) GetIssue(ctx context.Context, issueID string) (core.Issue, *core.IssueLease, error) {
	var result struct {
		Issue core.Issue       `json:"issue"`
		Lease *core.IssueLease `json:"lease,omitempty"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/issues/"+issueID, nil, &result); err != nil {
		return core.Issue{}, nil, err
	}
	return result.Issue, result.Lease, nil
}

// ListIssues sends a GET /v1/issues request with optional single-value query params.
// It remains available for source compatibility; new callers with multi-value
// filters should use ListIssuesWithFilters.
func (c *Client) ListIssues(ctx context.Context, project, repo, worktree, status, assignee, issueType, externalKey string) ([]core.Issue, error) {
	return c.ListIssuesWithFilters(ctx, core.IssueListParams{
		Project:     project,
		Repo:        repo,
		Worktree:    worktree,
		Status:      status,
		Assignee:    assignee,
		IssueType:   issueType,
		ExternalKey: externalKey,
	})
}

// ListIssuesWithFilters sends a GET /v1/issues request with optional filters.
// Project, status, and issue-type slices are emitted as repeated query keys.
func (c *Client) ListIssuesWithFilters(ctx context.Context, params core.IssueListParams) ([]core.Issue, error) {
	path := "/v1/issues"
	query := url.Values{}
	appendParam := func(key, val string) {
		if val != "" {
			query.Set(key, val)
		}
	}
	appendValues := func(key string, values []string, fallback string) {
		if len(values) == 0 {
			appendParam(key, fallback)
			return
		}
		for _, value := range values {
			query.Add(key, value)
		}
	}
	appendValues("project", params.Projects, params.Project)
	appendParam("repo", params.Repo)
	appendParam("worktree", params.Worktree)
	appendValues("status", params.Statuses, params.Status)
	appendParam("assignee", params.Assignee)
	appendValues("type", params.IssueTypes, params.IssueType)
	appendParam("external_key", params.ExternalKey)
	appendValues("tag", params.Tags, "")
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Offset > 0 {
		query.Set("offset", strconv.Itoa(params.Offset))
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	var result struct {
		Issues []core.Issue `json:"issues"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Issues, nil
}

// ClaimIssue sends a POST /v1/issues/{issueID}/claim request.
func (c *Client) ClaimIssue(ctx context.Context, issueID, holder string, ttlSeconds int) (core.ClaimResponse, error) {
	return c.ClaimIssueWithSession(ctx, issueID, holder, ttlSeconds, "")
}

// ClaimIssueWithSession sends a claim with optional non-secret session
// correlation metadata. Session identity never replaces the lease holder.
func (c *Client) ClaimIssueWithSession(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID string) (core.ClaimResponse, error) {
	return c.ClaimIssueWithSessionAndMode(ctx, issueID, holder, ttlSeconds, sessionID, "")
}

// ClaimIssueWithSessionAndMode sends a claim with optional non-secret session
// correlation metadata and a caller-declared invocation mode. The mode is
// recorded on the claim event; an empty mode is left for the daemon to
// normalize to the conservative "unknown" default rather than inferring
// anything from the process tree.
func (c *Client) ClaimIssueWithSessionAndMode(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID, invocationMode string) (core.ClaimResponse, error) {
	body := core.ClaimRequest{Holder: holder, TTLSeconds: ttlSeconds, SessionID: sessionID, InvocationMode: invocationMode}
	var result core.ClaimResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/claim", body, &result); err != nil {
		return core.ClaimResponse{}, err
	}
	return result, nil
}

// ClaimIssueWithRequest sends a fully specified claim, including any
// AFC-SDD-0159 operation_id. Retrying the same request with the same
// operation_id returns the original committed ClaimResponse rather than
// attempting a second claim.
func (c *Client) ClaimIssueWithRequest(ctx context.Context, issueID string, req core.ClaimRequest) (core.ClaimResponse, error) {
	var result core.ClaimResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/claim", req, &result); err != nil {
		return core.ClaimResponse{}, err
	}
	return result, nil
}

// HeartbeatLease sends a POST /v1/issues/{issueID}/heartbeat request with the
// lease generation from the claim that created the lease.
func (c *Client) HeartbeatLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int) (string, error) {
	body := core.HeartbeatRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, TTLSeconds: ttlSeconds}
	return c.HeartbeatLeaseWithOperation(ctx, issueID, body)
}

func (c *Client) HeartbeatLeaseWithOperation(ctx context.Context, issueID string, body core.HeartbeatRequest) (string, error) {
	var result struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/heartbeat", body, &result); err != nil {
		return "", err
	}
	return result.ExpiresAt, nil
}

// ReleaseLease sends a POST /v1/issues/{issueID}/release request with the
// lease generation from the claim that created the lease.
func (c *Client) ReleaseLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64) error {
	body := core.ReleaseRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration}
	return c.ReleaseLeaseWithOperation(ctx, issueID, body)
}

func (c *Client) ReleaseLeaseWithOperation(ctx context.Context, issueID string, body core.ReleaseRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/release", body, nil)
}

// HandoffLease sends a HANDOFF note and lease release as one atomic request
// with the lease generation from the claim that created the lease.
func (c *Client) HandoffLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, note string) (core.HandoffResponse, error) {
	return c.HandoffLeaseWithMode(ctx, issueID, leaseToken, leaseGeneration, note, "")
}

// HandoffLeaseWithMode sends a HANDOFF note and lease release as one atomic
// request, recording a caller-declared invocation mode on the handoff note's
// audit event. An empty mode normalizes to "unknown" on the daemon.
func (c *Client) HandoffLeaseWithMode(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, note, invocationMode string) (core.HandoffResponse, error) {
	body := core.HandoffRequest{LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, Note: note, InvocationMode: invocationMode}
	return c.HandoffLeaseWithOperation(ctx, issueID, body)
}

func (c *Client) HandoffLeaseWithOperation(ctx context.Context, issueID string, body core.HandoffRequest) (core.HandoffResponse, error) {
	var result core.HandoffResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/handoff", body, &result); err != nil {
		return core.HandoffResponse{}, err
	}
	return result, nil
}

// UpdateIssue sends a PATCH /v1/issues/{issueID} request.
func (c *Client) UpdateIssue(ctx context.Context, issueID string, req core.UpdateIssueRequest) (core.Issue, error) {
	var result struct {
		Issue core.Issue `json:"issue"`
	}
	if err := c.doJSON(ctx, http.MethodPatch, "/v1/issues/"+issueID, req, &result); err != nil {
		return core.Issue{}, err
	}
	return result.Issue, nil
}

// CloseIssue sends a POST /v1/issues/{issueID}/close request.
func (c *Client) CloseIssue(ctx context.Context, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	var result core.CloseIssueResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/close", req, &result); err != nil {
		return core.CloseIssueResult{}, err
	}
	return result, nil
}

// OperatorCloseIssue sends a POST /v1/issues/{issueID}/operator-close request.
// This local operator path is explicit and intentionally has no lease token.
func (c *Client) OperatorCloseIssue(ctx context.Context, issueID string, req core.OperatorCloseIssueRequest) (core.CloseIssueResult, error) {
	var result core.CloseIssueResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/operator-close", req, &result); err != nil {
		return core.CloseIssueResult{}, err
	}
	return result, nil
}

// OperatorReopenIssue sends a POST /v1/issues/{issueID}/operator-reopen request.
func (c *Client) OperatorReopenIssue(ctx context.Context, issueID string, req core.OperatorReopenIssueRequest) (core.Issue, error) {
	var result struct {
		Issue core.Issue `json:"issue"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/operator-reopen", req, &result); err != nil {
		return core.Issue{}, err
	}
	return result.Issue, nil
}

// OperatorReleaseIssue sends a POST /v1/issues/{issueID}/operator-release
// request. This local operator path is explicit and intentionally has no
// lease token; it recovers a claim whose lease token was lost before its
// TTL naturally expired it, without waiting for expiry.
func (c *Client) OperatorReleaseIssue(ctx context.Context, issueID string, req core.OperatorReleaseIssueRequest) (core.Issue, error) {
	var result struct {
		Issue core.Issue `json:"issue"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/operator-release", req, &result); err != nil {
		return core.Issue{}, err
	}
	return result.Issue, nil
}

// LinkArtifact sends a POST /v1/issues/{issueID}/links request.
func (c *Client) LinkArtifact(ctx context.Context, issueID string, req core.LinkArtifactRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/links", req, nil)
}

// UnlinkArtifact sends a DELETE /v1/issues/{issueID}/links request.
func (c *Client) UnlinkArtifact(ctx context.Context, issueID string, req core.UnlinkArtifactRequest) error {
	query := url.Values{}
	query.Set("artifact", req.Artifact)
	if req.Relation != "" {
		query.Set("relation", req.Relation)
	}
	if req.Actor != "" {
		query.Set("actor", req.Actor)
	}
	return c.doJSON(ctx, http.MethodDelete, "/v1/issues/"+issueID+"/links?"+query.Encode(), nil, nil)
}

// ListIssueLinks sends a GET /v1/issues/{issueID}/links request.
func (c *Client) ListIssueLinks(ctx context.Context, issueID string) ([]core.ArtifactRef, error) {
	var result struct {
		Links []core.ArtifactRef `json:"links"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/issues/"+issueID+"/links", nil, &result); err != nil {
		return nil, err
	}
	return result.Links, nil
}

// AddDependency sends a POST /v1/issues/{issueID}/dependencies request.
func (c *Client) AddDependency(ctx context.Context, issueID string, req core.AddDependencyRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/dependencies", req, nil)
}

// RemoveDependency sends a DELETE /v1/issues/{issueID}/dependencies/{dependsOn} request.
func (c *Client) RemoveDependency(ctx context.Context, issueID string, req core.RemoveDependencyRequest) error {
	path := "/v1/issues/" + issueID + "/dependencies/" + req.DependsOn
	query := url.Values{}
	if req.Kind != "" {
		query.Set("kind", req.Kind)
	}
	if req.Actor != "" {
		query.Set("actor", req.Actor)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// AddTag sends a POST /v1/issues/{issueID}/tags request.
func (c *Client) AddTag(ctx context.Context, issueID string, req core.AddTagRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/tags", req, nil)
}

// RemoveTag sends a DELETE /v1/issues/{issueID}/tags request with the tag
// as a query parameter (the tag value contains '/', which is not a safe
// single path segment across all HTTP tooling).
func (c *Client) RemoveTag(ctx context.Context, issueID, tag, actor string) error {
	query := url.Values{"tag": {tag}}
	if actor != "" {
		query.Set("actor", actor)
	}
	return c.doJSON(ctx, http.MethodDelete, "/v1/issues/"+issueID+"/tags?"+query.Encode(), nil, nil)
}

// CreateNote sends a POST /v1/issues/{issueID}/notes request.
func (c *Client) CreateNote(ctx context.Context, issueID, author, body string) (core.Note, error) {
	return c.CreateNoteWithMode(ctx, issueID, author, body, "")
}

// CreateNoteWithMode sends a POST /v1/issues/{issueID}/notes request with a
// caller-declared invocation mode recorded on the note_added audit event. An
// empty mode normalizes to "unknown" on the daemon.
func (c *Client) CreateNoteWithMode(ctx context.Context, issueID, author, body, invocationMode string) (core.Note, error) {
	req := core.CreateNoteRequest{Author: author, Body: body, InvocationMode: invocationMode}
	var result struct {
		Note core.Note `json:"note"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/issues/"+issueID+"/notes", req, &result); err != nil {
		return core.Note{}, err
	}
	return result.Note, nil
}

// ListNotes sends a GET /v1/issues/{issueID}/notes request.
func (c *Client) ListNotes(ctx context.Context, issueID string) ([]core.Note, error) {
	var result struct {
		Notes []core.Note `json:"notes"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/issues/"+issueID+"/notes", nil, &result); err != nil {
		return nil, err
	}
	return result.Notes, nil
}

// ListEvents sends a GET /v1/issues/{issueID}/events request.
func (c *Client) ListEvents(ctx context.Context, issueID string) ([]core.Event, error) {
	var result struct {
		Events []core.Event `json:"events"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/issues/"+issueID+"/events", nil, &result); err != nil {
		return nil, err
	}
	return result.Events, nil
}

// RecentEvents reads the newest events and returns a cursor for future updates.
func (c *Client) RecentEvents(ctx context.Context, limit int) (core.EventPage, error) {
	path := "/v1/events/recent"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var result core.EventPage
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return core.EventPage{}, err
	}
	if result.Events == nil {
		result.Events = []core.Event{}
	}
	return result, nil
}

// WatchEvents sends a GET /v1/events request with cursor pagination and optional long-polling.
func (c *Client) WatchEvents(ctx context.Context, since string, limit, waitMS int) (core.EventPage, error) {
	path := "/v1/events"
	query := url.Values{}
	if since != "" {
		query.Set("since", since)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if waitMS > 0 {
		query.Set("wait_ms", strconv.Itoa(waitMS))
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	var result core.EventPage
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return core.EventPage{}, err
	}
	if result.Events == nil {
		result.Events = []core.Event{}
	}
	return result, nil
}

// ExportJSONL streams a JSONL export from the daemon to the provided writer.
func (c *Client) ExportJSONL(ctx context.Context, w io.Writer) error {
	return c.doStream(ctx, http.MethodGet, "/v1/export/jsonl", nil, w)
}

// ListReadyIssues sends a GET /v1/issues/ready request with optional
// project, repo, and tag filters. Multiple tags are ANDed by the daemon.
func (c *Client) ListReadyIssues(ctx context.Context, project, repo string, tags []string) ([]core.Issue, error) {
	path := "/v1/issues/ready"
	var params []string
	if project != "" {
		params = append(params, "project="+project)
	}
	if repo != "" {
		params = append(params, "repo="+repo)
	}
	for _, tag := range tags {
		params = append(params, "tag="+tag)
	}
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	var result struct {
		Issues []core.Issue `json:"issues"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Issues, nil
}

// SetOperatorToken sets the bearer token used for operator-authorization requests.
func (c *Client) SetOperatorToken(token string) {
	c.operatorToken = token
}

// doJSON performs an HTTP request and decodes the JSON response.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, target any) error {
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://af-coordinator"+path, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.operatorToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.operatorToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp core.APIErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Code != "" {
			return &ClientError{Code: errResp.Error.Code, Message: errResp.Error.Message}
		}
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

func (c *Client) doStream(ctx context.Context, method, path string, body any, w io.Writer) error {
	if w == nil {
		return fmt.Errorf("stream target writer is required")
	}

	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://af-coordinator"+path, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp core.APIErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Code != "" {
			return &ClientError{Code: errResp.Error.Code, Message: errResp.Error.Message}
		}
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("copy response body: %w", err)
	}

	return nil
}
