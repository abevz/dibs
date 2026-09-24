package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/client"
)

// mcpWireCall deliberately uses raw newline JSON rather than server framing helpers.
func mcpWireCall(t *testing.T, s *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := s.Run(context.Background(), bytes.NewReader(append(request, '\n')), &stdout); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) || bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("invalid NDJSON response: %q", stdout.String())
	}
	var response struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSuffix(stdout.Bytes(), []byte("\n")), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 1 || response.Error != nil {
		t.Fatalf("invalid JSON-RPC response: %+v", response)
	}
	return response.Result
}

func operationContent(t *testing.T, result map[string]any, id string) map[string]any {
	t.Helper()
	if result["isError"] == true {
		t.Fatalf("unexpected tool error: %v", result)
	}
	content, ok := result["structuredContent"].(map[string]any)
	if !ok || content["operation_id"] != id {
		t.Fatalf("operation_id = %v, want %s", result["structuredContent"], id)
	}
	return content
}

func TestMCPOperationIDSchemas(t *testing.T) {
	tools := NewServer(&fakeClient{}, "test", "test").tools()
	want := map[string]bool{
		"create_issue": true, "claim_issue": true, "heartbeat_issue": true,
		"release_issue": true, "update_issue": true, "handoff_issue": true, "close_issue": true,
	}
	for _, tool := range tools {
		name := tool["name"].(string)
		if !want[name] {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		field, ok := schema["properties"].(map[string]any)["operation_id"].(map[string]any)
		if !ok || field["type"] != "string" {
			t.Errorf("%s missing operation_id: %v", name, schema)
		}
		for _, required := range schema["required"].([]string) {
			if required == "operation_id" {
				t.Errorf("%s requires optional operation_id", name)
			}
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestMCPOperationIDWireReplayAgainstTestDaemon(t *testing.T) {
	ctx := context.Background()
	c := startMCPTestDaemon(t)
	if _, err := c.CreateProject(ctx, "afc", "Test project", ""); err != nil {
		t.Fatal(err)
	}
	s := NewServer(c, "test", "test")
	create := map[string]any{"project": "afc", "scope_kind": "project", "title": "MCP retry", "operation_id": "mcp-create-0001"}
	first := operationContent(t, mcpWireCall(t, s, "create_issue", create), "mcp-create-0001")
	second := operationContent(t, mcpWireCall(t, s, "create_issue", create), "mcp-create-0001")
	if !equalJSON(first, second) {
		t.Fatalf("create replay changed result: %v != %v", first, second)
	}
	issue := first["issue"].(map[string]any)
	id := issue["short_id"].(string)
	create["title"] = "changed"
	conflict := mcpWireCall(t, s, "create_issue", create)
	if conflict["isError"] != true || conflict["structuredContent"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatalf("changed create did not conflict: %v", conflict)
	}
	create["title"] = "MCP retry"

	claimArgs := map[string]any{"issue_id": id, "holder": "test", "operation_id": "mcp-claim-0001"}
	claimOne := operationContent(t, mcpWireCall(t, s, "claim_issue", claimArgs), "mcp-claim-0001")
	claimTwo := operationContent(t, mcpWireCall(t, s, "claim_issue", claimArgs), "mcp-claim-0001")
	if !equalJSON(claimOne, claimTwo) {
		t.Fatalf("claim replay changed result: %v != %v", claimOne, claimTwo)
	}
	claimArgs["holder"] = "changed"
	conflict = mcpWireCall(t, s, "claim_issue", claimArgs)
	if conflict["isError"] != true || conflict["structuredContent"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatalf("changed claim did not conflict: %v", conflict)
	}
	claimArgs["holder"] = "test"
	token := claimOne["lease_token"].(string)
	generation := claimOne["lease_generation"].(float64)
	version := claimOne["version"].(float64)
	heartbeatArgs := map[string]any{"issue_id": id, "lease_token": token, "lease_generation": generation, "ttl_seconds": 600, "operation_id": "mcp-heartbeat-0001"}
	heartbeatOne := operationContent(t, mcpWireCall(t, s, "heartbeat_issue", heartbeatArgs), "mcp-heartbeat-0001")
	heartbeatTwo := operationContent(t, mcpWireCall(t, s, "heartbeat_issue", heartbeatArgs), "mcp-heartbeat-0001")
	if !equalJSON(heartbeatOne, heartbeatTwo) {
		t.Fatalf("heartbeat replay changed expiry: %v != %v", heartbeatOne, heartbeatTwo)
	}
	heartbeatArgs["ttl_seconds"] = 601
	conflict = mcpWireCall(t, s, "heartbeat_issue", heartbeatArgs)
	if conflict["isError"] != true || conflict["structuredContent"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatalf("changed heartbeat did not conflict: %v", conflict)
	}

	update := map[string]any{"issue_id": id, "expected_version": version, "title": "Updated via MCP", "lease_token": token, "lease_generation": generation, "operation_id": "mcp-update-0001"}
	updatedOne := operationContent(t, mcpWireCall(t, s, "update_issue", update), "mcp-update-0001")
	updatedTwo := operationContent(t, mcpWireCall(t, s, "update_issue", update), "mcp-update-0001")
	if !equalJSON(updatedOne, updatedTwo) {
		t.Fatalf("update replay changed result: %v != %v", updatedOne, updatedTwo)
	}
	handoff := map[string]any{"issue_id": id, "lease_token": token, "lease_generation": generation, "note": "HANDOFF: MCP retry", "operation_id": "mcp-handoff-0001"}
	handoffOne := operationContent(t, mcpWireCall(t, s, "handoff_issue", handoff), "mcp-handoff-0001")
	handoffTwo := operationContent(t, mcpWireCall(t, s, "handoff_issue", handoff), "mcp-handoff-0001")
	if !equalJSON(handoffOne, handoffTwo) {
		t.Fatalf("handoff replay changed result: %v != %v", handoffOne, handoffTwo)
	}
	claimArgs["operation_id"] = "mcp-claim-0002"
	claimThree := operationContent(t, mcpWireCall(t, s, "claim_issue", claimArgs), "mcp-claim-0002")
	release := map[string]any{"issue_id": id, "lease_token": claimThree["lease_token"], "lease_generation": claimThree["lease_generation"], "operation_id": "mcp-release-0001"}
	releaseOne := operationContent(t, mcpWireCall(t, s, "release_issue", release), "mcp-release-0001")
	releaseTwo := operationContent(t, mcpWireCall(t, s, "release_issue", release), "mcp-release-0001")
	if !equalJSON(releaseOne, releaseTwo) {
		t.Fatalf("release replay changed result: %v != %v", releaseOne, releaseTwo)
	}
	claimArgs["operation_id"] = "mcp-claim-0003"
	claimFour := operationContent(t, mcpWireCall(t, s, "claim_issue", claimArgs), "mcp-claim-0003")
	closeArgs := map[string]any{"issue_id": id, "resolution": "done", "expected_version": claimFour["version"], "lease_token": claimFour["lease_token"], "lease_generation": claimFour["lease_generation"], "note": "done", "operation_id": "mcp-close-0001"}
	closeOne := operationContent(t, mcpWireCall(t, s, "close_issue", closeArgs), "mcp-close-0001")
	closeTwo := operationContent(t, mcpWireCall(t, s, "close_issue", closeArgs), "mcp-close-0001")
	if !equalJSON(closeOne, closeTwo) {
		t.Fatalf("close replay changed result: %v != %v", closeOne, closeTwo)
	}
	events, err := c.ListEvents(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"issue_created", "issue_claimed", "issue_claimed", "issue_claimed", "issue_closed"}
	for _, eventType := range wantEvents {
		count := 0
		for _, event := range events {
			if event.EventType == eventType {
				count++
			}
		}
		if eventType == "issue_claimed" {
			if count != 3 {
				t.Errorf("claim events = %d, want 3", count)
			}
		} else if count != 1 {
			t.Errorf("%s events = %d, want 1", eventType, count)
		}
	}
	for _, event := range events {
		if strings.Contains(event.PayloadJSON, "mcp-") {
			t.Fatalf("operation ID leaked to event: %s", event.PayloadJSON)
		}
	}
}

func TestMCPOperationIDGeneratedAndValidated(t *testing.T) {
	f := &fakeClient{}
	s := NewServer(f, "test", "test")
	create := map[string]any{"project": "afc", "scope_kind": "project", "title": "generated"}
	result := mcpWireCall(t, s, "create_issue", create)
	content := result["structuredContent"].(map[string]any)
	id, ok := content["operation_id"].(string)
	if !ok || len(id) != 36 || f.lastCreateReq.OperationID != id {
		t.Fatalf("generated ID was not returned and forwarded: %v / %v", content, f.lastCreateReq)
	}
	create["operation_id"] = "short"
	result = mcpWireCall(t, s, "create_issue", create)
	if result["isError"] != true || f.lastCreateReq.OperationID != id {
		t.Fatalf("invalid operation ID reached client: %v", result)
	}
	errorResult := toolErrorResult(operationToolError{id: id, err: &client.ClientError{Code: "idempotency_conflict", Message: "changed request"}})
	got := errorResult["structuredContent"].(map[string]any)
	if got["operation_id"] != id || got["code"] != "idempotency_conflict" {
		t.Fatalf("operation error lost retry ID or typed code: %v", got)
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
