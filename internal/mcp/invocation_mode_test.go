package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestInvocationModeToolSchemas(t *testing.T) {
	resp := NewServer(&fakeClient{}, "test", "test").handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list",
	})
	tools := resp.Result.(map[string]any)["tools"].([]map[string]any)
	want := map[string]bool{
		"claim_issue": true, "add_note": true, "handoff_issue": true,
		"close_issue": true, "operator_close_issue": true,
	}
	for _, tool := range tools {
		name := tool["name"].(string)
		if !want[name] {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		field, ok := schema["properties"].(map[string]any)["invocation_mode"].(map[string]any)
		if !ok || field["type"] != "string" || !reflect.DeepEqual(field["enum"], []string{"interactive", "scheduled", "unknown"}) {
			t.Errorf("%s invocation_mode schema = %v", name, field)
		}
		for _, required := range schema["required"].([]string) {
			if required == "invocation_mode" {
				t.Errorf("%s requires optional invocation_mode", name)
			}
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing mode-aware tools: %v", want)
	}
}

func TestInvocationModeMCPAuditEvents(t *testing.T) {
	t.Setenv("DIBS_OPERATOR_TOKEN", "scratch-operator-token")
	ctx := context.Background()
	c := startMCPTestDaemon(t)
	c.SetOperatorToken("scratch-operator-token")
	if _, err := c.CreateProject(ctx, "afc", "Test project", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := c.CreateIssue(ctx, core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: "mode test", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(c, "test", "test")
	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
		if err != nil {
			t.Fatal(err)
		}
		resp := s.handleRequest(ctx, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
		if resp == nil || resp.Error != nil {
			t.Fatalf("%s response = %+v", name, resp)
		}
		result := resp.Result.(map[string]any)
		if (result["isError"] == true) != wantError {
			t.Fatalf("%s isError = %v, want %v: %v", name, result["isError"], wantError, result["structuredContent"])
		}
		return result
	}
	modeEvent := func(id, eventType, wantMode string) {
		t.Helper()
		events, err := c.ListEvents(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].EventType != eventType {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(events[i].PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["invocation_mode"] != wantMode {
				t.Errorf("%s mode = %v, want %s", eventType, payload["invocation_mode"], wantMode)
			}
			return
		}
		t.Fatalf("no %s event for %s", eventType, id)
	}
	rejectInvalid := func(name, id string, args map[string]any) {
		t.Helper()
		before, err := c.ListEvents(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		args["invocation_mode"] = "batch"
		call(name, args, true)
		delete(args, "invocation_mode")
		after, err := c.ListEvents(ctx, id)
		if err != nil || len(after) != len(before) {
			t.Fatalf("%s invalid mode changed events: %d -> %d, error %v", name, len(before), len(after), err)
		}
	}
	claimArgs := map[string]any{"issue_id": issue.ShortID, "holder": "test"}
	rejectInvalid("claim_issue", issue.ShortID, claimArgs)
	claimArgs["invocation_mode"] = "interactive"
	claim := call("claim_issue", claimArgs, false)["structuredContent"].(core.ClaimResponse)
	modeEvent(issue.ShortID, "issue_claimed", "interactive")
	noteArgs := map[string]any{"issue_id": issue.ShortID, "body": "scheduled note"}
	rejectInvalid("add_note", issue.ShortID, noteArgs)
	noteArgs["invocation_mode"] = "scheduled"
	call("add_note", noteArgs, false)
	modeEvent(issue.ShortID, "note_added", "scheduled")
	call("add_note", map[string]any{"issue_id": issue.ShortID, "body": "omitted mode note"}, false)
	modeEvent(issue.ShortID, "note_added", "unknown")
	handoffArgs := map[string]any{
		"issue_id": issue.ShortID, "lease_token": claim.LeaseToken,
		"lease_generation": claim.LeaseGeneration, "note": "HANDOFF: mode test",
	}
	rejectInvalid("handoff_issue", issue.ShortID, handoffArgs)
	call("handoff_issue", handoffArgs, false)
	modeEvent(issue.ShortID, "note_added", "unknown")
	claim = call("claim_issue", map[string]any{"issue_id": issue.ShortID, "holder": "test", "invocation_mode": "scheduled"}, false)["structuredContent"].(core.ClaimResponse)
	modeEvent(issue.ShortID, "issue_claimed", "scheduled")
	call("handoff_issue", map[string]any{
		"issue_id": issue.ShortID, "lease_token": claim.LeaseToken,
		"lease_generation": claim.LeaseGeneration, "note": "HANDOFF: scheduled mode",
		"invocation_mode": "scheduled",
	}, false)
	modeEvent(issue.ShortID, "note_added", "scheduled")
	claim = call("claim_issue", map[string]any{"issue_id": issue.ShortID, "holder": "test", "invocation_mode": "interactive"}, false)["structuredContent"].(core.ClaimResponse)
	closeArgs := map[string]any{
		"issue_id": issue.ShortID, "resolution": "done", "expected_version": claim.Version,
		"lease_token": claim.LeaseToken, "lease_generation": claim.LeaseGeneration,
	}
	rejectInvalid("close_issue", issue.ShortID, closeArgs)
	closeArgs["invocation_mode"] = "interactive"
	call("close_issue", closeArgs, false)
	modeEvent(issue.ShortID, "issue_closed", "interactive")
	defaultIssue, err := c.CreateIssue(ctx, core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: "default modes", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defaultClaim := call("claim_issue", map[string]any{"issue_id": defaultIssue.ShortID, "holder": "test"}, false)["structuredContent"].(core.ClaimResponse)
	modeEvent(defaultIssue.ShortID, "issue_claimed", "unknown")
	call("close_issue", map[string]any{
		"issue_id": defaultIssue.ShortID, "resolution": "done", "expected_version": defaultClaim.Version,
		"lease_token": defaultClaim.LeaseToken, "lease_generation": defaultClaim.LeaseGeneration,
	}, false)
	modeEvent(defaultIssue.ShortID, "issue_closed", "unknown")

	operatorIssue, err := c.CreateIssue(ctx, core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: "operator mode", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	operatorArgs := map[string]any{
		"issue_id": operatorIssue.ShortID, "resolution": "done",
		"expected_version": operatorIssue.Version, "reason": "scratch test",
	}
	rejectInvalid("operator_close_issue", operatorIssue.ShortID, operatorArgs)
	operatorArgs["invocation_mode"] = "scheduled"
	call("operator_close_issue", operatorArgs, false)
	modeEvent(operatorIssue.ShortID, "issue_operator_closed", "scheduled")
	defaultOperatorIssue, err := c.CreateIssue(ctx, core.CreateIssueRequest{Project: "afc", ScopeKind: "project", Title: "default operator mode", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	call("operator_close_issue", map[string]any{
		"issue_id": defaultOperatorIssue.ShortID, "resolution": "done",
		"expected_version": defaultOperatorIssue.Version, "reason": "scratch test",
	}, false)
	modeEvent(defaultOperatorIssue.ShortID, "issue_operator_closed", "unknown")
}
