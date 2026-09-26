package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestClaimConflictBodyIncludesPublicLeaseDetails(t *testing.T) {
	server, db := newTestServer(t)
	ctx := context.Background()
	if _, err := sqlite.CreateProject(ctx, db, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := sqlite.CreateIssue(ctx, db, "demo", core.CreateIssueRequest{ScopeKind: "project", Title: "Race"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := sqlite.ClaimIssueWithOperation(ctx, db, issue.ID, core.ClaimRequest{Holder: "codex", TTLSeconds: 3600, SessionID: "dibs-run:v1:arch:1234"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/v1/issues/"+issue.ShortID+"/claim", "application/json", strings.NewReader(`{"holder":"claude"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope core.APIErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict || resp.Header.Get("X-Dibs-Error-Code") != core.ErrLeaseHeld || envelope.Error.Details == nil {
		t.Fatalf("status/error = %d %+v", resp.StatusCode, envelope.Error)
	}
	details := envelope.Error.Details
	if details.ShortID != issue.ShortID || details.Holder != "codex" || details.LeaseExpiresAt != first.ExpiresAt || details.LeasePID != 1234 || details.LeaseHost != "arch" {
		t.Fatalf("details = %+v", details)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), first.LeaseToken) || strings.Contains(string(encoded), issue.ID) {
		t.Fatalf("response disclosed token or internal UUID: %s", encoded)
	}
}
