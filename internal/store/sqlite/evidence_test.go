package sqlite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
)

func TestEvidenceLifecycleAndRetention(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", filepath.Join(t.TempDir(), "durable"))
	db := newTestDB(t)
	s := NewStore(db)
	ctx := t.Context()
	if _, err := CreateProject(ctx, db, "ev", "Evidence", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(ctx, db, "ev", core.CreateIssueRequest{ScopeKind: "project", Title: "Evidence work"})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "review.json")
	if err = os.WriteFile(source, []byte(`{"verdict":"PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	diff := strings.Repeat("a", 64)
	e, err := s.RegisterEvidence(ctx, issue.ID, core.RegisterEvidenceRequest{SourcePath: source, Kind: "review", Status: "PASS", DiffHash: diff})
	if err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	if err = s.ExportJSONL(ctx, &exported); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported.String(), `"type":"evidence"`) || !strings.Contains(exported.String(), e.SHA256) {
		t.Fatalf("evidence missing from export: %s", exported.String())
	}
	if e.SHA256 == "" || e.Path == source || e.Size == 0 {
		t.Fatalf("bad evidence: %+v", e)
	}
	info, err := os.Stat(e.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	claim, err := ClaimIssue(ctx, db, issue.ID, "first", 900)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := HandoffLease(ctx, db, issue.ID, core.HandoffRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "HANDOFF: verified work; branch feature/evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(handoff.Evidence) != 1 || handoff.Evidence[0].SHA256 != e.SHA256 {
		t.Fatalf("handoff lost evidence: %+v", handoff)
	}
	claim, err = ClaimIssue(ctx, db, issue.ID, "second", 900)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GetIssue(ctx, db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Evidence) != 1 || got.LastHandoff == nil || !strings.Contains(got.LastHandoff.Body, "verified work") {
		t.Fatalf("claim context missing: %+v", got)
	}
	req := core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, RequireEvidence: []string{"review"}, DiffHash: strings.Repeat("b", 64), Note: "verified"}
	if _, err = CloseIssue(ctx, db, issue.ID, req); err == nil {
		t.Fatal("closed with evidence for wrong diff")
	}
	req.DiffHash = diff
	if err = os.WriteFile(e.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = CloseIssue(ctx, db, issue.ID, req); err == nil {
		t.Fatal("closed with tampered evidence")
	}
	if err = os.WriteFile(e.Path, []byte(`{"verdict":"PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Active issues never qualify, regardless of evidence age.
	items, err := s.PruneEvidence(ctx, core.EvidenceMaintenanceRequest{Days: 1, Apply: true})
	if err != nil || len(items) != 0 {
		t.Fatalf("active prune: %+v %v", items, err)
	}
	if _, err = CloseIssue(ctx, db, issue.ID, req); err != nil {
		t.Fatal(err)
	}
	// RFC3339 UTC matches production timestamp contract.
	if _, err = db.Exec(`UPDATE issues SET closed_at='2026-01-01T00:00:00Z' WHERE id=?`, issue.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.PruneEvidence(ctx, core.EvidenceMaintenanceRequest{Days: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("preview: %v %v", items, err)
	}
	if _, err = os.Stat(e.Path); err != nil {
		t.Fatal("preview removed file")
	}
	items, err = s.PruneEvidence(ctx, core.EvidenceMaintenanceRequest{Days: 1, Apply: true})
	if err != nil || len(items) != 1 || items[0].PrunedAt == "" {
		t.Fatalf("prune: %v %v", items, err)
	}
	if _, err = os.Stat(e.Path); !os.IsNotExist(err) {
		t.Fatalf("file not pruned: %v", err)
	}
	items, err = s.ListEvidence(ctx, issue.ID)
	if err != nil || len(items) != 1 || items[0].SHA256 != e.SHA256 || items[0].PrunedAt == "" {
		t.Fatalf("summary lost: %v %v", items, err)
	}
}

func TestEvidenceRejectsInvalidRegistration(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", t.TempDir())
	db := newTestDB(t)
	s := NewStore(db)
	tests := []core.RegisterEvidenceRequest{
		{SourcePath: "/tmp/x", Kind: "review"},
		{SourcePath: "/tmp/x", Kind: "unknown", DiffHash: strings.Repeat("a", 64)},
		{SourcePath: "/tmp/x", Kind: "log", DiffHash: "short"},
		{SourcePath: "/tmp/x", Kind: "log", CommitSHA: "HEAD"},
		{SourcePath: "/tmp/x", Kind: "log", DiffHash: strings.Repeat("a", 64), Status: "PASS"},
	}
	for _, req := range tests {
		if _, err := s.RegisterEvidence(t.Context(), "unused", req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}

// Slow evidence verification must not let a now-expired holder close using
// the timestamp captured before hashing began.
func TestEvidenceCloseRechecksExpiryAfterVerification(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", t.TempDir())
	db := newTestDB(t)
	s := NewStore(db)
	ctx := t.Context()
	if _, err := CreateProject(ctx, db, "slow", "Slow", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(ctx, db, "slow", core.CreateIssueRequest{ScopeKind: "project", Title: "Slow verification"})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "log")
	if err = os.WriteFile(source, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	diff := strings.Repeat("c", 64)
	if _, err = s.RegisterEvidence(ctx, issue.ID, core.RegisterEvidenceRequest{SourcePath: source, Kind: "log", DiffHash: diff}); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(ctx, db, issue.ID, "owner", 900)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(2 * time.Second).Truncate(time.Second)
	if _, err = db.Exec(`UPDATE leases SET expires_at=? WHERE issue_id=?`, expiry.Format(time.RFC3339), issue.ID); err != nil {
		t.Fatal(err)
	}
	delayed := context.WithValue(ctx, coordinationProofHookContextKey{}, coordinationProofHook(func(point coordinationProofPoint) {
		if point == coordinationProofCloseEvidenceVerified {
			time.Sleep(time.Until(expiry) + 20*time.Millisecond)
		}
	}))
	_, err = CloseIssue(delayed, db, issue.ID, core.CloseIssueRequest{Resolution: "done", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, RequireEvidence: []string{"log"}, DiffHash: diff})
	if err == nil {
		t.Fatal("expired owner closed after slow evidence verification")
	}
	got, _, err := GetIssue(ctx, db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("expired close changed status to %s", got.Status)
	}
}
