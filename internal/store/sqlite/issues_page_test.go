package sqlite

import (
	"context"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestIssueListPaginationAfterFiltering(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateProject(ctx, db, "alpha", "Alpha", ""); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"a", "b", "c"} {
		if _, err := CreateIssue(ctx, db, "alpha", core.CreateIssueRequest{ScopeKind: "project", Title: title, IssueType: "task"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateIssue(ctx, db, "alpha", core.CreateIssueRequest{ScopeKind: "project", Title: "excluded", IssueType: "bug"}); err != nil {
		t.Fatal(err)
	}
	full, err := ListIssues(ctx, db, core.IssueListParams{IssueType: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 3 {
		t.Fatalf("filtered full count = %d", len(full))
	}
	for offset := range full {
		page, err := ListIssues(ctx, db, core.IssueListParams{IssueType: "task", Limit: 1, Offset: offset})
		if err != nil || len(page) != 1 || page[0].ID != full[offset].ID {
			t.Fatalf("offset %d: %+v, %v", offset, page, err)
		}
	}
	page, err := ListIssues(ctx, db, core.IssueListParams{IssueType: "task", Limit: 1, Offset: 3})
	if err != nil || len(page) != 0 {
		t.Fatalf("past end = %+v, %v", page, err)
	}
}
