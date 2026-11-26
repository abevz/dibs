package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCloseLatestUsesCurrentVersionAndFencesLease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := newTestDB(t)
	if _, err := CreateProject(ctx, db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(ctx, db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(ctx, db, issue.ID, "worker", 900)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateIssue(ctx, db, issue.ID, core.UpdateIssueRequest{
		Title: "Current metadata", Actor: "worker", ExpectedVersion: claim.Version,
		LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := core.CloseIssueRequest{
		Resolution: "done", Actor: "worker", ExpectedVersion: core.LatestIssueVersion,
		LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, OperationID: "latest-close-0001",
	}
	for _, tc := range []struct {
		name   string
		change func(*core.CloseIssueRequest)
		code   string
	}{
		{"wrong token", func(req *core.CloseIssueRequest) { req.LeaseToken = "other" }, core.ErrLeaseExpired},
		{"wrong generation", func(req *core.CloseIssueRequest) { req.LeaseGeneration++ }, core.ErrLeaseExpired},
		{"stale numeric version", func(req *core.CloseIssueRequest) { req.ExpectedVersion = claim.Version }, core.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := req
			tc.change(&attempt)
			_, err := CloseIssue(ctx, db, issue.ID, attempt)
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("close error = %v, want %s", err, tc.code)
			}
			if apiErr.Code == core.ErrConflict {
				for _, want := range []string{fmt.Sprintf("current version is %d", updated.Version),
					fmt.Sprintf("current lease generation is %d", claim.LeaseGeneration)} {
					if !strings.Contains(apiErr.Message, want) {
						t.Errorf("conflict %q missing %q", apiErr.Message, want)
					}
				}
			}
			got, lease, err := GetIssue(ctx, db, issue.ID)
			if err != nil || got.Version != updated.Version || lease == nil || lease.LeaseGeneration != claim.LeaseGeneration {
				t.Fatalf("rejected close changed issue or lease: version=%d lease present=%t err=%v", got.Version, lease != nil, err)
			}
		})
	}
	closed, err := CloseIssue(ctx, db, issue.ID, req)
	if err != nil {
		t.Fatalf("latest close = %+v, %v", closed, err)
	}
	replay, err := CloseIssue(ctx, db, issue.ID, req)
	if err != nil || replay != closed {
		t.Fatalf("latest replay = %+v, %v, want %+v", replay, err, closed)
	}
	got, lease, err := GetIssue(ctx, db, issue.ID)
	if err != nil || got.Title != updated.Title || got.Version != updated.Version+1 || got.Status != "done" || lease != nil {
		t.Fatalf("issue after latest close: title=%q status=%q lease present=%t err=%v", got.Title, got.Status, lease != nil, err)
	}
}
