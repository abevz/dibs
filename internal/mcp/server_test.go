package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

type fakeClient struct {
	healthResp             core.Health
	getIssueResp           core.Issue
	getLeaseResp           *core.IssueLease
	readyResp              []core.Issue
	claimResp              core.ClaimResponse
	heartbeatResp          string
	handoffResp            core.HandoffResponse
	noteResp               core.Note
	notesResp              []core.Note
	eventsResp             []core.Event
	closeResp              core.CloseIssueResult
	operatorCloseResp      core.CloseIssueResult
	operatorReopenResp     core.Issue
	operatorReleaseResp    core.Issue
	lastIssueID            string
	lastHolder             string
	lastTTL                int
	lastSessionID          string
	lastLeaseToken         string
	lastHandoffNote        string
	lastNoteAuthor         string
	lastNoteBody           string
	lastCloseReq           core.CloseIssueRequest
	lastOperatorCloseReq   core.OperatorCloseIssueRequest
	lastOperatorReopenReq  core.OperatorReopenIssueRequest
	lastOperatorReleaseReq core.OperatorReleaseIssueRequest
	lastAddTagReq          core.AddTagRequest
	lastRemoveTagTag       string
	lastRemoveTagActor     string
	lastReadyTags          []string
}

func (f *fakeClient) Health(context.Context) (core.Health, error) { return f.healthResp, nil }
func (f *fakeClient) GetIssue(context.Context, string) (core.Issue, *core.IssueLease, error) {
	return f.getIssueResp, f.getLeaseResp, nil
}
func (f *fakeClient) ListReadyIssues(_ context.Context, _, _ string, tags []string) ([]core.Issue, error) {
	f.lastReadyTags = tags
	return f.readyResp, nil
}
func (f *fakeClient) ClaimIssue(_ context.Context, issueID, holder string, ttlSeconds int) (core.ClaimResponse, error) {
	f.lastIssueID = issueID
	f.lastHolder = holder
	f.lastTTL = ttlSeconds
	return f.claimResp, nil
}
func (f *fakeClient) ClaimIssueWithSession(_ context.Context, issueID, holder string, ttlSeconds int, sessionID string) (core.ClaimResponse, error) {
	f.lastIssueID = issueID
	f.lastHolder = holder
	f.lastTTL = ttlSeconds
	f.lastSessionID = sessionID
	return f.claimResp, nil
}
func (f *fakeClient) HeartbeatLease(_ context.Context, issueID, leaseToken string, leaseGeneration int64, ttlSeconds int) (string, error) {
	f.lastIssueID = issueID
	f.lastLeaseToken = leaseToken
	f.lastTTL = ttlSeconds
	return f.heartbeatResp, nil
}
func (f *fakeClient) HandoffLease(_ context.Context, issueID, leaseToken string, leaseGeneration int64, note string) (core.HandoffResponse, error) {
	f.lastIssueID = issueID
	f.lastLeaseToken = leaseToken
	f.lastHandoffNote = note
	return f.handoffResp, nil
}
func (f *fakeClient) CreateNote(_ context.Context, issueID, author, body string) (core.Note, error) {
	f.lastIssueID = issueID
	f.lastNoteAuthor = author
	f.lastNoteBody = body
	return f.noteResp, nil
}
func (f *fakeClient) ListNotes(context.Context, string) ([]core.Note, error) { return f.notesResp, nil }
func (f *fakeClient) ListEvents(context.Context, string) ([]core.Event, error) {
	return f.eventsResp, nil
}
func (f *fakeClient) CloseIssue(_ context.Context, issueID string, req core.CloseIssueRequest) (core.CloseIssueResult, error) {
	f.lastIssueID = issueID
	f.lastCloseReq = req
	return f.closeResp, nil
}
func (f *fakeClient) OperatorCloseIssue(_ context.Context, issueID string, req core.OperatorCloseIssueRequest) (core.CloseIssueResult, error) {
	f.lastIssueID = issueID
	f.lastOperatorCloseReq = req
	return f.operatorCloseResp, nil
}
func (f *fakeClient) OperatorReopenIssue(_ context.Context, issueID string, req core.OperatorReopenIssueRequest) (core.Issue, error) {
	f.lastIssueID = issueID
	f.lastOperatorReopenReq = req
	return f.operatorReopenResp, nil
}

func (f *fakeClient) OperatorReleaseIssue(_ context.Context, issueID string, req core.OperatorReleaseIssueRequest) (core.Issue, error) {
	f.lastIssueID = issueID
	f.lastOperatorReleaseReq = req
	return f.operatorReleaseResp, nil
}

func (f *fakeClient) AddTag(_ context.Context, issueID string, req core.AddTagRequest) error {
	f.lastIssueID = issueID
	f.lastAddTagReq = req
	return nil
}

func (f *fakeClient) RemoveTag(_ context.Context, issueID, tag, actor string) error {
	f.lastIssueID = issueID
	f.lastRemoveTagTag = tag
	f.lastRemoveTagActor = actor
	return nil
}

func TestHandleInitialize(t *testing.T) {
	s := NewServer(&fakeClient{}, "tester", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-03-26"}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected initialize result, got %+v", resp)
	}
	result := resp.Result.(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
}

func TestToolsListIncludesCoordinatorTools(t *testing.T) {
	s := NewServer(&fakeClient{}, "tester", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "tools/list",
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected tools/list result, got %+v", resp)
	}
	result := resp.Result.(map[string]any)
	tools := result["tools"].([]map[string]any)
	if len(tools) < 8 {
		t.Fatalf("expected several tools, got %d", len(tools))
	}
}

func TestToolCallClaimIssueUsesDefaultActor(t *testing.T) {
	fake := &fakeClient{claimResp: core.ClaimResponse{LeaseToken: "tok"}}
	s := NewServer(fake, "codex-actor", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("3"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"claim_issue","arguments":{"issue_id":"afc-28","ttl_seconds":900,"session_id":"session-28"}}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected tool result, got %+v", resp)
	}
	if fake.lastHolder != "codex-actor" {
		t.Fatalf("holder = %q, want %q", fake.lastHolder, "codex-actor")
	}
	if fake.lastSessionID != "session-28" {
		t.Fatalf("session_id = %q, want %q", fake.lastSessionID, "session-28")
	}
	callResult := resp.Result.(map[string]any)
	if callResult["isError"] != nil {
		t.Fatalf("unexpected tool error result: %+v", callResult)
	}
}

func TestToolCallHandoffIssueUsesAtomicClientPath(t *testing.T) {
	fake := &fakeClient{handoffResp: core.HandoffResponse{Note: core.Note{ID: "n1"}}}
	s := NewServer(fake, "codex-actor", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("4"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"handoff_issue","arguments":{"issue_id":"afc-52","lease_token":"lease","lease_generation":1,"note":"HANDOFF: continue after review"}}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected handoff tool result, got %+v", resp)
	}
	if fake.lastIssueID != "afc-52" || fake.lastLeaseToken != "lease" || fake.lastHandoffNote != "HANDOFF: continue after review" {
		t.Fatalf("unexpected handoff call: issue=%q token=%q note=%q", fake.lastIssueID, fake.lastLeaseToken, fake.lastHandoffNote)
	}

	invalid := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("5"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"handoff_issue","arguments":{"issue_id":"afc-52","lease_token":"lease","lease_generation":1,"note":"continue after review"}}`),
	})
	result := invalid.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("malformed handoff unexpectedly succeeded: %+v", result)
	}
}

func TestToolCallAddTagUsesDefaultActor(t *testing.T) {
	fake := &fakeClient{}
	s := NewServer(fake, "codex-actor", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("6"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"add_tag","arguments":{"issue_id":"afc-28","tag":"area/frontend"}}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected tool result, got %+v", resp)
	}
	if fake.lastIssueID != "afc-28" || fake.lastAddTagReq.Tag != "area/frontend" || fake.lastAddTagReq.Actor != "codex-actor" {
		t.Fatalf("unexpected add_tag call: issue=%q req=%+v", fake.lastIssueID, fake.lastAddTagReq)
	}
	callResult := resp.Result.(map[string]any)
	if callResult["isError"] != nil {
		t.Fatalf("unexpected tool error result: %+v", callResult)
	}

	invalid := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("7"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"add_tag","arguments":{"issue_id":"afc-28"}}`),
	})
	result := invalid.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("add_tag without tag unexpectedly succeeded: %+v", result)
	}
}

func TestToolCallRemoveTagUsesDefaultActor(t *testing.T) {
	fake := &fakeClient{}
	s := NewServer(fake, "codex-actor", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("8"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"remove_tag","arguments":{"issue_id":"afc-28","tag":"area/frontend"}}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected tool result, got %+v", resp)
	}
	if fake.lastIssueID != "afc-28" || fake.lastRemoveTagTag != "area/frontend" || fake.lastRemoveTagActor != "codex-actor" {
		t.Fatalf("unexpected remove_tag call: issue=%q tag=%q actor=%q", fake.lastIssueID, fake.lastRemoveTagTag, fake.lastRemoveTagActor)
	}
	callResult := resp.Result.(map[string]any)
	if callResult["isError"] != nil {
		t.Fatalf("unexpected tool error result: %+v", callResult)
	}
}

func TestToolCallCloseIssuePassesStructuredMetadata(t *testing.T) {
	fake := &fakeClient{closeResp: core.CloseIssueResult{Status: "closed", Branch: "codex/afc-28"}}
	s := NewServer(fake, "codex-actor", "0055")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("4"),
		Method:  "tools/call",
		Params: json.RawMessage(`{
			"name":"close_issue",
			"arguments":{
				"issue_id":"afc-28",
				"resolution":"done",
				"expected_version":2,
				"lease_token":"lease",
				"lease_generation":3,
				"branch":"codex/afc-28",
				"pr_url":"https://example/pr/28",
				"commit_sha":"ddb6d05"
			}
		}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("expected close tool result, got %+v", resp)
	}
	if fake.lastCloseReq.Branch != "codex/afc-28" || fake.lastCloseReq.PRURL != "https://example/pr/28" || fake.lastCloseReq.CommitSHA != "ddb6d05" || fake.lastCloseReq.LeaseGeneration != 3 {
		t.Fatalf("unexpected close request: %+v", fake.lastCloseReq)
	}
}

func TestOperatorToolsUseExplicitTokenlessRequests(t *testing.T) {
	fake := &fakeClient{
		operatorCloseResp:   core.CloseIssueResult{Status: "closed", Resolution: "done"},
		operatorReopenResp:  core.Issue{Status: "open"},
		operatorReleaseResp: core.Issue{Status: "open"},
	}
	s := NewServer(fake, "operator", "0055")

	for _, test := range []struct {
		name string
		args string
	}{
		{
			name: "operator close",
			args: `{"name":"operator_close_issue","arguments":{"issue_id":"afc-50","resolution":"done","expected_version":1,"reason":"parent complete"}}`,
		},
		{
			name: "operator reopen",
			args: `{"name":"operator_reopen_issue","arguments":{"issue_id":"afc-50","expected_version":2,"reason":"needs follow-up"}}`,
		},
		{
			name: "operator release",
			args: `{"name":"operator_release_issue","arguments":{"issue_id":"afc-50","expected_version":3,"reason":"agent crashed, lease token lost"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := s.handleRequest(context.Background(), rpcRequest{
				JSONRPC: "2.0", ID: json.RawMessage("5"), Method: "tools/call", Params: json.RawMessage(test.args),
			})
			if resp == nil || resp.Error != nil {
				t.Fatalf("expected operator tool result, got %+v", resp)
			}
		})
	}
	if fake.lastOperatorCloseReq.Reason != "parent complete" || fake.lastOperatorCloseReq.Actor != "operator" {
		t.Fatalf("unexpected operator close request: %+v", fake.lastOperatorCloseReq)
	}
	if fake.lastOperatorReopenReq.Reason != "needs follow-up" || fake.lastOperatorReopenReq.Actor != "operator" {
		t.Fatalf("unexpected operator reopen request: %+v", fake.lastOperatorReopenReq)
	}
	if fake.lastOperatorReleaseReq.Reason != "agent crashed, lease token lost" || fake.lastOperatorReleaseReq.Actor != "operator" {
		t.Fatalf("unexpected operator release request: %+v", fake.lastOperatorReleaseReq)
	}

	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage("6"), Method: "tools/call",
		Params: json.RawMessage(`{"name":"operator_close_issue","arguments":{"issue_id":"afc-50","resolution":"done","expected_version":1,"reason":"parent complete","lease_token":"fake"}}`),
	})
	result := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("operator tool accepted a lease token: %+v", result)
	}
}

func TestRunProcessesNewlineMessages(t *testing.T) {
	fake := &fakeClient{healthResp: core.Health{Name: "af-coordinator", Status: "ok"}}
	s := NewServer(fake, "tester", "0055")

	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"health","arguments":{}}}`
	input := request + "\n"
	var out bytes.Buffer
	if err := s.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(out.String(), "Content-Length:") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("expected newline JSON response, got %q", out.String())
	}
	if !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("expected health payload in response, got %q", out.String())
	}
}
