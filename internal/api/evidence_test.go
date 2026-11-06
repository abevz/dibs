package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestEvidenceRegisterHandoffClaimVisibility(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", filepath.Join(t.TempDir(), "durable"))
	server, db := newTestServer(t)
	ctx := t.Context()
	if _, err := sqlite.CreateProject(ctx, db, "ev", "Evidence", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := sqlite.CreateIssue(ctx, db, "ev", core.CreateIssueRequest{ScopeKind: "project", Title: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "review.json")
	if err = os.WriteFile(source, []byte(`{"result":"PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(path string, body any, want int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result json.RawMessage
		if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s status=%d want=%d body=%s", path, resp.StatusCode, want, result)
		}
		return result
	}
	base := "/v1/issues/" + issue.ShortID
	request(base+"/evidence", core.RegisterEvidenceRequest{Kind: "review", SourcePath: source, DiffHash: strings.Repeat("a", 64), Status: "PASS"}, 201)
	var first core.ClaimResponse
	if err = json.Unmarshal(request(base+"/claim", core.ClaimRequest{Holder: "first", TTLSeconds: 900}, 200), &first); err != nil {
		t.Fatal(err)
	}
	// dq-67 timeline: owner is still leased 18 seconds after failure. No
	// child-failure release is sent; a different ready-loop cannot claim it.
	request(base+"/claim", core.ClaimRequest{Holder: "other-at-18-seconds", TTLSeconds: 900}, 409)
	request(base+"/handoff", core.HandoffRequest{LeaseToken: first.LeaseToken, LeaseGeneration: first.LeaseGeneration, Note: "HANDOFF: branch feature/verified; worktree /work/ev"}, 200)
	var second core.ClaimResponse
	if err = json.Unmarshal(request(base+"/claim", core.ClaimRequest{Holder: "second", TTLSeconds: 900}, 200), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Evidence) != 1 || second.LastHandoff == nil || !strings.Contains(second.LastHandoff.Body, "feature/verified") {
		t.Fatalf("new claimer blind: %+v", second)
	}
	if second.Evidence[0].SHA256 == "" || second.Evidence[0].Path == source {
		t.Fatal("evidence not durable")
	}
	resp, err := http.Get(server.URL + base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var detail struct {
		Issue core.Issue `json:"issue"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Issue.Evidence) != 1 || detail.Issue.LastHandoff == nil {
		t.Fatal("get lost context")
	}
	request(base+"/close", core.CloseIssueRequest{Actor: "second", Resolution: "done", ExpectedVersion: second.Version, LeaseToken: second.LeaseToken, LeaseGeneration: second.LeaseGeneration, RequireEvidence: []string{"log"}, DiffHash: strings.Repeat("a", 64)}, 400)
	request(base+"/close", core.CloseIssueRequest{Actor: "second", Resolution: "done", ExpectedVersion: second.Version, LeaseToken: second.LeaseToken, LeaseGeneration: second.LeaseGeneration, RequireEvidence: []string{"review"}, DiffHash: strings.Repeat("a", 64), Note: fmt.Sprintf("evidence %s", second.Evidence[0].ID)}, 200)
	request("/v1/evidence/prune", core.EvidenceMaintenanceRequest{Days: 0}, 400)
	request("/v1/issues/unknown/evidence", core.RegisterEvidenceRequest{Kind: "log", SourcePath: source}, 404)
}
