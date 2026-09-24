package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/core"
	"github.com/google/uuid"
)

const (
	serverName             = "dibs-mcp"
	defaultProtocolVersion = "2025-03-26"
)

// CoordinatorClient describes the daemon API surface used by the MCP wrapper.
type CoordinatorClient interface {
	Health(ctx context.Context) (core.Health, error)
	GetIssue(ctx context.Context, issueID string) (core.Issue, *core.IssueLease, error)
	ListReadyIssues(ctx context.Context, project, repo string, tags []string) ([]core.Issue, error)
	CreateIssue(ctx context.Context, req core.CreateIssueRequest) (core.Issue, error)
	ClaimIssue(ctx context.Context, issueID, holder string, ttlSeconds int) (core.ClaimResponse, error)
	ClaimIssueWithSession(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID string) (core.ClaimResponse, error)
	ClaimIssueWithSessionAndMode(ctx context.Context, issueID, holder string, ttlSeconds int, sessionID, invocationMode string) (core.ClaimResponse, error)
	ClaimIssueWithRequest(ctx context.Context, issueID string, req core.ClaimRequest) (core.ClaimResponse, error)
	HeartbeatLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int) (string, error)
	HeartbeatLeaseWithOperation(ctx context.Context, issueID string, req core.HeartbeatRequest) (string, error)
	ReleaseLeaseWithOperation(ctx context.Context, issueID string, req core.ReleaseRequest) error
	HandoffLease(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, note string) (core.HandoffResponse, error)
	HandoffLeaseWithMode(ctx context.Context, issueID, leaseToken string, leaseGeneration int64, note, invocationMode string) (core.HandoffResponse, error)
	HandoffLeaseWithOperation(ctx context.Context, issueID string, req core.HandoffRequest) (core.HandoffResponse, error)
	UpdateIssue(ctx context.Context, issueID string, req core.UpdateIssueRequest) (core.Issue, error)
	CreateNote(ctx context.Context, issueID, author, body string) (core.Note, error)
	CreateNoteWithMode(ctx context.Context, issueID, author, body, invocationMode string) (core.Note, error)
	ListNotes(ctx context.Context, issueID string) ([]core.Note, error)
	ListEvents(ctx context.Context, issueID string) ([]core.Event, error)
	CloseIssue(ctx context.Context, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error)
	OperatorCloseIssue(ctx context.Context, issueID string, req core.OperatorCloseIssueRequest) (core.CloseIssueResult, error)
	OperatorReopenIssue(ctx context.Context, issueID string, req core.OperatorReopenIssueRequest) (core.Issue, error)
	OperatorReleaseIssue(ctx context.Context, issueID string, req core.OperatorReleaseIssueRequest) (core.Issue, error)
	AddTag(ctx context.Context, issueID string, req core.AddTagRequest) error
	RemoveTag(ctx context.Context, issueID, tag, actor string) error
}

// Server is a tiny MCP stdio server that wraps the daemon API.
type Server struct {
	client  CoordinatorClient
	actor   string
	name    string
	version string
}

// NewServer constructs a new MCP wrapper server.
func NewServer(c CoordinatorClient, actor, version string) *Server {
	return &Server{
		client:  c,
		actor:   actor,
		name:    serverName,
		version: version,
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Run serves newline-delimited JSON-RPC 2.0 messages over MCP stdio.
func (s *Server) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		body, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return err
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 {
			if err == io.EOF {
				return nil
			}
			continue
		}
		if !utf8.Valid(body) {
			if writeErr := writeMessage(w, rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "invalid UTF-8 JSON request"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			if writeErr := writeMessage(w, rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "invalid JSON request"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}

		resp := s.handleRequest(ctx, req)
		if resp == nil {
			continue
		}
		if err := writeMessage(w, *resp); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) *rpcResponse {
	if len(req.ID) == 0 {
		return nil // JSON-RPC notifications never receive a response.
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return s.errorResponse(req.ID, -32600, "jsonrpc must be 2.0")
	}

	switch req.Method {
	case "initialize":
		return s.initializeResponse(req)
	case "ping":
		return s.resultResponse(req.ID, map[string]any{})
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.resultResponse(req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		return s.handleToolCall(ctx, req)
	default:
		return s.errorResponse(req.ID, -32601, "method not found")
	}
}

func (s *Server) initializeResponse(req rpcRequest) *rpcResponse {
	protocolVersion := defaultProtocolVersion
	if len(req.Params) > 0 {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil && params.ProtocolVersion != "" {
			protocolVersion = params.ProtocolVersion
		}
	}

	return s.resultResponse(req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	})
}

func (s *Server) handleToolCall(ctx context.Context, req rpcRequest) *rpcResponse {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "invalid tools/call params")
	}

	result, err := s.callTool(ctx, params)
	if err != nil {
		return s.resultResponse(req.ID, toolErrorResult(err))
	}
	return s.resultResponse(req.ID, toolSuccessResult(result))
}

func (s *Server) callTool(ctx context.Context, params toolCallParams) (any, error) {
	switch params.Name {
	case "health":
		return s.client.Health(ctx)
	case "get_issue":
		var args struct {
			IssueID string `json:"issue_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" {
			return nil, fmt.Errorf("issue_id is required")
		}
		issue, lease, err := s.client.GetIssue(ctx, args.IssueID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"issue": issue, "lease": lease}, nil
	case "list_ready_issues":
		var args struct {
			Project string   `json:"project"`
			Repo    string   `json:"repo"`
			Tags    []string `json:"tags"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		issues, err := s.client.ListReadyIssues(ctx, args.Project, args.Repo, args.Tags)
		if err != nil {
			return nil, err
		}
		return map[string]any{"issues": issues}, nil
	case "create_issue":
		var args core.CreateIssueRequest
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		args.Actor = actor
		if err := core.ValidateCreateIssue(args); err != nil {
			return nil, err
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		args.OperationID = id
		issue, err := s.client.CreateIssue(ctx, args)
		return operationOutcome(id, map[string]any{"issue": issue}, err)
	case "claim_issue":
		var args struct {
			IssueID        string `json:"issue_id"`
			Holder         string `json:"holder"`
			Actor          string `json:"actor"`
			TTLSeconds     int    `json:"ttl_seconds"`
			SessionID      string `json:"session_id"`
			InvocationMode string `json:"invocation_mode"`
			OperationID    string `json:"operation_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" {
			return nil, fmt.Errorf("issue_id is required")
		}
		holder, err := s.resolveActor(args.Holder, args.Actor)
		if err != nil {
			return nil, err
		}
		mode, err := core.NormalizeInvocationMode(args.InvocationMode)
		if err != nil {
			return nil, err
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		claim, err := s.client.ClaimIssueWithRequest(ctx, args.IssueID, core.ClaimRequest{
			Holder: holder, TTLSeconds: args.TTLSeconds, SessionID: args.SessionID,
			InvocationMode: mode, OperationID: id,
		})
		return operationOutcome(id, claim, err)
	case "heartbeat_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			LeaseToken      string `json:"lease_token"`
			LeaseGeneration int64  `json:"lease_generation"`
			TTLSeconds      int    `json:"ttl_seconds"`
			OperationID     string `json:"operation_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.LeaseToken == "" {
			return nil, fmt.Errorf("issue_id and lease_token are required")
		}
		if args.LeaseGeneration <= 0 {
			return nil, fmt.Errorf("lease_generation is required")
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		expiresAt, err := s.client.HeartbeatLeaseWithOperation(ctx, args.IssueID, core.HeartbeatRequest{
			LeaseToken: args.LeaseToken, LeaseGeneration: args.LeaseGeneration,
			TTLSeconds: args.TTLSeconds, OperationID: id,
		})
		return operationOutcome(id, map[string]any{"expires_at": expiresAt}, err)
	case "release_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			LeaseToken      string `json:"lease_token"`
			LeaseGeneration int64  `json:"lease_generation"`
			OperationID     string `json:"operation_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.LeaseToken == "" || args.LeaseGeneration <= 0 {
			return nil, fmt.Errorf("issue_id, lease_token, and lease_generation are required")
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		err = s.client.ReleaseLeaseWithOperation(ctx, args.IssueID, core.ReleaseRequest{
			LeaseToken: args.LeaseToken, LeaseGeneration: args.LeaseGeneration, OperationID: id,
		})
		return operationOutcome(id, map[string]any{"status": "ok"}, err)
	case "handoff_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			LeaseToken      string `json:"lease_token"`
			LeaseGeneration int64  `json:"lease_generation"`
			Note            string `json:"note"`
			InvocationMode  string `json:"invocation_mode"`
			OperationID     string `json:"operation_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.LeaseToken == "" {
			return nil, fmt.Errorf("issue_id and lease_token are required")
		}
		if args.LeaseGeneration <= 0 {
			return nil, fmt.Errorf("lease_generation is required")
		}
		if err := core.ValidateHandoffRequest(core.HandoffRequest{Note: args.Note}); err != nil {
			return nil, err
		}
		mode, err := core.NormalizeInvocationMode(args.InvocationMode)
		if err != nil {
			return nil, err
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.HandoffLeaseWithOperation(ctx, args.IssueID, core.HandoffRequest{
			LeaseToken: args.LeaseToken, LeaseGeneration: args.LeaseGeneration,
			Note: args.Note, InvocationMode: mode, OperationID: id,
		})
		return operationOutcome(id, resp, err)
	case "add_note":
		var args struct {
			IssueID        string `json:"issue_id"`
			Body           string `json:"body"`
			Author         string `json:"author"`
			Actor          string `json:"actor"`
			InvocationMode string `json:"invocation_mode"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.Body == "" {
			return nil, fmt.Errorf("issue_id and body are required")
		}
		author, err := s.resolveActor(args.Author, args.Actor)
		if err != nil {
			return nil, err
		}
		mode, err := core.NormalizeInvocationMode(args.InvocationMode)
		if err != nil {
			return nil, err
		}
		return s.client.CreateNoteWithMode(ctx, args.IssueID, author, args.Body, mode)
	case "add_tag":
		var args struct {
			IssueID string `json:"issue_id"`
			Tag     string `json:"tag"`
			Actor   string `json:"actor"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.Tag == "" {
			return nil, fmt.Errorf("issue_id and tag are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		if err := s.client.AddTag(ctx, args.IssueID, core.AddTagRequest{Tag: args.Tag, Actor: actor}); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok"}, nil
	case "remove_tag":
		var args struct {
			IssueID string `json:"issue_id"`
			Tag     string `json:"tag"`
			Actor   string `json:"actor"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.Tag == "" {
			return nil, fmt.Errorf("issue_id and tag are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		if err := s.client.RemoveTag(ctx, args.IssueID, args.Tag, actor); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok"}, nil
	case "list_notes":
		var args struct {
			IssueID string `json:"issue_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" {
			return nil, fmt.Errorf("issue_id is required")
		}
		notes, err := s.client.ListNotes(ctx, args.IssueID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"notes": notes}, nil
	case "list_issue_events":
		var args struct {
			IssueID string `json:"issue_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" {
			return nil, fmt.Errorf("issue_id is required")
		}
		events, err := s.client.ListEvents(ctx, args.IssueID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": events}, nil
	case "update_issue":
		var args struct {
			IssueID string `json:"issue_id"`
			core.UpdateIssueRequest
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.ExpectedVersion <= 0 {
			return nil, fmt.Errorf("issue_id and expected_version are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		req := args.UpdateIssueRequest
		req.Actor, req.OperationID = actor, id
		issue, err := s.client.UpdateIssue(ctx, args.IssueID, req)
		return operationOutcome(id, map[string]any{"issue": issue}, err)
	case "close_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			Resolution      string `json:"resolution"`
			Branch          string `json:"branch"`
			PRURL           string `json:"pr_url"`
			CommitSHA       string `json:"commit_sha"`
			ExpectedVersion int    `json:"expected_version"`
			LeaseToken      string `json:"lease_token"`
			LeaseGeneration int64  `json:"lease_generation"`
			Actor           string `json:"actor"`
			Note            string `json:"note"`
			InvocationMode  string `json:"invocation_mode"`
			OperationID     string `json:"operation_id"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.Resolution == "" || args.ExpectedVersion <= 0 || args.LeaseToken == "" {
			return nil, fmt.Errorf("issue_id, resolution, expected_version, and lease_token are required")
		}
		if args.LeaseGeneration <= 0 {
			return nil, fmt.Errorf("lease_generation is required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		mode, err := core.NormalizeInvocationMode(args.InvocationMode)
		if err != nil {
			return nil, err
		}
		id, err := toolOperationID(args.OperationID)
		if err != nil {
			return nil, err
		}
		result, err := s.client.CloseIssue(ctx, args.IssueID, core.CloseIssueRequest{
			Resolution:      args.Resolution,
			Branch:          args.Branch,
			PRURL:           args.PRURL,
			CommitSHA:       args.CommitSHA,
			ExpectedVersion: args.ExpectedVersion,
			LeaseToken:      args.LeaseToken,
			LeaseGeneration: args.LeaseGeneration,
			Actor:           actor,
			Note:            args.Note,
			InvocationMode:  mode,
			OperationID:     id,
		})
		return operationOutcome(id, result, err)
	case "operator_close_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			Resolution      string `json:"resolution"`
			ExpectedVersion int    `json:"expected_version"`
			Reason          string `json:"reason"`
			Actor           string `json:"actor"`
			InvocationMode  string `json:"invocation_mode"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.Resolution == "" || args.ExpectedVersion <= 0 || strings.TrimSpace(args.Reason) == "" {
			return nil, fmt.Errorf("issue_id, resolution, expected_version, and reason are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		mode, err := core.NormalizeInvocationMode(args.InvocationMode)
		if err != nil {
			return nil, err
		}
		return s.client.OperatorCloseIssue(ctx, args.IssueID, core.OperatorCloseIssueRequest{
			Resolution:      args.Resolution,
			ExpectedVersion: args.ExpectedVersion,
			Actor:           actor,
			Reason:          args.Reason,
			InvocationMode:  mode,
		})
	case "operator_reopen_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			ExpectedVersion int    `json:"expected_version"`
			Reason          string `json:"reason"`
			Actor           string `json:"actor"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.ExpectedVersion <= 0 || strings.TrimSpace(args.Reason) == "" {
			return nil, fmt.Errorf("issue_id, expected_version, and reason are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		return s.client.OperatorReopenIssue(ctx, args.IssueID, core.OperatorReopenIssueRequest{
			ExpectedVersion: args.ExpectedVersion,
			Actor:           actor,
			Reason:          args.Reason,
		})
	case "operator_release_issue":
		var args struct {
			IssueID         string `json:"issue_id"`
			ExpectedVersion int    `json:"expected_version"`
			Reason          string `json:"reason"`
			Actor           string `json:"actor"`
		}
		if err := unmarshalArgs(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.IssueID == "" || args.ExpectedVersion <= 0 || strings.TrimSpace(args.Reason) == "" {
			return nil, fmt.Errorf("issue_id, expected_version, and reason are required")
		}
		actor, err := s.resolveActor(args.Actor, "")
		if err != nil {
			return nil, err
		}
		return s.client.OperatorReleaseIssue(ctx, args.IssueID, core.OperatorReleaseIssueRequest{
			ExpectedVersion: args.ExpectedVersion,
			Actor:           actor,
			Reason:          args.Reason,
		})
	default:
		return nil, fmt.Errorf("unknown tool: %s", params.Name)
	}
}

func (s *Server) tools() []map[string]any {
	return []map[string]any{
		toolDefinition("health", "Return daemon health from GET /healthz.", objectSchema(nil)),
		toolDefinition("get_issue", "Fetch one issue plus its active lease, if any.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
		})),
		toolDefinition("list_ready_issues", "List ready issues filtered by optional project, repo, and tags.", objectSchema([]schemaField{
			{name: "project", fieldType: "string", description: "Optional project key."},
			{name: "repo", fieldType: "string", description: "Optional repository id or logical name."},
			{name: "tags", fieldType: "array", itemType: "string", description: "Optional namespaced tags; an issue must carry every listed tag (AND)."},
		})),
		toolDefinition("create_issue", "Create an issue with a retry-safe operation ID.", objectSchema([]schemaField{
			{name: "project", fieldType: "string", description: "Project key.", required: true},
			{name: "scope_kind", fieldType: "string", description: "project, repository, or worktree.", required: true},
			{name: "title", fieldType: "string", description: "Issue title.", required: true},
			{name: "issue_type", fieldType: "string", description: "Optional task, bug, feature, epic, or chore."},
			{name: "repo", fieldType: "string", description: "Repository reference for repository/worktree scope."},
			{name: "worktree", fieldType: "string", description: "Optional worktree reference."},
			{name: "external_key", fieldType: "string", description: "Optional external key."},
			{name: "description", fieldType: "string", description: "Optional description."},
			{name: "acceptance_criteria", fieldType: "string", description: "Optional acceptance criteria."},
			{name: "priority", fieldType: "integer", description: "Optional priority; daemon default applies when omitted."},
			{name: "tags", fieldType: "array", itemType: "string", description: "Optional namespaced tags."},
			{name: "actor", fieldType: "string", description: "Optional actor; falls back to DIBS_ACTOR."},
			operationIDField(),
		})),
		toolDefinition("claim_issue", "Claim an issue and acquire a lease token.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "holder", fieldType: "string", description: "Optional holder name; falls back to actor or DIBS_ACTOR."},
			{name: "actor", fieldType: "string", description: "Optional actor fallback for the holder field."},
			{name: "ttl_seconds", fieldType: "integer", description: "Optional lease TTL in seconds; daemon default applies when omitted."},
			{name: "session_id", fieldType: "string", description: "Optional non-secret caller session correlation ID."},
			invocationModeField(),
			operationIDField(),
		})),
		toolDefinition("heartbeat_issue", "Extend an active lease. Exact operation_id replay returns historical expiry; use a new ID to prove current ownership.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "lease_token", fieldType: "string", description: "Current lease token.", required: true},
			{name: "lease_generation", fieldType: "integer", description: "Fencing generation from the claim that created the lease.", required: true},
			{name: "ttl_seconds", fieldType: "integer", description: "Optional lease TTL in seconds; daemon default applies when omitted."},
			operationIDField(),
		})),
		toolDefinition("release_issue", "Release the current active lease.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "lease_token", fieldType: "string", description: "Current lease token.", required: true},
			{name: "lease_generation", fieldType: "integer", description: "Fencing generation from claim.", required: true},
			operationIDField(),
		})),
		toolDefinition("handoff_issue", "Atomically add a required HANDOFF note and release an active lease.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "lease_token", fieldType: "string", description: "Active lease token.", required: true},
			{name: "lease_generation", fieldType: "integer", description: "Fencing generation from the claim that created the lease.", required: true},
			{name: "note", fieldType: "string", description: "Non-empty note beginning with HANDOFF:.", required: true},
			invocationModeField(),
			operationIDField(),
		})),
		toolDefinition("add_note", "Append a note to an issue.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "body", fieldType: "string", description: "Note text.", required: true},
			{name: "author", fieldType: "string", description: "Optional note author; falls back to actor or DIBS_ACTOR."},
			{name: "actor", fieldType: "string", description: "Optional actor fallback when author is omitted."},
			invocationModeField(),
		})),
		toolDefinition("add_tag", "Apply a namespaced tag ('namespace/value') to an issue.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "tag", fieldType: "string", description: "Namespaced tag, e.g. 'area/frontend'.", required: true},
			{name: "actor", fieldType: "string", description: "Optional actor; falls back to DIBS_ACTOR."},
		})),
		toolDefinition("remove_tag", "Remove a namespaced tag from an issue.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "tag", fieldType: "string", description: "Namespaced tag to remove.", required: true},
			{name: "actor", fieldType: "string", description: "Optional actor; falls back to DIBS_ACTOR."},
		})),
		toolDefinition("list_notes", "List notes for an issue.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
		})),
		toolDefinition("list_issue_events", "List activity events for an issue.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
		})),
		toolDefinition("update_issue", "Update issue metadata with an explicit expected version.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "expected_version", fieldType: "integer", description: "Original numeric issue version; reuse it on retry.", required: true},
			{name: "title", fieldType: "string", description: "Optional new title."},
			{name: "issue_type", fieldType: "string", description: "Optional new issue type."},
			{name: "external_key", fieldType: "string", description: "Optional external key."},
			{name: "description", fieldType: "string", description: "Optional description."},
			{name: "acceptance_criteria", fieldType: "string", description: "Optional acceptance criteria."},
			{name: "priority", fieldType: "integer", description: "Optional priority."},
			{name: "assignee", fieldType: "string", description: "Optional assignee."},
			{name: "status", fieldType: "string", description: "Optional nonterminal status."},
			{name: "lease_token", fieldType: "string", description: "Current lease token for a leased issue."},
			{name: "lease_generation", fieldType: "integer", description: "Fencing generation for a leased issue."},
			{name: "release_lease", fieldType: "boolean", description: "Release current lease in the update transaction."},
			{name: "actor", fieldType: "string", description: "Optional actor; falls back to DIBS_ACTOR."},
			operationIDField(),
		})),
		toolDefinition("close_issue", "Close an issue through the daemon API with structured resolution metadata.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "resolution", fieldType: "string", description: "Resolution: done or cancelled.", required: true},
			{name: "expected_version", fieldType: "integer", description: "Current issue version.", required: true},
			{name: "lease_token", fieldType: "string", description: "Active lease token.", required: true},
			{name: "lease_generation", fieldType: "integer", description: "Fencing generation from the claim that created the lease.", required: true},
			{name: "branch", fieldType: "string", description: "Optional branch name to record in close metadata."},
			{name: "pr_url", fieldType: "string", description: "Optional pull request URL to record in close metadata."},
			{name: "commit_sha", fieldType: "string", description: "Optional commit SHA to record in close metadata."},
			{name: "note", fieldType: "string", description: "Optional closing note appended atomically before close."},
			{name: "actor", fieldType: "string", description: "Optional actor; falls back to DIBS_ACTOR."},
			invocationModeField(),
			operationIDField(),
		})),
		toolDefinition("operator_close_issue", "Explicit local operator closure for unclaimable or administratively managed work; it never accepts a lease token.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "resolution", fieldType: "string", description: "Resolution: done or cancelled.", required: true},
			{name: "expected_version", fieldType: "integer", description: "Current issue version.", required: true},
			{name: "reason", fieldType: "string", description: "Why an operator is closing the work.", required: true},
			{name: "actor", fieldType: "string", description: "Optional operator identity; falls back to DIBS_ACTOR."},
			invocationModeField(),
		})),
		toolDefinition("operator_reopen_issue", "Explicit local operator reopen for terminal work; it never accepts a lease token.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "expected_version", fieldType: "integer", description: "Current issue version.", required: true},
			{name: "reason", fieldType: "string", description: "Why the terminal work is reopening.", required: true},
			{name: "actor", fieldType: "string", description: "Optional operator identity; falls back to DIBS_ACTOR."},
		})),
		toolDefinition("operator_release_issue", "Explicit local operator recovery for a stuck in_progress issue whose lease token was lost before its TTL expired; force-clears the lease and returns the issue to open without closing it. Never accepts a lease token.", objectSchema([]schemaField{
			{name: "issue_id", fieldType: "string", description: "Issue UUID or short id.", required: true},
			{name: "expected_version", fieldType: "integer", description: "Current issue version.", required: true},
			{name: "reason", fieldType: "string", description: "Why the lease is being force-cleared.", required: true},
			{name: "actor", fieldType: "string", description: "Optional operator identity; falls back to DIBS_ACTOR."},
		})),
	}
}

func (s *Server) resolveActor(primary, fallback string) (string, error) {
	if primary != "" {
		return primary, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	if s.actor != "" {
		return s.actor, nil
	}
	return "", fmt.Errorf("actor is required: pass actor/holder/author or set DIBS_ACTOR")
}

func (s *Server) resultResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

func (s *Server) errorResponse(id json.RawMessage, code int, message string) *rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
}

func toolSuccessResult(payload any) map[string]any {
	text := "{}"
	if payload != nil {
		if data, err := json.MarshalIndent(payload, "", "  "); err == nil {
			text = string(data)
		}
	}
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
		"structuredContent": payload,
	}
}

// A generated ID remains visible even when the daemon returns an ambiguous
// transport/server error, so an MCP caller can retry the exact request.
type operationToolError struct {
	id  string
	err error
}

func (e operationToolError) Error() string { return e.err.Error() }
func (e operationToolError) Unwrap() error { return e.err }

func toolOperationID(provided string) (string, error) {
	if provided == "" {
		provided = uuid.NewString()
	}
	if err := core.ValidateOperationID(provided); err != nil {
		return "", err
	}
	return provided, nil
}

func operationResult(id string, payload any) map[string]any {
	data, _ := json.Marshal(payload)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if result == nil {
		result = make(map[string]any)
	}
	result["operation_id"] = id
	return result
}

func operationOutcome(id string, payload any, err error) (any, error) {
	if err != nil {
		return nil, operationToolError{id: id, err: err}
	}
	return operationResult(id, payload), nil
}

func toolErrorResult(err error) map[string]any {
	payload := map[string]any{"message": err.Error()}
	var clientErr *client.ClientError
	if ok := asClientError(err, &clientErr); ok {
		payload["code"] = clientErr.Code
		payload["message"] = clientErr.Message
	}
	var operationErr operationToolError
	if errors.As(err, &operationErr) {
		payload["operation_id"] = operationErr.id
	}
	text, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(text)},
		},
		"structuredContent": payload,
		"isError":           true,
	}
}

func asClientError(err error, target **client.ClientError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

type schemaField struct {
	name        string
	fieldType   string
	description string
	required    bool
	// itemType is set for fieldType "array" to describe its element type.
	itemType string
	enum     []string
}

func invocationModeField() schemaField {
	return schemaField{name: "invocation_mode", fieldType: "string", description: "Optional caller-declared mode; omitted records unknown.", enum: core.InvocationModes}
}

func operationIDField() schemaField {
	return schemaField{name: "operation_id", fieldType: "string", description: "Optional caller-persisted retry ID. Reuse with identical arguments only to resolve an ambiguous outcome, never for lease liveness; omitted generates a new ID returned in the tool result."}
}

func toolDefinition(name, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": inputSchema,
	}
}

func objectSchema(fields []schemaField) map[string]any {
	props := map[string]any{}
	required := make([]string, 0)
	for _, field := range fields {
		prop := map[string]any{
			"type":        field.fieldType,
			"description": field.description,
		}
		if field.fieldType == "array" && field.itemType != "" {
			prop["items"] = map[string]any{"type": field.itemType}
		}
		if len(field.enum) > 0 {
			prop["enum"] = field.enum
		}
		props[field.name] = prop
		if field.required {
			required = append(required, field.name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func unmarshalArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func writeMessage(w io.Writer, resp rpcResponse) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	n, err := w.Write(body)
	if err == nil && n != len(body) {
		return io.ErrShortWrite
	}
	return err
}
