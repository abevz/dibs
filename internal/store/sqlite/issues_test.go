package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	req := core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "My test issue",
	}
	issue, err := CreateIssue(context.Background(), db, "test", req)
	if err != nil {
		t.Fatal(err)
	}
	if issue.ID == "" {
		t.Error("expected non-empty issue ID")
	}
	if issue.ShortID == "" {
		t.Error("expected non-empty short_id")
	}
	if issue.ShortID[:5] != "test-" {
		t.Errorf("expected short_id to start with 'test-', got %q", issue.ShortID)
	}
	if issue.Title != "My test issue" {
		t.Errorf("expected title %q, got %q", "My test issue", issue.Title)
	}
	if issue.Status != "open" {
		t.Errorf("expected status 'open', got %q", issue.Status)
	}
	if issue.ProjectID != p.ID {
		t.Errorf("expected project_id %q, got %q", p.ID, issue.ProjectID)
	}
}

func TestCreateIssueWithExternalKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "Mirrored issue",
		ExternalKey: "gh://abevz/af-coordinator/issues/26",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.ExternalKey != "gh://abevz/af-coordinator/issues/26" {
		t.Fatalf("external_key = %q, want %q", issue.ExternalKey, "gh://abevz/af-coordinator/issues/26")
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalKey != issue.ExternalKey {
		t.Fatalf("GetIssue external_key = %q, want %q", got.ExternalKey, issue.ExternalKey)
	}
}

func TestUnlinkArtifactEventPayloadIsJSONEscaped(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "repository", Title: "Unlink JSON test", Repo: repo.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	artifactPath := "docs/spec\"\\\n.md"
	relation := "implements\"\\\ncustom"
	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo: repo.ID, Kind: "sdd", RelativePath: artifactPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{
		Artifact: artifact.ID,
		Relation: relation,
	}); err != nil {
		t.Fatal(err)
	}

	if err := UnlinkArtifact(context.Background(), db, issue.ID, artifactPath, relation, "tester"); err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	var payloadJSON string
	for _, event := range events {
		if event.EventType == "artifact_unlinked" {
			payloadJSON = event.PayloadJSON
			break
		}
	}
	if payloadJSON == "" {
		t.Fatal("expected artifact_unlinked event")
	}
	if !json.Valid([]byte(payloadJSON)) {
		t.Fatalf("expected valid JSON payload, got %q", payloadJSON)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["artifact"] != artifactPath {
		t.Errorf("artifact payload = %q, want %q", payload["artifact"], artifactPath)
	}
	if payload["relation"] != relation {
		t.Errorf("relation payload = %q, want %q", payload["relation"], relation)
	}
}

func TestCreateIssueIncrementsSeq(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}

	if i1.ShortID != "test-1" {
		t.Errorf("expected first short_id 'test-1', got %q", i1.ShortID)
	}
	if i2.ShortID != "test-2" {
		t.Errorf("expected second short_id 'test-2', got %q", i2.ShortID)
	}
}

func TestCreateIssueProjectNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateIssue(context.Background(), db, "nonexistent", core.CreateIssueRequest{ScopeKind: "project", Title: "Fail"})
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
}

func TestGetIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	created, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Get me"})
	if err != nil {
		t.Fatal(err)
	}

	got, lease, err := GetIssue(context.Background(), db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if lease != nil {
		t.Error("expected no lease on newly created issue")
	}
}

func TestGetIssueIncludesExplicitDependencyIdentifiers(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	source, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(context.Background(), db, source.ID, core.AddDependencyRequest{
		DependsOn: target.ID,
		Kind:      "blocks",
	}); err != nil {
		t.Fatal(err)
	}

	got, _, err := GetIssue(context.Background(), db, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(got.Dependencies))
	}
	dep := got.Dependencies[0]
	if dep.IssueID != source.ID {
		t.Fatalf("issue_id = %q, want %q", dep.IssueID, source.ID)
	}
	if dep.IssueShortID != source.ShortID {
		t.Fatalf("issue_short_id = %q, want %q", dep.IssueShortID, source.ShortID)
	}
	if dep.DependsOnID != target.ID {
		t.Fatalf("depends_on_id = %q, want %q", dep.DependsOnID, target.ID)
	}
	if dep.DependsOnShortID != target.ShortID {
		t.Fatalf("depends_on_short_id = %q, want %q", dep.DependsOnShortID, target.ShortID)
	}
	if !got.Blocked {
		t.Fatal("expected open blocks dependency to mark issue blocked")
	}
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != target.ShortID {
		t.Fatalf("blocked_by = %#v, want [%q]", got.BlockedBy, target.ShortID)
	}

	if _, err := db.Exec("UPDATE issues SET status = 'done' WHERE id = ?", target.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err = GetIssue(context.Background(), db, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Blocked || len(got.BlockedBy) != 0 {
		t.Fatalf("terminal dependency still blocks issue: blocked=%t blocked_by=%#v", got.Blocked, got.BlockedBy)
	}
}

func TestGetIssuePopulatesReverseBlocks(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	source, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	// source depends on target with kind blocks: target blocks source.
	if err := AddDependency(context.Background(), db, source.ID, core.AddDependencyRequest{
		DependsOn: target.ID,
		Kind:      "blocks",
	}); err != nil {
		t.Fatal(err)
	}

	// The blocker sees the reverse relationship: Blocks contains the dependent.
	blocker, _, err := GetIssue(context.Background(), db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocker.Blocks) != 1 || blocker.Blocks[0] != source.ShortID {
		t.Fatalf("blocks = %#v, want [%q]", blocker.Blocks, source.ShortID)
	}
	// Symmetry: the dependent's BlockedBy names the blocker.
	dependent, _, err := GetIssue(context.Background(), db, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependent.BlockedBy) != 1 || dependent.BlockedBy[0] != target.ShortID {
		t.Fatalf("blocked_by = %#v, want [%q]", dependent.BlockedBy, target.ShortID)
	}

	// A terminal blocker no longer blocks anyone, mirroring the forward rule.
	if _, err := db.Exec("UPDATE issues SET status = 'done' WHERE id = ?", target.ID); err != nil {
		t.Fatal(err)
	}
	blocker, _, err = GetIssue(context.Background(), db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocker.Blocks) != 0 {
		t.Fatalf("terminal blocker still lists blocks: %#v", blocker.Blocks)
	}
}

func TestGetIssueByShortID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	created, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "By short ID"})
	if err != nil {
		t.Fatal(err)
	}

	resolvedID, err := ResolveIssueID(context.Background(), db, created.ShortID)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GetIssue(context.Background(), db, resolvedID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, got.ID)
	}
	if got.ShortID != created.ShortID {
		t.Errorf("expected short_id %q, got %q", created.ShortID, got.ShortID)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, _, err := GetIssue(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent issue")
	}
}

func TestGetIssueByShortIDNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, _, err := GetIssue(context.Background(), db, "no-proj-42")
	if err == nil {
		t.Fatal("expected error for nonexistent short_id")
	}
}

func TestListIssues(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	// No issues yet.
	issues, err := ListIssues(context.Background(), db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}

	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})

	issues, err = ListIssues(context.Background(), db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(issues))
	}
}

func TestListIssuesFilterByStatus(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Only open"})
	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Only open 2"})

	// Use raw SQL to set one to 'done'.
	_, err = db.Exec("UPDATE issues SET status = 'done' WHERE id = (SELECT id FROM issues ORDER BY created_at DESC LIMIT 1)")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Errorf("expected 1 open issue, got %d", len(issues))
	}
}

func TestListIssuesFilterByMultipleValues(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	for _, project := range []struct{ key, name string }{
		{key: "afc", name: "AF Coordinator"},
		{key: "aion", name: "Aion Forge"},
	} {
		if _, err := CreateProject(ctx, db, project.key, project.name, ""); err != nil {
			t.Fatal(err)
		}
	}

	wrongProject, err := CreateIssue(ctx, db, "afc", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Wrong type", IssueType: "bug",
	})
	if err != nil {
		t.Fatal(err)
	}
	epic, err := CreateIssue(ctx, db, "afc", core.CreateIssueRequest{
		ScopeKind: "project", Title: "AF epic", IssueType: "epic",
	})
	if err != nil {
		t.Fatal(err)
	}
	chore, err := CreateIssue(ctx, db, "aion", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Aion chore", IssueType: "chore",
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongStatus, err := CreateIssue(ctx, db, "aion", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Wrong status", IssueType: "epic",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, update := range []struct {
		id        string
		status    string
		updatedAt string
	}{
		{id: wrongProject.ID, status: "open", updatedAt: "2026-07-13T10:00:00Z"},
		{id: epic.ID, status: "open", updatedAt: "2026-07-13T11:00:00Z"},
		{id: chore.ID, status: "in_progress", updatedAt: "2026-07-13T12:00:00Z"},
		{id: wrongStatus.ID, status: "done", updatedAt: "2026-07-13T13:00:00Z"},
	} {
		if _, err := db.Exec(`UPDATE issues SET status = ?, updated_at = ? WHERE id = ?`, update.status, update.updatedAt, update.id); err != nil {
			t.Fatal(err)
		}
	}

	issues, err := ListIssues(ctx, db, core.IssueListParams{
		Projects:   []string{"afc", "aion"},
		IssueTypes: []string{"epic", "chore"},
		Statuses:   []string{"open", "in_progress"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 matching issues, got %d", len(issues))
	}
	if issues[0].ID != chore.ID || issues[1].ID != epic.ID {
		t.Fatalf("ordered issue IDs = [%s %s], want [%s %s]", issues[0].ID, issues[1].ID, chore.ID, epic.ID)
	}
}

func TestListIssuesFilterByExternalKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "Plain issue",
		ExternalKey: "temporal:workflow-123",
	}); err != nil {
		t.Fatal(err)
	}
	want, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "Mirrored issue",
		ExternalKey: "gh://abevz/af-coordinator/issues/26",
	})
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{
		ExternalKey: "gh://abevz/af-coordinator/issues/26",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].ID != want.ID {
		t.Fatalf("issue id = %q, want %q", issues[0].ID, want.ID)
	}
}

func TestListIssuesFilterByProjectScopedRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), db, "p2", "Project 2", ""); err != nil {
		t.Fatal(err)
	}

	repo1, _, err := CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p1-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p2-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo1.ID,
		Title:     "P1 issue",
	}); err != nil {
		t.Fatal(err)
	}
	want, err := CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo2.ID,
		Title:     "P2 issue",
	})
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{
		Project: "p2",
		Repo:    "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].ID != want.ID {
		t.Fatalf("expected issue %q, got %q", want.ID, issues[0].ID)
	}
}

func TestClaimIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Claimable"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if claim.LeaseToken == "" {
		t.Error("expected non-empty lease_token")
	}
	if claim.ExpiresAt == "" {
		t.Error("expected non-empty expires_at")
	}
	if claim.AttemptID == "" {
		t.Error("expected non-empty attempt_id")
	}
	if claim.LeaseGeneration != 1 {
		t.Errorf("lease_generation = %d, want 1", claim.LeaseGeneration)
	}

	// Verify issue status changed.
	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", got.Status)
	}
	// The claim response's Version must already reflect the increment that
	// claiming performs as a side effect, so a caller can use it directly as
	// --expected-version on close without a stale value read from an earlier
	// `issue get`.
	if claim.Version != got.Version {
		t.Errorf("expected claim response version %d to match post-claim issue version %d", claim.Version, got.Version)
	}
	if claim.Version != issue.Version+1 {
		t.Errorf("expected claim to increment version from %d to %d, got %d", issue.Version, issue.Version+1, claim.Version)
	}
	if lease == nil {
		t.Fatal("expected active lease")
	}
	if lease.Holder != "agent-1" {
		t.Errorf("expected holder 'agent-1', got %q", lease.Holder)
	}
	if lease.AttemptID != claim.AttemptID || lease.SessionID != "" {
		t.Fatalf("unexpected compatibility lease telemetry: %+v", lease)
	}
	if lease.LeaseGeneration != claim.LeaseGeneration {
		t.Fatalf("lease generation = %d, want claim generation %d", lease.LeaseGeneration, claim.LeaseGeneration)
	}
}

func TestClaimIssueRecordsAttemptAndSessionWithoutLeakingToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Attempt telemetry",
	})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-1", 900, "session-42")
	if err != nil {
		t.Fatal(err)
	}
	if claim.AttemptID == "" {
		t.Fatal("expected non-empty attempt_id")
	}

	_, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.AttemptID != claim.AttemptID || lease.SessionID != "session-42" {
		t.Fatalf("unexpected active lease: %+v", lease)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_claimed" {
		t.Fatalf("event_type = %q, want issue_claimed", last.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["attempt_id"] != claim.AttemptID || payload["lease_generation"] != float64(claim.LeaseGeneration) || payload["session_id"] != "session-42" ||
		payload["ttl_seconds"] != float64(900) || payload["expires_at"] != claim.ExpiresAt {
		t.Fatalf("unexpected claim payload: %v", payload)
	}
	if strings.Contains(last.PayloadJSON, claim.LeaseToken) {
		t.Fatalf("claim event leaked lease token: %s", last.PayloadJSON)
	}
}

func TestLeaseGenerationAdvancesOnlyOnFreshClaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "generation", "Generation", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "generation", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Monotonic lease generation",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-1", 3600, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseGeneration != 1 {
		t.Fatalf("first generation = %d, want 1", first.LeaseGeneration)
	}
	if _, err := HeartbeatLease(context.Background(), db, issue.ID, first.LeaseToken, first.LeaseGeneration, 3600, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, activeLease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activeLease == nil || activeLease.LeaseGeneration != first.LeaseGeneration {
		t.Fatalf("heartbeat changed lease generation: %+v", activeLease)
	}
	if _, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-1", 3600, "session-2"); err == nil {
		t.Fatal("same-holder claim returned an active lease token")
	} else {
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
			t.Fatalf("same-holder claim error = %v, want lease_held", err)
		}
	}
	if err := ReleaseLease(context.Background(), db, issue.ID, first.LeaseToken, first.LeaseGeneration, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseGeneration != first.LeaseGeneration+1 {
		t.Fatalf("second generation = %d, want %d", second.LeaseGeneration, first.LeaseGeneration+1)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	var claimGenerations []float64
	for _, event := range events {
		if event.EventType != "issue_claimed" && event.EventType != "issue_released" {
			continue
		}
		if strings.Contains(event.PayloadJSON, first.LeaseToken) || strings.Contains(event.PayloadJSON, second.LeaseToken) {
			t.Fatalf("lifecycle event leaked lease token: %s", event.PayloadJSON)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["lease_generation"]; !ok {
			t.Fatalf("%s event missing lease_generation: %v", event.EventType, payload)
		}
		if event.EventType == "issue_claimed" {
			claimGenerations = append(claimGenerations, payload["lease_generation"].(float64))
		}
	}
	if len(claimGenerations) != 2 || claimGenerations[0] != 1 || claimGenerations[1] != 2 {
		t.Fatalf("claim generations = %v, want [1 2]", claimGenerations)
	}
}

func TestLazyReclaimRecordsExpiredAttemptBeforeReplacement(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Expired attempt",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-1", 60, "session-old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`,
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), issue.ID); err != nil {
		t.Fatal(err)
	}

	second, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-2", 120, "session-new")
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID {
		t.Fatalf("replacement reused attempt_id %q", second.AttemptID)
	}
	if second.LeaseGeneration != first.LeaseGeneration+1 {
		t.Fatalf("replacement generation = %d, want %d", second.LeaseGeneration, first.LeaseGeneration+1)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	expiredIndex, replacementIndex := -1, -1
	for index, event := range events {
		switch event.EventType {
		case "lease_expired":
			expiredIndex = index
			var payload map[string]any
			if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["attempt_id"] != first.AttemptID || payload["end_reason"] != "expired" ||
				payload["session_id"] != "session-old" {
				t.Fatalf("unexpected expiry payload: %v", payload)
			}
			if strings.Contains(event.PayloadJSON, first.LeaseToken) {
				t.Fatalf("expiry event leaked lease token: %s", event.PayloadJSON)
			}
		case "issue_claimed":
			if strings.Contains(event.PayloadJSON, second.AttemptID) {
				replacementIndex = index
			}
		}
	}
	if expiredIndex < 0 || replacementIndex < 0 || expiredIndex >= replacementIndex {
		t.Fatalf("expected lease_expired before replacement issue_claimed, got indexes %d and %d", expiredIndex, replacementIndex)
	}
}

func TestClaimIssueAlreadyClaimed(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Double claim"})
	if err != nil {
		t.Fatal(err)
	}

	// First claim succeeds.
	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Second claim should fail.
	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err == nil {
		t.Fatal("expected error for already-claimed issue")
	}

	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrLeaseHeld {
		t.Errorf("expected code %q, got %q", core.ErrLeaseHeld, apiErr.Code)
	}
}

func TestClaimIssueRejectsUnfinishedBlocksDependency(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "claim-ready", "Claim Ready", ""); err != nil {
		t.Fatal(err)
	}
	blocker, err := CreateIssue(context.Background(), db, "claim-ready", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Blocker",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := CreateIssue(context.Background(), db, "claim-ready", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Blocked target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(context.Background(), db, target.ID, core.AddDependencyRequest{
		DependsOn: blocker.ID, Kind: "blocks", Actor: "planner",
	}); err != nil {
		t.Fatal(err)
	}

	eventsBefore, err := ListEvents(context.Background(), db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ClaimIssue(context.Background(), db, target.ID, "worker", 3600)
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrIssueNotReady {
		t.Fatalf("blocked claim error = %v, want issue_not_ready", err)
	}
	got, lease, err := GetIssue(context.Background(), db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := ListEvents(context.Background(), db, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" || got.Version != target.Version || lease != nil || len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("blocked claim changed state: issue=%+v lease=%+v events=%d->%d", got, lease, len(eventsBefore), len(eventsAfter))
	}

	if _, err := db.Exec(`UPDATE issues SET status = 'done' WHERE id = ?`, blocker.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, target.ID, "worker", 3600)
	if err != nil {
		t.Fatalf("claim after blocker completion: %v", err)
	}
	if claim.LeaseToken == "" {
		t.Fatal("claim after blocker completion returned no token")
	}
}

func TestConcurrentClaimsProduceOneAttemptWinner(t *testing.T) {
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Concurrent claim",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use distinct holders so all are competing against each other; only one
	// should win a fresh claim and the rest must receive ErrLeaseHeld.
	const claimants = 4
	start := make(chan struct{})
	errs := make(chan error, claimants)
	var wg sync.WaitGroup
	for index := 0; index < claimants; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			holder := fmt.Sprintf("agent-%d", index)
			_, err := ClaimIssueWithSession(context.Background(), db, issue.ID, holder, 60, "session-concurrent")
			errs <- err
		}(index)
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
			t.Fatalf("concurrent claim error = %v, want lease_held", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful concurrent claims = %d, want 1", succeeded)
	}
	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	claims := 0
	for _, event := range events {
		if event.EventType == "issue_claimed" {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("issue_claimed events = %d, want 1", claims)
	}
}

func TestClaimIssueWithExpiredLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Expired lease claim"})
	if err != nil {
		t.Fatal(err)
	}

	// Claim it first.
	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Manually expire the lease in the DB and keep the status as 'in_progress'.
	_, err = db.Exec(
		`UPDATE leases SET expires_at = ? WHERE issue_id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		issue.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Second claim (by agent-2) should now succeed because the lease has expired,
	// even though the status is still 'in_progress'.
	resp, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatalf("expected claim on expired lease to succeed, got: %v", err)
	}
	if resp.LeaseToken == "" {
		t.Error("expected new lease token to be returned")
	}
}

func TestClaimIssueNonexistent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := ClaimIssue(context.Background(), db, "nonexistent", "agent-1", 3600)
	if err == nil {
		t.Fatal("expected error for nonexistent issue")
	}
}

func TestHeartbeatLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Heart me"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	newExpires, err := HeartbeatLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, 7200, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if newExpires == "" {
		t.Error("expected non-empty new expires_at")
	}
	if newExpires == claim.ExpiresAt {
		t.Error("expected new expires_at to differ from original")
	}
	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].EventType != "issue_claimed" {
		t.Fatalf("heartbeat appended an event: %+v", events)
	}
}

func TestHeartbeatLeaseWrongToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Heart fail"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = HeartbeatLease(context.Background(), db, issue.ID, "wrong-token", claim.LeaseGeneration, 7200, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for wrong lease token")
	}
}

func TestReleaseLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Release me"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	err = ReleaseLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	// Verify lease is gone and issue is back to open.
	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" {
		t.Errorf("expected status 'open' after release, got %q", got.Status)
	}
	if lease != nil {
		t.Error("expected lease to be nil after release")
	}
	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if last.EventType != "issue_released" || payload["attempt_id"] != claim.AttemptID || payload["end_reason"] != "released" {
		t.Fatalf("unexpected release event: type=%q payload=%v", last.EventType, payload)
	}
	if payload["lease_generation"] != float64(claim.LeaseGeneration) {
		t.Fatalf("release event generation = %v, want %d", payload["lease_generation"], claim.LeaseGeneration)
	}
	if strings.Contains(last.PayloadJSON, claim.LeaseToken) {
		t.Fatalf("release event leaked lease token: %s", last.PayloadJSON)
	}
}

func TestReleaseLeaseWrongToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Release fail"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	err = ReleaseLease(context.Background(), db, issue.ID, "wrong-token", claim.LeaseGeneration, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for wrong lease token")
	}
}

func TestHeartbeatLeaseWrongGenerationFailsCAS(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Heartbeat gen mismatch"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Correct token but a stale generation must not renew the lease.
	_, err = HeartbeatLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration+1, 7200, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for wrong lease generation")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("heartbeat error = %v, want lease_expired", err)
	}

	var storedExpires string
	if err := db.QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&storedExpires); err != nil {
		t.Fatal(err)
	}
	if storedExpires != claim.ExpiresAt {
		t.Fatalf("stale heartbeat changed lease expiry: got %s, want %s", storedExpires, claim.ExpiresAt)
	}
}

func TestHeartbeatLeaseExpiredFailsWithoutMutation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Heartbeat expired"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Force expiry; the CAS predicate must reject the renewal even though the
	// token and generation are the current ones.
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}

	_, err = HeartbeatLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, 7200, time.Now().UTC())
	if err == nil {
		t.Fatal("expected lease_expired error for expired lease")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("heartbeat error = %v, want lease_expired", err)
	}

	var storedExpires string
	if err := db.QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&storedExpires); err != nil {
		t.Fatal(err)
	}
	if storedExpires != forcedExpires {
		t.Fatalf("heartbeat resurrected expired lease: expires_at = %s, want %s", storedExpires, forcedExpires)
	}
}

func TestHeartbeatRenewalWinsBeforeReclaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Heartbeat wins"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Ordering 1 of the forced heartbeat/reclaim interleaving: the renewal
	// commits first, so the reclaim attempt must lose.
	newExpires, err := HeartbeatLease(context.Background(), db, issue.ID, first.LeaseToken, first.LeaseGeneration, 7200, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err == nil {
		t.Fatal("reclaim succeeded after a committed renewal")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
		t.Fatalf("reclaim error = %v, want lease_held", err)
	}

	// The failed reclaim must not have touched the renewed lease deadline.
	var storedExpires string
	if err := db.QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&storedExpires); err != nil {
		t.Fatal(err)
	}
	if storedExpires != newExpires {
		t.Fatalf("failed reclaim changed lease expiry: got %s, want %s", storedExpires, newExpires)
	}
}

func TestStaleHeartbeatFailsAfterReclaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Reclaim wins"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}

	// Ordering 2 of the forced heartbeat/reclaim interleaving: the reclaim
	// commits first (expired lease replaced), so the old holder's heartbeat
	// must fail and must not touch the replacement.
	second, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = HeartbeatLease(context.Background(), db, issue.ID, first.LeaseToken, first.LeaseGeneration, 7200, time.Now().UTC())
	if err == nil {
		t.Fatal("stale heartbeat succeeded after reclaim")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("stale heartbeat error = %v, want lease_expired", err)
	}

	// The replacement expiry must never change.
	var storedExpires string
	if err := db.QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&storedExpires); err != nil {
		t.Fatal(err)
	}
	if storedExpires != second.ExpiresAt {
		t.Fatalf("stale heartbeat changed replacement expiry: got %s, want %s", storedExpires, second.ExpiresAt)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
	if lease == nil || lease.LeaseGeneration != second.LeaseGeneration {
		t.Fatalf("replacement lease = %+v, want generation %d", lease, second.LeaseGeneration)
	}
}

func TestReleaseLeaseWrongGenerationFailsCAS(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Release gen mismatch"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Correct token but a stale generation must not release the lease.
	err = ReleaseLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration+1, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for wrong lease generation")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("release error = %v, want lease_expired", err)
	}

	var leaseCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM leases WHERE issue_id = ?`, issue.ID).Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 1 {
		t.Fatalf("stale release removed the lease: count = %d, want 1", leaseCount)
	}

	after, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || after.Version != before.Version {
		t.Fatalf("stale release mutated issue: status %q->%q, version %d->%d", before.Status, after.Status, before.Version, after.Version)
	}

	eventsAfter, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("stale release appended events: %d -> %d", len(eventsBefore), len(eventsAfter))
	}
}

func TestReleaseLeaseExpiredFailsWithoutStateChanges(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Expired release"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}

	// Release of an expired lease must fail with lease_expired and leave
	// issue/event/lease state untouched.
	err = ReleaseLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, time.Now().UTC())
	if err == nil {
		t.Fatal("expected lease_expired error for expired lease")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("release error = %v, want lease_expired", err)
	}

	after, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || after.Version != before.Version || after.ClaimedAt != before.ClaimedAt {
		t.Fatalf("expired release mutated issue: status %q->%q, version %d->%d", before.Status, after.Status, before.Version, after.Version)
	}

	eventsAfter, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("expired release appended events: %d -> %d", len(eventsBefore), len(eventsAfter))
	}

	var storedExpires string
	if err := db.QueryRow(`SELECT expires_at FROM leases WHERE issue_id = ?`, issue.ID).Scan(&storedExpires); err != nil {
		t.Fatal(err)
	}
	if storedExpires != forcedExpires {
		t.Fatalf("expired release removed or changed the lease: expires_at = %s, want %s", storedExpires, forcedExpires)
	}
}

func TestReleaseLeaseSingleStatusVersionTransition(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Single release transition"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	err = ReleaseLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" {
		t.Fatalf("status = %q, want open", got.Status)
	}
	if lease != nil {
		t.Fatalf("lease = %+v, want nil", lease)
	}
	if got.Version != claim.Version+1 {
		t.Fatalf("version = %d, want %d (exactly one transition)", got.Version, claim.Version+1)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	released := 0
	for _, event := range events {
		if event.EventType != "issue_released" {
			continue
		}
		released++
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["attempt_id"] != claim.AttemptID {
			t.Fatalf("release event attempt_id = %v, want %s", payload["attempt_id"], claim.AttemptID)
		}
	}
	if released != 1 {
		t.Fatalf("issue_released events = %d, want 1", released)
	}
}

func TestListReadyIssues(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create two open issues.
	issue1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Open issue"})
	if err != nil {
		t.Fatal(err)
	}
	issue2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Done issue"})
	if err != nil {
		t.Fatal(err)
	}

	// Mark the second issue as 'done' via raw SQL.
	_, err = db.Exec("UPDATE issues SET status = 'done' WHERE id = ?", issue2.ID)
	if err != nil {
		t.Fatal(err)
	}

	ready, err := ListReadyIssues(context.Background(), db, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready issue, got %d", len(ready))
	}
	if len(ready) > 0 && ready[0].ID != issue1.ID {
		t.Errorf("expected ready issue to be the open one (%q), got %q", issue1.ID, ready[0].ID)
	}
}

func TestListReadyIssuesExcludesLeased(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Leased issue"})
	if err != nil {
		t.Fatal(err)
	}

	// Claim the issue → it becomes in_progress with an active lease.
	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// ListReadyIssues should exclude leased issues.
	ready, err := ListReadyIssues(context.Background(), db, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Errorf("expected 0 ready issues (issue is leased), got %d", len(ready))
	}
}

func TestListReadyIssuesByProject(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p1, err := CreateProject(context.Background(), db, "p1", "Project 1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateProject(context.Background(), db, "p2", "Project 2", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{ScopeKind: "project", Title: "P1 issue"})
	CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{ScopeKind: "project", Title: "P2 issue"})

	ready, err := ListReadyIssues(context.Background(), db, p1.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready issue for project p1, got %d", len(ready))
	}
	if len(ready) > 0 && ready[0].Title != "P1 issue" {
		t.Errorf("expected title 'P1 issue', got %q", ready[0].Title)
	}
}

func TestListReadyIssuesByProjectAndScopedRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	p2, err := CreateProject(context.Background(), db, "p2", "Project 2", "")
	if err != nil {
		t.Fatal(err)
	}

	repo1, _, err := CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p1-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p2-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo1.ID,
		Title:     "P1 shared",
	}); err != nil {
		t.Fatal(err)
	}
	want, err := CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo2.ID,
		Title:     "P2 shared",
	})
	if err != nil {
		t.Fatal(err)
	}

	ready, err := ListReadyIssues(context.Background(), db, p2.ID, repo2.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready issue, got %d", len(ready))
	}
	if ready[0].ID != want.ID {
		t.Fatalf("expected issue %q, got %q", want.ID, ready[0].ID)
	}
}

func TestListReadyIssuesWithExpiredLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Expired lease"})
	if err != nil {
		t.Fatal(err)
	}

	// Insert an already-expired lease directly.
	_, err = db.Exec(
		`INSERT INTO leases (issue_id, holder, lease_token, expires_at, created_at, updated_at)
		 VALUES (?, 'old-agent', 'old-token', ?, ?, ?)`,
		issue.ID,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatal(err)
	}

	ready, err := ListReadyIssues(context.Background(), db, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready issue (expired lease should be ignored), got %d", len(ready))
	}
}

func TestCreateIssueWithRepoScope(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "my-repo",
		CanonicalGitDir: "/repos/my-repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      repo.ID,
		Title:     "Repo-scoped issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.RepositoryID != repo.ID {
		t.Errorf("expected repository_id %q, got %q", repo.ID, issue.RepositoryID)
	}
	if issue.ScopeKind != "repository" {
		t.Errorf("expected scope_kind 'repository', got %q", issue.ScopeKind)
	}
	if issue.ProjectID == "" {
		t.Error("expected non-empty project_id")
	}
}

func TestCreateIssueWithRepoScopeUsesProjectScopedRepo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), db, "p2", "Project 2", ""); err != nil {
		t.Fatal(err)
	}
	_, _, err := CreateRepo(context.Background(), db, "p1", core.CreateRepoRequest{
		Project:         "p1",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p1-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo2, _, err := CreateRepo(context.Background(), db, "p2", core.CreateRepoRequest{
		Project:         "p2",
		LogicalName:     "shared",
		CanonicalGitDir: "/repos/p2-shared.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{
		ScopeKind: "repository",
		Repo:      "shared",
		Title:     "Scoped repo issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.RepositoryID != repo2.ID {
		t.Fatalf("expected repository_id %q, got %q", repo2.ID, issue.RepositoryID)
	}
}

func TestListReadyIssuesWithLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create two issues.
	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Ready issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Claimed issue",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Claim i2.
	_, err = ClaimIssue(context.Background(), db, i2.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// List ready issues: only i1 (unclaimed) should appear.
	ready, err := ListReadyIssues(context.Background(), db, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready issue, got %d", len(ready))
	}
	if ready[0].ID != i1.ID {
		t.Errorf("expected ready issue ID %q, got %q", i1.ID, ready[0].ID)
	}
	if ready[0].Title != "Ready issue" {
		t.Errorf("expected title 'Ready issue', got %q", ready[0].Title)
	}
}

func TestUpdateIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Original title",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "Updated title",
		ExpectedVersion: issue.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Updated title" {
		t.Errorf("expected title 'Updated title', got %q", updated.Title)
	}
	if updated.Version != issue.Version+1 {
		t.Errorf("expected version %d, got %d", issue.Version+1, updated.Version)
	}
}

func TestUpdateIssueExternalKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Original title",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		ExternalKey:     "temporal:workflow-456",
		ExpectedVersion: issue.Version,
		Actor:           "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ExternalKey != "temporal:workflow-456" {
		t.Fatalf("external_key = %q, want %q", updated.ExternalKey, "temporal:workflow-456")
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_updated" {
		t.Fatalf("expected last event issue_updated, got %q", last.EventType)
	}
	var payload struct {
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range payload.Changed {
		if field == "external_key" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected changed fields to include external_key, got %v", payload.Changed)
	}
}

func TestUpdateIssueVersionConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Version test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First update succeeds.
	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "First update",
		ExpectedVersion: issue.Version,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second update with stale version should fail.
	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "Stale update",
		ExpectedVersion: issue.Version, // old version
	})
	if err == nil {
		t.Fatal("expected error for version conflict")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
}

func TestConcurrentUpdatesWithSameVersionCommitOnce(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// Production Open serializes mutations through one physical connection.
	db.SetMaxOpenConns(1)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Concurrent update",
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, title := range []string{"update-a", "update-b"} {
		title := title
		go func() {
			<-start
			_, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
				Title:           title,
				ExpectedVersion: issue.Version,
			})
			results <- err
		}()
	}
	close(start)

	succeeded := 0
	conflicted := 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
			continue
		}
		var apiErr core.APIError
		if errors.As(err, &apiErr) && apiErr.Code == core.ErrConflict {
			conflicted++
			continue
		}
		t.Fatalf("concurrent update error = %v, want nil or conflict", err)
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent update outcomes: succeeded=%d conflicted=%d, want 1/1", succeeded, conflicted)
	}

	updated, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != issue.Version+1 {
		t.Fatalf("version = %d, want one increment to %d", updated.Version, issue.Version+1)
	}
}

func TestCloseIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Close me",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "tester", 60)
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err = GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
		Resolution:      "done",
		Branch:          "codex/afc-27",
		PRURL:           "https://github.com/abevz/dibs/pull/27",
		CommitSHA:       "ba6d011",
		ExpectedVersion: issue.Version,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "codex/afc-27" {
		t.Fatalf("branch = %q, want %q", result.Branch, "codex/afc-27")
	}
	if result.PRURL != "https://github.com/abevz/dibs/pull/27" {
		t.Fatalf("pr_url = %q", result.PRURL)
	}
	if result.CommitSHA != "ba6d011" {
		t.Fatalf("commit_sha = %q", result.CommitSHA)
	}

	// Verify it's closed.
	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Errorf("expected status 'done', got %q", got.Status)
	}
	if got.ClosedAt == "" {
		t.Error("expected non-empty closed_at")
	}
	if got.Version != issue.Version+1 {
		t.Errorf("expected version %d, got %d", issue.Version+1, got.Version)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_closed" {
		t.Fatalf("expected issue_closed event, got %q", last.EventType)
	}
	if !json.Valid([]byte(last.PayloadJSON)) {
		t.Fatalf("expected valid JSON payload, got %q", last.PayloadJSON)
	}
	var payload struct {
		Resolution string   `json:"resolution"`
		AttemptID  string   `json:"attempt_id"`
		EndReason  string   `json:"end_reason"`
		FromStatus string   `json:"from_status"`
		ToStatus   string   `json:"to_status"`
		Branch     string   `json:"branch"`
		PRURL      string   `json:"pr_url"`
		CommitSHA  string   `json:"commit_sha"`
		Changed    []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Resolution != "done" || payload.Branch != "codex/afc-27" ||
		payload.PRURL != "https://github.com/abevz/dibs/pull/27" ||
		payload.CommitSHA != "ba6d011" || payload.FromStatus != "in_progress" ||
		payload.ToStatus != "done" || payload.AttemptID != claim.AttemptID ||
		payload.EndReason != "done" {
		t.Fatalf("unexpected close payload: %+v", payload)
	}
	if strings.Contains(last.PayloadJSON, claim.LeaseToken) {
		t.Fatalf("close event leaked lease token: %s", last.PayloadJSON)
	}
}

func TestCloseIssueCancelled(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Cancel me",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "tester", 60)
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err = GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
		Resolution:      "cancelled",
		ExpectedVersion: issue.Version,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", got.Status)
	}
}

func TestCloseIssueVersionConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Conflict",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use wrong expected version.
	_, err = CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
		Resolution:      "done",
		ExpectedVersion: 99,
	})
	if err == nil {
		t.Fatal("expected error for version conflict")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrConflict {
		t.Errorf("expected code %q, got %q", core.ErrConflict, apiErr.Code)
	}
}

func TestCloseIssueRequiresActiveMatchingLeaseAndNonTerminalIssue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prepare    func(t *testing.T, db *sql.DB, issue core.Issue) (core.Issue, string)
		resolution string
		wantCode   string
	}{
		{
			name:       "missing lease",
			resolution: "done",
			wantCode:   core.ErrLeaseExpired,
			prepare: func(_ *testing.T, _ *sql.DB, issue core.Issue) (core.Issue, string) {
				return issue, ""
			},
		},
		{
			name:       "expired lease",
			resolution: "done",
			wantCode:   core.ErrLeaseExpired,
			prepare: func(t *testing.T, db *sql.DB, issue core.Issue) (core.Issue, string) {
				claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent", 60)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE leases SET expires_at = '2026-07-13T19:00:00Z' WHERE issue_id = ?`, issue.ID); err != nil {
					t.Fatal(err)
				}
				updated, _, err := GetIssue(context.Background(), db, issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				return updated, claim.LeaseToken
			},
		},
		{
			name:       "wrong lease token",
			resolution: "done",
			wantCode:   core.ErrLeaseExpired,
			prepare: func(t *testing.T, db *sql.DB, issue core.Issue) (core.Issue, string) {
				if _, err := ClaimIssue(context.Background(), db, issue.ID, "agent", 60); err != nil {
					t.Fatal(err)
				}
				updated, _, err := GetIssue(context.Background(), db, issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				return updated, "wrong-token"
			},
		},
		{
			name:       "already terminal",
			resolution: "done",
			wantCode:   core.ErrValidationFailed,
			prepare: func(t *testing.T, db *sql.DB, issue core.Issue) (core.Issue, string) {
				if _, err := db.Exec(`UPDATE issues SET status = 'cancelled', version = version + 1 WHERE id = ?`, issue.ID); err != nil {
					t.Fatal(err)
				}
				updated, _, err := GetIssue(context.Background(), db, issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				return updated, ""
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newTestDB(t)
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: test.name})
			if err != nil {
				t.Fatal(err)
			}
			issue, token := test.prepare(t, db, issue)

			_, err = CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
				Resolution:      test.resolution,
				ExpectedVersion: issue.Version,
				LeaseToken:      token,
				Actor:           "agent",
			})
			if err == nil {
				t.Fatalf("CloseIssue() succeeded, want %s", test.wantCode)
			}
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != test.wantCode {
				t.Fatalf("CloseIssue() error = %v, want %s", err, test.wantCode)
			}
		})
	}
}

func TestStaleGenerationCloseFailsAfterReclaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Reclaim close",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}
	second, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The replacement holds the current token, but the presented generation is
	// the stale pre-reclaim value; the generation CAS must reject the close
	// before any note, event, or status change.
	_, err = CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
		Resolution:      "done",
		ExpectedVersion: updated.Version,
		LeaseToken:      second.LeaseToken,
		LeaseGeneration: first.LeaseGeneration,
		Actor:           "agent-1",
	})
	if err == nil {
		t.Fatal("stale generation close succeeded after reclaim")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("stale generation close error = %v, want lease_expired", err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("stale generation close changed status to %q", got.Status)
	}
	if lease == nil || lease.LeaseGeneration != second.LeaseGeneration {
		t.Fatalf("replacement lease = %+v, want generation %d", lease, second.LeaseGeneration)
	}
	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventType == "issue_closed" {
			t.Fatalf("stale generation close wrote an issue_closed event: %+v", event)
		}
	}
}

func TestUpdateIssueRejectsTerminalTransitions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		current string
		target  string
	}{
		{name: "close through update", current: "open", target: "done"},
		{name: "reopen through update", current: "cancelled", target: "open"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newTestDB(t)
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: test.name})
			if err != nil {
				t.Fatal(err)
			}
			if test.current != "open" {
				if _, err := db.Exec(`UPDATE issues SET status = ? WHERE id = ?`, test.current, issue.ID); err != nil {
					t.Fatal(err)
				}
			}
			current, _, err := GetIssue(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}

			_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
				Status:          test.target,
				ExpectedVersion: current.Version,
				Actor:           "agent",
			})
			if err == nil {
				t.Fatal("UpdateIssue() succeeded, want validation error")
			}
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != core.ErrValidationFailed {
				t.Fatalf("UpdateIssue() error = %v, want validation_failed", err)
			}
		})
	}
}

func TestOperatorCloseIssueClosesUnclaimableEpicWithoutLeaseToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	epic, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		IssueType: "epic",
		Title:     "Operator closes this epic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssue(context.Background(), db, epic.ID, "agent", 60); err == nil {
		t.Fatal("ClaimIssue() succeeded for an epic")
	}

	result, err := OperatorCloseIssue(context.Background(), db, epic.ID, core.OperatorCloseIssueRequest{
		Resolution:      "done",
		ExpectedVersion: epic.Version,
		Actor:           "operator",
		Reason:          "closing completed parent work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "closed" || result.Resolution != "done" {
		t.Fatalf("unexpected close result: %+v", result)
	}

	events, err := ListEvents(context.Background(), db, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_operator_closed" {
		t.Fatalf("event_type = %q, want issue_operator_closed", last.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["from_status"] != "open" || payload["to_status"] != "done" ||
		payload["reason"] != "closing completed parent work" {
		t.Fatalf("unexpected operator-close payload: %v", payload)
	}
}

func TestOperatorCloseIssueWithMetadata(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Close with metadata",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := OperatorCloseIssue(context.Background(), db, issue.ID, core.OperatorCloseIssueRequest{
		Resolution:      "done",
		ExpectedVersion: issue.Version,
		Actor:           "operator",
		Reason:          "merged via PR",
		Branch:          "feat/metadata",
		PRURL:           "https://github.com/example/repo/pull/42",
		CommitSHA:       "abc123def456",
		Note:            "Closed via operator after PR merge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "feat/metadata" {
		t.Fatalf("result.Branch = %q, want %q", result.Branch, "feat/metadata")
	}
	if result.PRURL != "https://github.com/example/repo/pull/42" {
		t.Fatalf("result.PRURL = %q, want %q", result.PRURL, "https://github.com/example/repo/pull/42")
	}
	if result.CommitSHA != "abc123def456" {
		t.Fatalf("result.CommitSHA = %q, want %q", result.CommitSHA, "abc123def456")
	}

	// Verify note was inserted.
	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Body != "Closed via operator after PR merge" {
		t.Fatalf("note body = %q, want %q", notes[0].Body, "Closed via operator after PR merge")
	}

	// Verify event payload includes metadata.
	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_operator_closed" {
		t.Fatalf("event_type = %q, want issue_operator_closed", last.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["branch"] != "feat/metadata" {
		t.Fatalf("payload[\"branch\"] = %v, want feat/metadata", payload["branch"])
	}
	if payload["pr_url"] != "https://github.com/example/repo/pull/42" {
		t.Fatalf("payload[\"pr_url\"] = %v, want https://github.com/example/repo/pull/42", payload["pr_url"])
	}
	if payload["commit_sha"] != "abc123def456" {
		t.Fatalf("payload[\"commit_sha\"] = %v, want abc123def456", payload["commit_sha"])
	}
}

func TestOperatorCloseIssueVersionConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Version conflict on operator close",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use a wrong version to trigger conflict.
	wrongVersion := issue.Version + 99
	_, err = OperatorCloseIssue(context.Background(), db, issue.ID, core.OperatorCloseIssueRequest{
		Resolution:      "cancelled",
		ExpectedVersion: wrongVersion,
		Actor:           "operator",
		Reason:          "testing version conflict",
	})
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrConflict {
		t.Fatalf("error code = %q, want %q", apiErr.Code, core.ErrConflict)
	}
}

func TestOperatorReopenIssueRequiresExplicitReasonAndEmitsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Reopen me",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OperatorCloseIssue(context.Background(), db, issue.ID, core.OperatorCloseIssueRequest{
		Resolution:      "cancelled",
		ExpectedVersion: issue.Version,
		Actor:           "operator",
		Reason:          "superseded",
	}); err != nil {
		t.Fatal(err)
	}
	closed, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := OperatorReopenIssue(context.Background(), db, issue.ID, core.OperatorReopenIssueRequest{
		ExpectedVersion: closed.Version,
		Actor:           "operator",
	}); err == nil {
		t.Fatal("OperatorReopenIssue() succeeded without a reason")
	}

	reopened, err := OperatorReopenIssue(context.Background(), db, issue.ID, core.OperatorReopenIssueRequest{
		ExpectedVersion: closed.Version,
		Actor:           "operator",
		Reason:          "work needs reassessment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != "open" || reopened.ClosedAt != "" {
		t.Fatalf("unexpected reopened issue: %+v", reopened)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.EventType != "issue_reopened" {
		t.Fatalf("event_type = %q, want issue_reopened", last.EventType)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["from_status"] != "cancelled" || payload["to_status"] != "open" ||
		payload["reason"] != "work needs reassessment" {
		t.Fatalf("unexpected reopen payload: %v", payload)
	}
}

func TestOperatorReleaseIssueClearsStuckLeaseWithoutClosing(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Abandoned claim",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a script that claimed the issue and then crashed before ever
	// persisting the lease token: the claim response is discarded here.
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "flaky-script", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Cannot release without --reason.
	if _, err := OperatorReleaseIssue(context.Background(), db, issue.ID, core.OperatorReleaseIssueRequest{
		ExpectedVersion: claim.Version,
		Actor:           "operator",
	}); err == nil {
		t.Fatal("OperatorReleaseIssue() succeeded without a reason")
	}

	// Cannot release an issue that isn't actually in_progress.
	otherIssue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Never claimed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OperatorReleaseIssue(context.Background(), db, otherIssue.ID, core.OperatorReleaseIssueRequest{
		ExpectedVersion: otherIssue.Version,
		Actor:           "operator",
		Reason:          "should be rejected",
	}); err == nil {
		t.Fatal("OperatorReleaseIssue() succeeded on a non-in_progress issue")
	}

	released, err := OperatorReleaseIssue(context.Background(), db, issue.ID, core.OperatorReleaseIssueRequest{
		ExpectedVersion: claim.Version,
		Actor:           "operator",
		Reason:          "flaky-script crashed before persisting the lease token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != "open" {
		t.Fatalf("expected status 'open' after release, got %q", released.Status)
	}

	// The lease must actually be gone, so a fresh claim succeeds immediately
	// instead of waiting out the original TTL.
	_, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease != nil {
		t.Fatalf("expected no active lease after release, got %+v", lease)
	}
	if _, err := ClaimIssue(context.Background(), db, issue.ID, "another-agent", 3600); err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReleased bool
	for _, e := range events {
		if e.EventType == "issue_operator_released" {
			sawReleased = true
			var payload map[string]any
			if err := json.Unmarshal([]byte(e.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["reason"] != "flaky-script crashed before persisting the lease token" {
				t.Fatalf("unexpected release payload: %v", payload)
			}
			if payload["lease_generation"] != float64(claim.LeaseGeneration) || payload["attempt_id"] != claim.AttemptID {
				t.Fatalf("operator release lost lease identity: %v", payload)
			}
		}
	}
	if !sawReleased {
		t.Fatal("expected an issue_operator_released event")
	}
}

func TestCloseIssueIncludesIssueExternalKeyInPayloadAndResult(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "Close me",
		ExternalKey: "temporal:workflow-456",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "tester", 60)
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err = GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := CloseIssue(context.Background(), db, issue.ID, core.CloseIssueRequest{
		Resolution:      "done",
		ExpectedVersion: issue.Version,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalKey != "temporal:workflow-456" {
		t.Fatalf("result external_key = %q, want %q", result.ExternalKey, "temporal:workflow-456")
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	var payload map[string]any
	if err := json.Unmarshal([]byte(last.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["external_key"] != "temporal:workflow-456" {
		t.Fatalf("payload external_key = %v, want %q", payload["external_key"], "temporal:workflow-456")
	}
}

func TestCreateIssueAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "Event \"test\"",
		ExternalKey: "gh://abevz/af-coordinator/issues/26",
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "issue_created" {
		t.Errorf("expected event_type 'issue_created', got %q", events[0].EventType)
	}
	if !json.Valid([]byte(events[0].PayloadJSON)) {
		t.Fatalf("expected valid JSON payload, got %q", events[0].PayloadJSON)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["external_key"] != "gh://abevz/af-coordinator/issues/26" {
		t.Fatalf("event external_key = %q, want %q", payload["external_key"], "gh://abevz/af-coordinator/issues/26")
	}
}

func TestClaimIssueAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Claim event"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have issue_created + issue_claimed.
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
	found := false
	for _, e := range events {
		if e.EventType == "issue_claimed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'issue_claimed'")
	}
}

func TestReleaseLeaseAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Release event"})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	err = ReleaseLease(context.Background(), db, issue.ID, claim.LeaseToken, claim.LeaseGeneration, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have issue_created + issue_claimed + issue_released.
	found := false
	for _, e := range events {
		if e.EventType == "issue_released" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'issue_released'")
	}
}

func TestHandoffLeaseRecordsNoteBeforeHandoffRelease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Atomic handoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := HandoffLease(context.Background(), db, issue.ID, core.HandoffRequest{
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		Note:            "HANDOFF: implementation is ready for review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Note.Author != "agent-1" || resp.Note.Body != "HANDOFF: implementation is ready for review" {
		t.Fatalf("unexpected handoff note: %+v", resp.Note)
	}

	updated, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "open" {
		t.Fatalf("status = %q, want open", updated.Status)
	}
	if lease != nil {
		t.Fatalf("lease = %+v, want nil", lease)
	}

	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].ID != resp.Note.ID {
		t.Fatalf("notes = %+v, want returned handoff note", notes)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	var noteAdded, released *core.Event
	for i := range events {
		event := &events[i]
		if strings.Contains(event.PayloadJSON, claim.LeaseToken) {
			t.Fatalf("event %s leaked lease token: %s", event.EventType, event.PayloadJSON)
		}
		switch event.EventType {
		case "note_added":
			noteAdded = event
		case "issue_released":
			released = event
		}
	}
	if noteAdded == nil || released == nil {
		t.Fatalf("expected note_added and issue_released events, got %+v", events)
	}
	if noteAdded.Sequence >= released.Sequence {
		t.Fatalf("note sequence %d must precede release sequence %d", noteAdded.Sequence, released.Sequence)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(released.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["attempt_id"] != claim.AttemptID || payload["end_reason"] != "handoff" {
		t.Fatalf("release payload = %+v", payload)
	}
}

func TestHandoffLeaseRejectsInvalidOrExpiredLeaseWithoutPartialState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  func(core.ClaimResponse) core.HandoffRequest
		expire   bool
		wantCode string
	}{
		{
			name: "missing note",
			request: func(claim core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{LeaseToken: claim.LeaseToken}
			},
			wantCode: core.ErrValidationFailed,
		},
		{
			name: "malformed note",
			request: func(claim core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{LeaseToken: claim.LeaseToken, Note: "next steps"}
			},
			wantCode: core.ErrValidationFailed,
		},
		{
			name: "missing token",
			request: func(core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{Note: "HANDOFF: next steps"}
			},
			wantCode: core.ErrValidationFailed,
		},
		{
			name: "wrong token",
			request: func(claim core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{
					LeaseToken:      "wrong-token",
					LeaseGeneration: claim.LeaseGeneration,
					Note:            "HANDOFF: next steps",
				}
			},
			wantCode: core.ErrLeaseExpired,
		},
		{
			name: "wrong generation",
			request: func(claim core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{
					LeaseToken:      claim.LeaseToken,
					LeaseGeneration: claim.LeaseGeneration + 1,
					Note:            "HANDOFF: next steps",
				}
			},
			wantCode: core.ErrLeaseExpired,
		},
		{
			name: "expired token",
			request: func(claim core.ClaimResponse) core.HandoffRequest {
				return core.HandoffRequest{
					LeaseToken:      claim.LeaseToken,
					LeaseGeneration: claim.LeaseGeneration,
					Note:            "HANDOFF: next steps",
				}
			},
			expire:   true,
			wantCode: core.ErrLeaseExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: tt.name})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
			if err != nil {
				t.Fatal(err)
			}
			if tt.expire {
				if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339), issue.ID); err != nil {
					t.Fatal(err)
				}
			}

			_, err = HandoffLease(context.Background(), db, issue.ID, tt.request(claim))
			if err == nil {
				t.Fatal("expected handoff error")
			}
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tt.wantCode {
				t.Fatalf("error = %v, want API code %q", err, tt.wantCode)
			}

			var leaseCount int
			if err := db.QueryRow(`SELECT count(*) FROM leases WHERE issue_id = ?`, issue.ID).Scan(&leaseCount); err != nil {
				t.Fatal(err)
			}
			if leaseCount != 1 {
				t.Fatalf("lease count = %d, want 1", leaseCount)
			}
			notes, err := ListNotes(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != 0 {
				t.Fatalf("notes = %+v, want none", notes)
			}
			events, err := ListEvents(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 {
				t.Fatalf("events = %+v, want only issue_created and issue_claimed", events)
			}
		})
	}
}

func TestStaleGenerationHandoffFailsAfterReclaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Reclaim handoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}
	second, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// The replacement holds the current token, but the presented generation is
	// the stale pre-reclaim value; the generation CAS must reject the handoff
	// before any note or event is written.
	_, err = HandoffLease(context.Background(), db, issue.ID, core.HandoffRequest{
		LeaseToken:      second.LeaseToken,
		LeaseGeneration: first.LeaseGeneration,
		Note:            "HANDOFF: stale handoff",
	})
	if err == nil {
		t.Fatal("stale generation handoff succeeded after reclaim")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("stale generation handoff error = %v, want lease_expired", err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("stale generation handoff changed status to %q", got.Status)
	}
	if lease == nil || lease.LeaseGeneration != second.LeaseGeneration {
		t.Fatalf("replacement lease = %+v, want generation %d", lease, second.LeaseGeneration)
	}
	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("stale generation handoff wrote notes: %+v", notes)
	}
}

func TestHandoffLeaseRollsBackWhenNoteOrReleaseWriteFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		triggerSQL string
	}{
		{
			name: "note write",
			triggerSQL: `CREATE TRIGGER fail_handoff_note BEFORE INSERT ON notes
				BEGIN SELECT RAISE(ABORT, 'note write failed'); END`,
		},
		{
			name: "lease release",
			triggerSQL: `CREATE TRIGGER fail_handoff_release BEFORE DELETE ON leases
				BEGIN SELECT RAISE(ABORT, 'lease release failed'); END`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: tt.name})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tt.triggerSQL); err != nil {
				t.Fatal(err)
			}

			if _, err := HandoffLease(context.Background(), db, issue.ID, core.HandoffRequest{
				LeaseToken:      claim.LeaseToken,
				LeaseGeneration: claim.LeaseGeneration,
				Note:            "HANDOFF: retry after repair",
			}); err == nil {
				t.Fatal("expected handoff failure")
			}

			updated, lease, err := GetIssue(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != "in_progress" || lease == nil {
				t.Fatalf("partial handoff changed issue: status=%q lease=%+v", updated.Status, lease)
			}
			notes, err := ListNotes(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != 0 {
				t.Fatalf("notes = %+v, want rollback", notes)
			}
			events, err := ListEvents(context.Background(), db, issue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 {
				t.Fatalf("events = %+v, want rollback", events)
			}
		})
	}
}

func TestCreateNoteAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Note event"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateNote(context.Background(), db, issue.ID, core.CreateNoteRequest{Author: "tester", Body: "A note"})
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have issue_created + note_added.
	found := false
	for _, e := range events {
		if e.EventType == "note_added" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'note_added'")
	}
}

func TestAddDependencyAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}

	err = AddDependency(context.Background(), db, i1.ID, core.AddDependencyRequest{DependsOn: i2.ID, Kind: "blocks"})
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, i1.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have issue_created + dependency_added.
	found := false
	for _, e := range events {
		if e.EventType == "dependency_added" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'dependency_added'")
	}
}

func TestRemoveDependencyAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Src"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Dst"})
	if err != nil {
		t.Fatal(err)
	}

	err = AddDependency(context.Background(), db, i1.ID, core.AddDependencyRequest{DependsOn: i2.ID, Kind: "blocks"})
	if err != nil {
		t.Fatal(err)
	}

	err = RemoveDependency(context.Background(), db, i1.ID, i2.ID, "blocks", "test")
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, i1.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have issue_created + dependency_added + dependency_removed.
	found := false
	for _, e := range events {
		if e.EventType == "dependency_removed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'dependency_removed'")
	}
}

func TestListIssuesFilterByAssignee(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Unassigned"})

	// Update assignee directly.
	_, err = db.Exec("UPDATE issues SET assignee = 'alice' WHERE title = 'Unassigned'")
	if err != nil {
		t.Fatal(err)
	}

	assigned, err := ListIssues(context.Background(), db, core.IssueListParams{Assignee: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 1 {
		t.Errorf("expected 1 issue assigned to alice, got %d", len(assigned))
	}
}

func TestListIssuesEmpty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	// Should return empty slice, not nil.
	if issues == nil {
		t.Error("expected non-nil slice when no issues")
	}
}

func TestListIssuesShowsHolder(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Leased issue"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-42", 3600)
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Holder != "agent-42" {
		t.Errorf("expected holder 'agent-42', got %q", issues[0].Holder)
	}
}

func TestListIssuesWithAssigneeFilter(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Alpha"})

	_, err = db.Exec("UPDATE issues SET assignee = ? WHERE title = ?", "bob", "Alpha")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{Assignee: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Errorf("expected 1 issue for bob, got %d", len(issues))
	}
}

func TestListIssuesReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if issues == nil {
		t.Error("expected []core.Issue{}, got nil")
	}
}

func TestGetIssueIncludesLeasedFields(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Lease view"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-7", 3600)
	if err != nil {
		t.Fatal(err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected lease to be non-nil")
	}
	if got.Holder != "agent-7" {
		t.Errorf("expected issue.Holder 'agent-7', got %q", got.Holder)
	}
	if lease.Holder != "agent-7" {
		t.Errorf("expected lease.Holder 'agent-7', got %q", lease.Holder)
	}
}

func TestGetIssueWithLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Lease test"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected lease for claimed issue")
	}
	if lease.Holder != "agent-1" {
		t.Errorf("expected holder 'agent-1', got %q", lease.Holder)
	}
	if got.Holder != "agent-1" {
		t.Errorf("expected issue.holder 'agent-1', got %q", got.Holder)
	}
}

func TestCreateIssueWithWorktreeScope(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _, err := UpsertWorktree(context.Background(), db, repo.ID, core.CreateWorktreeRequest{
		Repo:         repo.ID,
		AbsolutePath: "/worktrees/feature",
		Branch:       "feature-x",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "worktree",
		Repo:      repo.ID,
		Worktree:  wt.ID,
		Title:     "Worktree-scoped",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.WorktreeID != wt.ID {
		t.Errorf("expected worktree_id %q, got %q", wt.ID, issue.WorktreeID)
	}
	if issue.RepositoryID != repo.ID {
		t.Errorf("expected repository_id %q, got %q", repo.ID, issue.RepositoryID)
	}
	if issue.ScopeKind != "worktree" {
		t.Errorf("expected scope_kind 'worktree', got %q", issue.ScopeKind)
	}
}

func TestUpdateIssueAssignment(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Assign me"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Assignee:        "alice",
		ExpectedVersion: issue.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Assignee != "alice" {
		t.Errorf("expected assignee 'alice', got %q", updated.Assignee)
	}
}

func TestUpdateIssueLeaseTokenRequired(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Lease locked"})
	if err != nil {
		t.Fatal(err)
	}

	// Claim the issue.
	_, err = ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to update without providing the lease_token should fail.
	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "Hack attempt",
		ExpectedVersion: 2,
	})
	if err == nil {
		t.Fatal("expected error for missing lease_token on leased issue")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrLeaseExpired {
		t.Errorf("expected code %q, got %q", core.ErrLeaseExpired, apiErr.Code)
	}
}

func TestExpiredLeaseUpdateFailsWithoutMutation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Expired owner update",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}

	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "stale owner write after expiry",
		ExpectedVersion: before.Version,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
	})
	if err == nil {
		t.Fatal("expired lease update succeeded before reclaim")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("expired lease update error = %v, want lease_expired", err)
	}

	after, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != before.Title || after.Status != before.Status || after.Version != before.Version {
		t.Fatalf("expired lease update changed issue: before=%+v after=%+v", before, after)
	}
	eventsAfter, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("expired lease update appended events: %d -> %d", len(eventsBefore), len(eventsAfter))
	}
}

func TestUpdateIssueWithReleaseUsesFencedLease(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Update and release",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "Released update",
		ExpectedVersion: claim.Version,
		LeaseToken:      claim.LeaseToken,
		LeaseGeneration: claim.LeaseGeneration,
		ReleaseLease:    true,
		Actor:           "agent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Released update" || updated.Status != "open" || updated.Version != claim.Version+1 {
		t.Fatalf("updated issue = %+v, want title/status/version transition", updated)
	}
	_, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease != nil {
		t.Fatalf("lease = %+v, want released", lease)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedEvents := 0
	releasedEvents := 0
	for _, event := range events {
		switch event.EventType {
		case "issue_updated":
			updatedEvents++
		case "issue_released":
			releasedEvents++
		}
	}
	if updatedEvents != 1 || releasedEvents != 1 {
		t.Fatalf("update/release events = %d/%d, want 1/1", updatedEvents, releasedEvents)
	}
}

func TestStaleGenerationUpdateFailsAfterReclaim(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Reclaim update",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ClaimIssue(context.Background(), db, issue.ID, "agent-1", 3600)
	if err != nil {
		t.Fatal(err)
	}
	forcedExpires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE leases SET expires_at = ? WHERE issue_id = ?`, forcedExpires, issue.ID); err != nil {
		t.Fatal(err)
	}
	second, err := ClaimIssue(context.Background(), db, issue.ID, "agent-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The replacement holds the current token, but its generation is the stale
	// pre-reclaim value; the generation CAS must reject the write.
	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		Title:           "stale owner write",
		ExpectedVersion: updated.Version,
		LeaseToken:      second.LeaseToken,
		LeaseGeneration: first.LeaseGeneration,
	})
	if err == nil {
		t.Fatal("stale generation update succeeded after reclaim")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseExpired {
		t.Fatalf("stale generation update error = %v, want lease_expired", err)
	}

	got, lease, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Reclaim update" || got.Status != "in_progress" {
		t.Fatalf("stale generation update changed the issue: %+v", got)
	}
	if lease == nil || lease.LeaseGeneration != second.LeaseGeneration {
		t.Fatalf("replacement lease = %+v, want generation %d", lease, second.LeaseGeneration)
	}
}

func TestCreateIssueDefaultPriority(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Default priority"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Priority != 3 {
		t.Errorf("expected default priority 3, got %d", issue.Priority)
	}
}

func TestCreateIssueCustomPriority(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "High priority",
		Priority:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Priority != 1 {
		t.Errorf("expected priority 1, got %d", issue.Priority)
	}
}

func TestLinkArtifact(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Link test"})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/spec.md",
	})
	if err != nil {
		t.Fatal(err)
	}

	createdAt, err := LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{Artifact: artifact.ID})
	if err != nil {
		t.Fatal(err)
	}
	if createdAt == "" {
		t.Error("expected non-empty created_at")
	}

	refs, err := ListIssueArtifacts(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 artifact ref, got %d", len(refs))
	}
	if refs[0].ID != artifact.ID {
		t.Errorf("expected artifact ID %q, got %q", artifact.ID, refs[0].ID)
	}
	if refs[0].Relation != "implements" {
		t.Errorf("expected relation 'implements', got %q", refs[0].Relation)
	}
}

func TestLinkArtifactCustomRelation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Custom rel"})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "adr",
		RelativePath: "docs/adr.md",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{
		Artifact: artifact.ID,
		Relation: "documents",
	})
	if err != nil {
		t.Fatal(err)
	}

	refs, err := ListIssueArtifacts(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refs[0].Relation != "documents" {
		t.Errorf("expected relation 'documents', got %q", refs[0].Relation)
	}
}

func TestLinkArtifactNonexistentIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := LinkArtifact(context.Background(), db, "nonexistent", core.LinkArtifactRequest{Artifact: "art-1"})
	if err == nil {
		t.Fatal("expected error for nonexistent issue")
	}
}

func TestLinkArtifactNonexistentArtifact(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Link fail"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{Artifact: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
}

func TestLinkArtifactDuplicate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Dup link"})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo:         repo.ID,
		Kind:         "sdd",
		RelativePath: "docs/dup.md",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First link succeeds.
	_, err = LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{Artifact: artifact.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Second link with same relation should fail.
	_, err = LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{Artifact: artifact.ID})
	if err == nil {
		t.Fatal("expected error for duplicate link")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrAlreadyLinked {
		t.Errorf("expected code %q, got %q", core.ErrAlreadyLinked, apiErr.Code)
	}
}

func TestListIssueArtifactsEmpty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "No artifacts"})
	if err != nil {
		t.Fatal(err)
	}

	refs, err := ListIssueArtifacts(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 artifact refs, got %d", len(refs))
	}
}

func TestCreateNote(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Note this"})
	if err != nil {
		t.Fatal(err)
	}

	note, err := CreateNote(context.Background(), db, issue.ID, core.CreateNoteRequest{Author: "me", Body: "A comment"})
	if err != nil {
		t.Fatal(err)
	}
	if note.Author != "me" {
		t.Errorf("expected author 'me', got %q", note.Author)
	}
	if note.Body != "A comment" {
		t.Errorf("expected body 'A comment', got %q", note.Body)
	}
	if note.IssueID != issue.ID {
		t.Errorf("expected issue_id %q, got %q", issue.ID, note.IssueID)
	}

	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Body != "A comment" {
		t.Errorf("expected body 'A comment', got %q", notes[0].Body)
	}
}

func TestCreateNoteNonexistentIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateNote(context.Background(), db, "nonexistent", core.CreateNoteRequest{Author: "me", Body: "fail"})
	if err == nil {
		t.Fatal("expected error for nonexistent issue")
	}
}

func TestListNotesEmpty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Quiet"})
	if err != nil {
		t.Fatal(err)
	}

	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("expected 0 notes, got %d", len(notes))
	}
}

func TestListNotesNonexistentIssue(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := ListNotes(context.Background(), db, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent issue")
	}
}

func TestMultipleNotes(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Chatty"})
	if err != nil {
		t.Fatal(err)
	}

	CreateNote(context.Background(), db, issue.ID, core.CreateNoteRequest{Author: "alice", Body: "First"})
	CreateNote(context.Background(), db, issue.ID, core.CreateNoteRequest{Author: "bob", Body: "Second"})

	notes, err := ListNotes(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].Body != "First" {
		t.Errorf("expected first note body 'First', got %q", notes[0].Body)
	}
	if notes[1].Body != "Second" {
		t.Errorf("expected second note body 'Second', got %q", notes[1].Body)
	}
}

func TestDependencyKindDefaults(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}

	// Empty kind should default to "blocks".
	err = AddDependency(context.Background(), db, i1.ID, core.AddDependencyRequest{DependsOn: i2.ID, Kind: ""})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIssueDescriptionDefault(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Desc check"})
	if err != nil {
		t.Fatal(err)
	}
	// Default description should be empty string.
	if issue.Description != "" {
		t.Errorf("expected empty description, got %q", issue.Description)
	}
}

func TestIssueWithDescription(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:   "project",
		Title:       "With desc",
		Description: "A detailed description",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Description != "A detailed description" {
		t.Errorf("expected description %q, got %q", "A detailed description", issue.Description)
	}
}

func TestListEventsEmpty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Eventless"})
	if err != nil {
		t.Fatal(err)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event (the create event), got %d", len(events))
	}
}

func TestListGlobalEvents(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	now := time.Now().UTC()
	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range []struct {
		id      string
		shortID string
		title   string
	}{
		{"issue-1", "test-1", "One"},
		{"issue-2", "test-2", "Two"},
		{"issue-3", "test-3", "Three"},
	} {
		_, err = db.Exec(
			`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
			 VALUES (?, ?, (SELECT id FROM projects WHERE key = 'test'), 'project', ?, '', 'open', 3, '', 1, ?, ?)`,
			issue.id, issue.shortID, issue.title, now.Format(time.RFC3339), now.Format(time.RFC3339),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at)
		 VALUES
		 ('evt-1', 'issue-1', 'system', 'issue.created', '{"n":1}', ?),
		 ('evt-2', 'issue-2', 'system', 'issue.updated', '{"n":2}', ?),
		 ('evt-3', 'issue-3', 'system', 'issue.closed', '{"n":3}', ?)`,
		now.Format(time.RFC3339),
		now.Add(time.Second).Format(time.RFC3339),
		now.Add(2*time.Second).Format(time.RFC3339),
	)
	if err != nil {
		t.Fatal(err)
	}

	page, err := ListGlobalEvents(context.Background(), db, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("expected 2 events in first page, got %d", len(page.Events))
	}
	if page.NextSince == "" {
		t.Fatal("expected non-empty next_since")
	}
	if !json.Valid([]byte(page.Events[0].PayloadJSON)) || !json.Valid([]byte(page.Events[1].PayloadJSON)) {
		t.Fatal("expected valid JSON payloads in first page")
	}

	nextPage, err := ListGlobalEvents(context.Background(), db, page.NextSince, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(nextPage.Events) != 1 {
		t.Fatalf("expected 1 event in second page, got %d", len(nextPage.Events))
	}
	if nextPage.Events[0].ID != "evt-3" {
		t.Fatalf("expected evt-3 in second page, got %q", nextPage.Events[0].ID)
	}
}

func TestListRecentEventsStartsAtNewest(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	for _, id := range []string{"first", "second", "third"} {
		if _, err := db.Exec(`INSERT INTO events (id, actor, event_type, payload_json, created_at) VALUES (?, 'test', 'test.event', '{}', ?)`, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListRecentEvents(ctx, db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].ID != "second" || page.Events[1].ID != "third" {
		t.Fatalf("recent events = %#v", page.Events)
	}
	if _, err := db.Exec(`INSERT INTO events (id, actor, event_type, payload_json, created_at) VALUES ('fourth', 'test', 'test.event', '{}', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	followup, err := ListGlobalEvents(ctx, db, page.NextSince, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(followup.Events) != 1 || followup.Events[0].ID != "fourth" {
		t.Fatalf("events after recent cursor = %#v", followup.Events)
	}
}

func TestListGlobalEventsInvalidCursor(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := ListGlobalEvents(context.Background(), db, "bad-cursor", 100)
	if err == nil {
		t.Fatal("expected invalid cursor error")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Fatalf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}

	unknown, err := encodeEventCursor(eventCursor{Sequence: 999})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ListGlobalEvents(context.Background(), db, unknown, 100)
	if err == nil {
		t.Fatal("expected unknown v2 cursor error")
	}
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrValidationFailed {
		t.Fatalf("expected validation error for unknown v2 cursor, got %v", err)
	}
}

func TestEventSequenceOrdersSameSecondTimelineAndPagination(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	project, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	const createdAt = "2026-07-13T20:00:00Z"
	if _, err := db.Exec(
		`INSERT INTO issues (id, short_id, project_id, scope_kind, title, description, status, priority, assignee, version, created_at, updated_at)
		 VALUES ('issue-1', 'test-1', ?, 'project', 'Sequence', '', 'open', 3, '', 1, ?, ?)`,
		project.ID, createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, issue_id, actor, event_type, payload_json, created_at) VALUES
		 ('event-z', 'issue-1', 'agent', 'first_inserted', '{}', ?),
		 ('event-a', 'issue-1', 'agent', 'second_inserted', '{}', ?),
		 ('event-m', 'issue-1', 'agent', 'third_inserted', '{}', ?)`,
		createdAt, createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}

	timeline, err := ListEvents(context.Background(), db, "issue-1")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"event-z", "event-a", "event-m"}
	if len(timeline) != len(wantIDs) {
		t.Fatalf("timeline events = %d, want %d", len(timeline), len(wantIDs))
	}
	for i, want := range wantIDs {
		if timeline[i].ID != want || timeline[i].Sequence != int64(i+1) {
			t.Fatalf("timeline[%d] = %#v, want id=%q sequence=%d", i, timeline[i], want, i+1)
		}
	}

	first, err := ListGlobalEvents(context.Background(), db, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ListGlobalEvents(context.Background(), db, first.NextSince, 1)
	if err != nil {
		t.Fatal(err)
	}
	third, err := ListGlobalEvents(context.Background(), db, second.NextSince, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Events[0].ID != "event-z" || second.Events[0].ID != "event-a" || third.Events[0].ID != "event-m" {
		t.Fatalf("pagination order = %q, %q, %q", first.Events[0].ID, second.Events[0].ID, third.Events[0].ID)
	}
	if first.NextSince[:3] != "v2." {
		t.Fatalf("next_since = %q, want v2 cursor", first.NextSince)
	}

	legacyJSON, err := json.Marshal(legacyEventCursor{ID: "event-z", CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	legacySince := "v1." + base64.RawURLEncoding.EncodeToString(legacyJSON)
	legacyPage, err := ListGlobalEvents(context.Background(), db, legacySince, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyPage.Events) != 2 || legacyPage.Events[0].ID != "event-a" || legacyPage.Events[1].ID != "event-m" {
		t.Fatalf("legacy cursor page = %#v", legacyPage.Events)
	}
}

func TestAddRemoveDependency(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	// A depends on B.
	err = AddDependency(context.Background(), db, i1.ID, core.AddDependencyRequest{DependsOn: i2.ID, Kind: "blocks"})
	if err != nil {
		t.Fatal(err)
	}

	err = RemoveDependency(context.Background(), db, i1.ID, i2.ID, "blocks", "tester")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDependencyNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "X"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Y"})
	if err != nil {
		t.Fatal(err)
	}

	err = RemoveDependency(context.Background(), db, i1.ID, i2.ID, "blocks", "tester")
	if err == nil {
		t.Fatal("expected error for removing nonexistent dependency")
	}
}

func TestDependencyCycleDetection(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	i1, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	// A depends on B.
	err = AddDependency(context.Background(), db, i1.ID, core.AddDependencyRequest{DependsOn: i2.ID, Kind: "blocks"})
	if err != nil {
		t.Fatal(err)
	}

	// B depending on A should create a cycle.
	err = AddDependency(context.Background(), db, i2.ID, core.AddDependencyRequest{DependsOn: i1.ID, Kind: "blocks"})
	if err == nil {
		t.Fatal("expected error for cycle detection")
	}

	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrDependencyCycle {
		t.Errorf("expected code %q, got %q", core.ErrDependencyCycle, apiErr.Code)
	}
}

// TestConcurrentOppositeEdgesCommitAtMostOne races A depends on B against
// B depends on A on the serialized writer. Each edge is individually acyclic,
// but together they form a cycle; exactly one transaction may commit while the
// loser validates against the committed edge and fails with dependency_cycle.
func TestConcurrentOppositeEdgesCommitAtMostOne(t *testing.T) {
	db := newTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	a, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, edge := range []struct{ issue, dependsOn string }{
		{a.ID, b.ID},
		{b.ID, a.ID},
	} {
		wg.Add(1)
		go func(issue, dependsOn string) {
			defer wg.Done()
			<-start
			errs <- AddDependency(context.Background(), db, issue, core.AddDependencyRequest{DependsOn: dependsOn, Kind: "blocks", Actor: "racer"})
		}(edge.issue, edge.dependsOn)
	}
	close(start)
	wg.Wait()
	close(errs)

	committed := 0
	cycles := 0
	for err := range errs {
		if err == nil {
			committed++
			continue
		}
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrDependencyCycle {
			t.Fatalf("concurrent opposite-edge error = %v, want dependency_cycle", err)
		}
		cycles++
	}
	if committed != 1 || cycles != 1 {
		t.Fatalf("concurrent opposite edges: committed=%d cycles=%d, want 1 and 1", committed, cycles)
	}

	var edges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dependencies`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 1 {
		t.Fatalf("committed dependency edges = %d, want 1", edges)
	}
	var added int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'dependency_added'`).Scan(&added); err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("dependency_added events = %d, want 1", added)
	}
}

// TestAddDependencyTraversalFailureAborts proves an injected traversal error is
// returned (never treated as "no cycle") and leaves no edge or event behind.
func TestAddDependencyTraversalFailureAborts(t *testing.T) {
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	a, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected traversal failure")
	err = addDependency(context.Background(), db, a.ID, core.AddDependencyRequest{DependsOn: b.ID, Kind: "blocks", Actor: "tester"},
		func(context.Context, dependencyQueryer, string, string) (bool, error) {
			return false, injected
		})
	if !errors.Is(err, injected) {
		t.Fatalf("traversal failure error = %v, want injected failure", err)
	}

	var edges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dependencies`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("dependency edges after failed traversal = %d, want 0", edges)
	}
	var added int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'dependency_added'`).Scan(&added); err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("dependency_added events after failed traversal = %d, want 0", added)
	}
}

// TestAddDependencyRejectsCrossProjectEndpoints defines the cross-project
// endpoint policy: a dependency edge must stay inside one project, and a
// violation is rejected with validation_failed before any edge or event.
func TestAddDependencyRejectsCrossProjectEndpoints(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "p1", "Project 1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProject(context.Background(), db, "p2", "Project 2", ""); err != nil {
		t.Fatal(err)
	}
	a, err := CreateIssue(context.Background(), db, "p1", core.CreateIssueRequest{ScopeKind: "project", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateIssue(context.Background(), db, "p2", core.CreateIssueRequest{ScopeKind: "project", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	err = AddDependency(context.Background(), db, a.ID, core.AddDependencyRequest{DependsOn: b.ID, Kind: "blocks", Actor: "tester"})
	if err == nil {
		t.Fatal("expected cross-project dependency to be rejected")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != core.ErrValidationFailed {
		t.Fatalf("cross-project error = %v, want validation_failed", err)
	}

	var edges int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dependencies`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("dependency edges after cross-project rejection = %d, want 0", edges)
	}
	var added int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'dependency_added'`).Scan(&added); err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("dependency_added events after cross-project rejection = %d, want 0", added)
	}
}

// TestDependantBecomesReadyAfterBlockerClosed proves ready stays computed, not
// cached: closing the final blocker makes the dependant visible on the next
// ListReadyIssues query.
func TestDependantBecomesReadyAfterBlockerClosed(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	p, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Blocker"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	if err := AddDependency(context.Background(), db, target.ID, core.AddDependencyRequest{DependsOn: blocker.ID, Kind: "blocks", Actor: "planner"}); err != nil {
		t.Fatal(err)
	}

	ready, err := ListReadyIssues(context.Background(), db, p.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range ready {
		if issue.ID == target.ID {
			t.Fatal("target must not be ready while its blocker is unfinished")
		}
	}

	if _, err := OperatorCloseIssue(context.Background(), db, blocker.ID, core.OperatorCloseIssueRequest{
		Resolution:      "done",
		Actor:           "operator",
		Reason:          "completed",
		ExpectedVersion: blocker.Version,
	}); err != nil {
		t.Fatal(err)
	}

	ready, err = ListReadyIssues(context.Background(), db, p.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range ready {
		if issue.ID == target.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("target must be ready after its blocker is closed")
	}
}

func TestCreateIssueDefaultType(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Default type"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.IssueType != "task" {
		t.Errorf("expected issue_type 'task', got %q", issue.IssueType)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IssueType != "task" {
		t.Errorf("expected persisted issue_type 'task', got %q", got.IssueType)
	}
}

func TestCreateIssueWithType(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A bug", IssueType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.IssueType != "bug" {
		t.Errorf("expected issue_type 'bug', got %q", issue.IssueType)
	}
}

func TestAcceptanceCriteriaRoundTrip(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}

	// Create with acceptance criteria; it must persist and be returned.
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind:          "project",
		Title:              "With acceptance",
		AcceptanceCriteria: "- go test ./... passes\n- docs updated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if issue.AcceptanceCriteria != "- go test ./... passes\n- docs updated" {
		t.Errorf("create: acceptance_criteria not returned, got %q", issue.AcceptanceCriteria)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AcceptanceCriteria != issue.AcceptanceCriteria {
		t.Errorf("get: acceptance_criteria not persisted, got %q", got.AcceptanceCriteria)
	}

	// Update under optimistic version; the field must change.
	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		AcceptanceCriteria: "- new criterion",
		ExpectedVersion:    issue.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AcceptanceCriteria != "- new criterion" {
		t.Errorf("update: acceptance_criteria not updated, got %q", updated.AcceptanceCriteria)
	}

	// A create without the field leaves it empty (omitted in JSON).
	plain, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "No acceptance",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.AcceptanceCriteria != "" {
		t.Errorf("create without acceptance: expected empty, got %q", plain.AcceptanceCriteria)
	}
}

func TestListIssuesFilterByType(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A task"})
	if err != nil {
		t.Fatal(err)
	}
	bug, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "A bug", IssueType: "bug"})
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListIssues(context.Background(), db, core.IssueListParams{IssueType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 bug, got %d", len(issues))
	}
	if issues[0].ID != bug.ID {
		t.Errorf("expected bug %q, got %q", bug.ID, issues[0].ID)
	}
}

func TestReadyExcludesEpics(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Big epic", IssueType: "epic"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Small task"})
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListReadyIssues(context.Background(), db, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 ready issue, got %d", len(issues))
	}
	if issues[0].ID != task.ID {
		t.Errorf("expected task %q to be ready, got %q", task.ID, issues[0].ID)
	}
}

func TestClaimEpicRejected(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	epic, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Big epic", IssueType: "epic"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ClaimIssue(context.Background(), db, epic.ID, "agent-1", 60)
	if err == nil {
		t.Fatal("expected error claiming an epic")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Errorf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}

func TestUpdateIssueType(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Reclassify me"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		IssueType:       "bug",
		ExpectedVersion: issue.Version,
		Actor:           "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IssueType != "bug" {
		t.Errorf("expected issue_type 'bug', got %q", updated.IssueType)
	}

	// Invalid type is rejected.
	_, err = UpdateIssue(context.Background(), db, issue.ID, core.UpdateIssueRequest{
		IssueType:       "story",
		ExpectedVersion: updated.Version,
		Actor:           "tester",
	})
	if err == nil {
		t.Fatal("expected error for invalid issue_type")
	}
}

func TestUnlinkArtifact(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	repo, _, err := CreateRepo(context.Background(), db, "test", core.CreateRepoRequest{
		Project:         "test",
		LogicalName:     "repo",
		CanonicalGitDir: "/repos/repo.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "repository", Title: "Unlink test", Repo: repo.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := CreateArtifact(context.Background(), db, repo.ID, core.CreateArtifactRequest{
		Repo: repo.ID, Kind: "sdd", RelativePath: "docs/spec.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LinkArtifact(context.Background(), db, issue.ID, core.LinkArtifactRequest{Artifact: artifact.ID}); err != nil {
		t.Fatal(err)
	}

	// Unlink by relative path (resolved against the issue's repo).
	if err := UnlinkArtifact(context.Background(), db, issue.ID, "docs/spec.md", "", "tester"); err != nil {
		t.Fatal(err)
	}
	refs, err := ListIssueArtifacts(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs after unlink, got %d", len(refs))
	}

	// Unlinking a non-existent link is a not-found error.
	err = UnlinkArtifact(context.Background(), db, issue.ID, artifact.ID, "", "tester")
	if err == nil {
		t.Fatal("expected not-found error unlinking an absent link")
	}
	if apiErr, ok := err.(core.APIError); !ok || apiErr.Code != core.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestClaimIssueSameHolderCannotRecoverToken proves holder attribution is not
// authorization. Repeating the public holder string must not return or renew
// the current owner's secret capability.
func TestClaimIssueSameHolderCannotRecoverToken(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := CreateProject(context.Background(), db, "claim-auth", "Claim Authorization", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "claim-auth", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Holder is not authentication",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := ClaimIssueWithSession(context.Background(), db, issue.ID, "agent-1", 3600, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	before, leaseBefore, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leaseBefore == nil {
		t.Fatal("expected active lease")
	}
	eventsBefore, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, holder := range []string{"agent-1", "agent-2"} {
		_, err := ClaimIssueWithSession(context.Background(), db, issue.ID, holder, 7200, "session-retry")
		var apiErr core.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != core.ErrLeaseHeld {
			t.Fatalf("claim by %q error = %v, want lease_held", holder, err)
		}
	}

	after, leaseAfter, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version || len(eventsBefore) != len(eventsAfter) {
		t.Fatalf("rejected claim changed issue/events: version %d -> %d, events %d -> %d", before.Version, after.Version, len(eventsBefore), len(eventsAfter))
	}
	if leaseAfter == nil || leaseAfter.AttemptID != first.AttemptID || leaseAfter.LeaseGeneration != first.LeaseGeneration || leaseAfter.ExpiresAt != leaseBefore.ExpiresAt {
		t.Fatalf("rejected claim changed active lease: before=%+v after=%+v", leaseBefore, leaseAfter)
	}
}

func TestIssueRunProcessMetadataOnlyForActiveTypedSession(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateProject(ctx, db, "pidtest", "PID test", ""); err != nil {
		t.Fatal(err)
	}
	makeIssue := func(title string) core.Issue {
		issue, err := CreateIssue(ctx, db, "pidtest", core.CreateIssueRequest{ScopeKind: "project", Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	local := makeIssue("local")
	remote := makeIssue("remote")
	manual := makeIssue("manual")
	selfReported := makeIssue("self reported")
	if _, err := ClaimIssueWithSession(ctx, db, local.ID, "agent-a", 60, "dibs-run:v1:host-a:1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssueWithSession(ctx, db, remote.ID, "agent-b", 60, "dibs-run:v1:host-b:1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssueWithSession(ctx, db, manual.ID, "agent-c", 60, "ordinary-session"); err != nil {
		t.Fatal(err)
	}
	// The session ID is caller supplied; even a manual claimant can report a
	// typed PID. The display must describe it as unverified advisory metadata.
	if _, err := ClaimIssueWithSession(ctx, db, selfReported.ID, "manual-agent", 60, "dibs-claim:v1:claimed-host:999"); err != nil {
		t.Fatal(err)
	}
	list, err := ListIssues(ctx, db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.Issue{}
	for _, i := range list {
		byID[i.ID] = i
	}
	if byID[local.ID].LeasePID != 1234 || byID[local.ID].LeaseHost != "host-a" {
		t.Fatalf("local metadata=%+v", byID[local.ID])
	}
	if byID[remote.ID].LeasePID != 1234 || byID[remote.ID].LeaseHost != "host-b" {
		t.Fatalf("remote metadata=%+v", byID[remote.ID])
	}
	if byID[manual.ID].LeasePID != 0 || byID[manual.ID].LeaseHost != "" {
		t.Fatalf("manual metadata=%+v", byID[manual.ID])
	}
	if byID[selfReported.ID].LeasePID != 999 || byID[selfReported.ID].LeaseHost != "claimed-host" {
		t.Fatalf("self reported metadata=%+v", byID[selfReported.ID])
	}
	got, _, err := GetIssue(ctx, db, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LeasePID != 1234 || got.LeaseHost != "host-a" {
		t.Fatalf("get metadata=%+v", got)
	}
	if _, err := db.ExecContext(ctx, "UPDATE leases SET expires_at = ? WHERE issue_id = ?", time.Now().UTC().Add(-time.Second).Format(time.RFC3339), local.ID); err != nil {
		t.Fatal(err)
	}
	list, err = ListIssues(ctx, db, core.IssueListParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range list {
		if i.ID == local.ID && (i.LeasePID != 0 || i.LeaseHost != "") {
			t.Fatalf("expired metadata=%+v", i)
		}
	}
}

func TestParseLeaseProcessSessionIDRejectsOtherFormats(t *testing.T) {
	for _, value := range []string{"", "1234", "dibs-run:v1:host:0", "dibs-claim:v1:host:-1", "dibs-run:v1:host:not-a-pid", "dibs-claim:v1:bad host:1", "dibs-run:v2:host:1", "dibs-claim:v2:host:1"} {
		host, pid := parseLeaseProcessSessionID(value)
		if host != "" || pid != 0 {
			t.Fatalf("%q parsed as %s:%d", value, host, pid)
		}
	}
}
