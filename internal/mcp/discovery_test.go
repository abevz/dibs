package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestReadyPageIncludesProjectKey(t *testing.T) {
	f := &fakeClient{
		projectsResp: []core.Project{{ID: "p-uuid", Key: "afc"}},
		readyResp:    []core.Issue{{ID: "first", ProjectID: "p-uuid"}, {ID: "second", ProjectID: "p-uuid"}},
	}
	s := NewServer(f, "", "test")
	result, err := s.callTool(context.Background(), toolCallParams{Name: "list_ready_issues", Arguments: json.RawMessage(`{"limit":1,"offset":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	page := result.(map[string]any)
	if page["total"] != 2 {
		t.Fatalf("total = %v", page["total"])
	}
	items := page["issues"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	data, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ID         string `json:"id"`
		ProjectKey string `json:"project_key"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "second" || decoded.ProjectKey != "afc" {
		t.Fatalf("page item = %+v", decoded)
	}
}

func TestActorSchemaMatchesFallback(t *testing.T) {
	for _, tt := range []struct {
		actor string
		want  bool
	}{{"", true}, {"configured", false}} {
		s := NewServer(&fakeClient{}, tt.actor, "test")
		for _, tool := range s.tools() {
			name := tool["name"].(string)
			if name != "create_issue" && name != "update_issue" {
				continue
			}
			schema := tool["inputSchema"].(map[string]any)
			found := false
			for _, field := range schema["required"].([]string) {
				if field == "actor" {
					found = true
				}
			}
			if found != tt.want {
				t.Errorf("%s actor=%q required=%v", name, tt.actor, found)
			}
		}
	}
}
