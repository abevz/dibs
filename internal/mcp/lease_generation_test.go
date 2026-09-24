package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestLifecycleToolSchemasRequireLeaseGeneration(t *testing.T) {
	s := NewServer(&fakeClient{}, "test", "test")
	resp := s.handleRequest(context.Background(), rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list",
	})
	tools := resp.Result.(map[string]any)["tools"].([]map[string]any)
	want := map[string][]string{
		"heartbeat_issue": {"issue_id", "lease_generation", "lease_token"},
		"handoff_issue":   {"issue_id", "lease_generation", "lease_token", "note"},
		"close_issue":     {"expected_version", "issue_id", "lease_generation", "lease_token", "resolution"},
	}
	for _, tool := range tools {
		name := tool["name"].(string)
		required, ok := want[name]
		if !ok {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		field, ok := properties["lease_generation"].(map[string]any)
		if !ok || field["type"] != "integer" {
			t.Errorf("%s lease_generation schema = %v", name, properties["lease_generation"])
		}
		got := append([]string(nil), schema["required"].([]string)...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, required) {
			t.Errorf("%s required = %v, want %v", name, got, required)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("lifecycle tools absent: %v", want)
	}
}

func TestHeartbeatSchemaExplainsHistoricalReplay(t *testing.T) {
	s := NewServer(&fakeClient{}, "test", "test")
	for _, tool := range s.tools() {
		if tool["name"] != "heartbeat_issue" {
			continue
		}
		if !strings.Contains(tool["description"].(string), "historical expiry") {
			t.Fatalf("heartbeat description = %q", tool["description"])
		}
		fields := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		id := fields["operation_id"].(map[string]any)
		if !strings.Contains(id["description"].(string), "never for lease liveness") {
			t.Fatalf("operation_id description = %q", id["description"])
		}
		return
	}
	t.Fatal("heartbeat_issue schema missing")
}

func TestLifecycleToolsFenceGenerationAgainstDaemon(t *testing.T) {
	ctx := context.Background()
	c := startMCPTestDaemon(t)
	if _, err := c.CreateProject(ctx, "afc", "Test project", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := c.CreateIssue(ctx, core.CreateIssueRequest{
		Project: "afc", ScopeKind: "project", Title: "MCP lifecycle", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(c, "test", "test")
	call := func(name string, args map[string]any, wantError bool) {
		t.Helper()
		params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
		if err != nil {
			t.Fatal(err)
		}
		resp := s.handleRequest(ctx, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
		if resp == nil || resp.Error != nil {
			t.Fatalf("%s RPC response = %+v", name, resp)
		}
		result := resp.Result.(map[string]any)
		if (result["isError"] == true) != wantError {
			t.Fatalf("%s isError = %v, want %v: %v", name, result["isError"], wantError, result["structuredContent"])
		}
	}
	claim, err := c.ClaimIssue(ctx, issue.ShortID, "test", 900)
	if err != nil {
		t.Fatal(err)
	}
	handoff := map[string]any{"issue_id": issue.ShortID, "lease_token": claim.LeaseToken, "note": "HANDOFF: test"}
	call("handoff_issue", handoff, true) // Zero generation must fail locally.
	handoff["lease_generation"] = claim.LeaseGeneration + 1
	call("handoff_issue", handoff, true) // Stale generation must fail at the daemon/store seam.
	handoff["lease_generation"] = claim.LeaseGeneration
	call("handoff_issue", handoff, false)
	got, lease, err := c.GetIssue(ctx, issue.ShortID)
	if err != nil || got.Status != "open" || lease != nil {
		t.Fatalf("after handoff issue/lease/error = %s/%+v/%v", got.Status, lease, err)
	}
	claim, err = c.ClaimIssue(ctx, issue.ShortID, "test", 900)
	if err != nil {
		t.Fatal(err)
	}
	closeArgs := map[string]any{
		"issue_id": issue.ShortID, "resolution": "done", "expected_version": claim.Version,
		"lease_token": claim.LeaseToken, "note": "done",
	}
	call("close_issue", closeArgs, true)
	closeArgs["lease_generation"] = claim.LeaseGeneration - 1
	call("close_issue", closeArgs, true)
	closeArgs["lease_generation"] = claim.LeaseGeneration
	call("close_issue", closeArgs, false)
	got, lease, err = c.GetIssue(ctx, issue.ShortID)
	if err != nil || got.Status != "done" || lease != nil {
		t.Fatalf("after close issue/lease/error = %s/%+v/%v", got.Status, lease, err)
	}
}
