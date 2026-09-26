package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestClaimConflictReportsCurrentLeaseWithoutToken(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateProject(ctx, db, "demo", "Demo", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(ctx, db, "demo", core.CreateIssueRequest{ScopeKind: "project", Title: "Race"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ClaimIssueWithOperation(ctx, db, issue.ID, core.ClaimRequest{Holder: "codex", TTLSeconds: 3600, SessionID: "dibs-claim:v1:arch:1234"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ClaimIssueWithOperation(ctx, db, issue.ID, core.ClaimRequest{Holder: "claude", TTLSeconds: 3600})
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld || apiErr.Details == nil {
		t.Fatalf("error = %v, want structured lease_held", err)
	}
	details := apiErr.Details
	if details.ShortID != issue.ShortID || details.Holder != "codex" || details.LeaseExpiresAt != first.ExpiresAt || details.LeasePID != 1234 || details.LeaseHost != "arch" {
		t.Fatalf("details = %+v", details)
	}
	encoded, err := json.Marshal(apiErr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), first.LeaseToken) || strings.Contains(string(encoded), issue.ID) {
		t.Fatalf("conflict disclosed a secret or internal UUID: %s", encoded)
	}
}
