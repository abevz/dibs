package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCreateIssueWithTags(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Tagged issue",
		Tags:      []string{"area/frontend", "theme/dark"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issue.Tags) != 2 {
		t.Fatalf("expected 2 tags on create response, got %v", issue.Tags)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "area/frontend" || got.Tags[1] != "theme/dark" {
		t.Errorf("expected sorted tags [area/frontend theme/dark], got %v", got.Tags)
	}
}

func TestAddTagAppendsEventAndGetIssueReturnsTag(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Issue"})
	if err != nil {
		t.Fatal(err)
	}

	if err := AddTag(context.Background(), db, issue.ID, core.AddTagRequest{Tag: "area/frontend", Actor: "tester"}); err != nil {
		t.Fatal(err)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "area/frontend" {
		t.Errorf("expected tags [area/frontend], got %v", got.Tags)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "issue_tagged" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'issue_tagged'")
	}
}

func TestAddTagInvalid(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Issue"})
	if err != nil {
		t.Fatal(err)
	}

	err = AddTag(context.Background(), db, issue.ID, core.AddTagRequest{Tag: "status/open", Actor: "tester"})
	if err == nil {
		t.Fatal("expected error for reserved namespace tag")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrValidationFailed {
		t.Errorf("expected code %q, got %q", core.ErrValidationFailed, apiErr.Code)
	}
}

func TestAddTagDuplicateConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Issue"})
	if err != nil {
		t.Fatal(err)
	}

	if err := AddTag(context.Background(), db, issue.ID, core.AddTagRequest{Tag: "area/frontend", Actor: "tester"}); err != nil {
		t.Fatal(err)
	}

	err = AddTag(context.Background(), db, issue.ID, core.AddTagRequest{Tag: "area/frontend", Actor: "tester"})
	if err == nil {
		t.Fatal("expected error for duplicate tag")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrAlreadyTagged {
		t.Errorf("expected code %q, got %q", core.ErrAlreadyTagged, apiErr.Code)
	}
}

func TestRemoveTagAppendsEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Issue"})
	if err != nil {
		t.Fatal(err)
	}
	if err := AddTag(context.Background(), db, issue.ID, core.AddTagRequest{Tag: "area/frontend", Actor: "tester"}); err != nil {
		t.Fatal(err)
	}

	if err := RemoveTag(context.Background(), db, issue.ID, "area/frontend", "tester"); err != nil {
		t.Fatal(err)
	}

	got, _, err := GetIssue(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("expected no tags after remove, got %v", got.Tags)
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "issue_untagged" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an event with event_type 'issue_untagged'")
	}
}

func TestRemoveTagNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{ScopeKind: "project", Title: "Issue"})
	if err != nil {
		t.Fatal(err)
	}

	err = RemoveTag(context.Background(), db, issue.ID, "area/frontend", "tester")
	if err == nil {
		t.Fatal("expected error for removing nonexistent tag")
	}
	var apiErr core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != core.ErrNotFound {
		t.Errorf("expected code %q, got %q", core.ErrNotFound, apiErr.Code)
	}
}

func TestListIssuesAndListReadyIssuesPopulateTags(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	proj, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project",
		Title:     "Issue",
		Tags:      []string{"exec/auto"},
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := ListIssues(context.Background(), db, core.IssueListParams{Project: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].Tags) != 1 || listed[0].Tags[0] != "exec/auto" {
		t.Errorf("expected ListIssues to populate tags, got %+v", listed)
	}

	ready, err := ListReadyIssues(context.Background(), db, proj.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range ready {
		if r.ID == issue.ID {
			if len(r.Tags) != 1 || r.Tags[0] != "exec/auto" {
				t.Errorf("expected ListReadyIssues to populate tags, got %v", r.Tags)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected created issue to appear in ready list")
	}
}

func TestListIssuesTagFilterANDSemantics(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	both, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Both tags", Tags: []string{"area/frontend", "theme/dark"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "One tag", Tags: []string{"area/frontend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Untagged",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Single tag matches both tagged issues.
	single, err := ListIssues(context.Background(), db, core.IssueListParams{
		Project: "test", Tags: []string{"area/frontend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 2 {
		t.Fatalf("expected 2 issues for single-tag filter, got %d", len(single))
	}

	// Two tags: AND semantics, only the issue with both matches.
	multi, err := ListIssues(context.Background(), db, core.IssueListParams{
		Project: "test", Tags: []string{"area/frontend", "theme/dark"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(multi) != 1 || multi[0].ID != both.ID {
		t.Fatalf("expected only %q to match both tags, got %+v", both.ShortID, multi)
	}

	// Tag with no matches.
	none, err := ListIssues(context.Background(), db, core.IssueListParams{
		Project: "test", Tags: []string{"area/backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no matches for unused tag, got %+v", none)
	}
}

func TestListReadyIssuesTagFilterANDSemantics(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	proj, err := CreateProject(context.Background(), db, "test", "Test", "")
	if err != nil {
		t.Fatal(err)
	}

	both, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Both tags", Tags: []string{"exec/auto", "area/frontend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "One tag", Tags: []string{"exec/auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "Untagged",
	})
	if err != nil {
		t.Fatal(err)
	}

	single, err := ListReadyIssues(context.Background(), db, proj.ID, "", []string{"exec/auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 2 {
		t.Fatalf("expected 2 ready issues for single-tag filter, got %d", len(single))
	}

	multi, err := ListReadyIssues(context.Background(), db, proj.ID, "", []string{"exec/auto", "area/frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if len(multi) != 1 || multi[0].ID != both.ID {
		t.Fatalf("expected only %q to match both tags, got %+v", both.ShortID, multi)
	}

	none, err := ListReadyIssues(context.Background(), db, proj.ID, "", []string{"exec/manual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no ready matches for unused tag, got %+v", none)
	}
}
