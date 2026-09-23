package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestPrintIssueFullShowsDependencyShortAndUUID(t *testing.T) {
	issue := core.Issue{
		ID:        "issue-1",
		ShortID:   "afc-1",
		Status:    "open",
		IssueType: "task",
		Title:     "Test issue",
		Priority:  3,
		ScopeKind: "project",
		Version:   1,
		CreatedAt: "2026-07-07T18:00:00Z",
		UpdatedAt: "2026-07-07T18:00:00Z",
		Dependencies: []core.Dependency{
			{
				IssueID:          "issue-1",
				IssueShortID:     "afc-1",
				DependsOnID:      "issue-2",
				DependsOnShortID: "afc-2",
				Kind:             "related",
			},
		},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	printIssueFull(issue, nil, nil, nil, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "Dependencies:") {
		t.Fatalf("expected dependency section in output, got %q", out)
	}
	if !strings.Contains(out, "related afc-2 [issue-2]") {
		t.Fatalf("expected dependency short id and UUID in output, got %q", out)
	}
}

// TestPrintIssueFullBlockingIsDirectional proves a blocks edge is never shown as
// an ambiguous "blocks <target>" line in the detail view: the blocked side shows
// "Blocked By", the blocking side shows "Blocks", and a status-only block is
// labelled distinctly.
func TestPrintIssueFullBlockingIsDirectional(t *testing.T) {
	captureFull := func(issue core.Issue) string {
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		printIssueFull(issue, nil, nil, nil, nil)
		_ = w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	// Blocked side: a blocks edge renders as "Blocked By", never "blocks afc-2".
	blocked := captureFull(core.Issue{
		ID: "issue-1", ShortID: "afc-1", Status: "open", IssueType: "task", Title: "Blocked",
		Blocked:   true,
		BlockedBy: []string{"afc-2"},
		Dependencies: []core.Dependency{
			{IssueID: "issue-1", DependsOnID: "issue-2", DependsOnShortID: "afc-2", Kind: "blocks"},
		},
	})
	if !strings.Contains(blocked, "Blocked By:    afc-2") {
		t.Fatalf("blocked side must show Blocked By: %q", blocked)
	}
	if strings.Contains(blocked, "blocks afc-2") || strings.Contains(blocked, "Dependencies:") {
		t.Fatalf("a blocks edge must not appear as an ambiguous raw dependency: %q", blocked)
	}

	// Blocking side: reverse Blocks list is shown.
	blocking := captureFull(core.Issue{
		ID: "issue-2", ShortID: "afc-2", Status: "open", IssueType: "task", Title: "Blocker",
		Blocks: []string{"afc-1"},
	})
	if !strings.Contains(blocking, "Blocks:        afc-1") {
		t.Fatalf("blocking side must show Blocks: %q", blocking)
	}

	// Status-only block: no dependency edge, labelled distinctly.
	statusBlocked := captureFull(core.Issue{
		ID: "issue-3", ShortID: "afc-3", Status: "blocked", IssueType: "task", Title: "Held",
		Blocked: true,
	})
	if !strings.Contains(statusBlocked, "Blocked:       yes (status)") {
		t.Fatalf("status-only block must be labelled distinctly: %q", statusBlocked)
	}
}

func TestPrintIssuesTableShowsDependenciesAndBlockedBy(t *testing.T) {
	issue := core.Issue{
		ID:        "issue-1",
		ShortID:   "afc-1",
		Status:    "open",
		IssueType: "feature",
		Title:     "Blocked issue",
		Blocked:   true,
		BlockedBy: []string{"afc-2"},
		Dependencies: []core.Dependency{
			{DependsOnShortID: "afc-2", Kind: "blocks"},
			{DependsOnShortID: "afc-epic", Kind: "parent"},
		},
		Tags: []string{"area/frontend", "exec/auto"},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printIssuesTable([]core.Issue{issue}, nil)
	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"BLOCKED BY", "DEPS", "TAGS", "open [B]", "afc-2", "parent:afc-epic", "area/frontend,exec/auto"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "blocks:afc-2") {
		t.Fatalf("blocking dependency should be represented by BLOCKED BY, not duplicated in DEPS: %q", out)
	}
	if strings.Index(out, "BLOCKED BY") < strings.Index(out, "CLAIMED") {
		t.Fatalf("expected BLOCKED BY after CLAIMED: %q", out)
	}
	if strings.Index(out, "DEPS") < strings.Index(out, "BLOCKED BY") {
		t.Fatalf("expected DEPS after BLOCKED BY: %q", out)
	}
	if strings.Index(out, "TAGS") < strings.Index(out, "DEPS") {
		t.Fatalf("expected TAGS after DEPS: %q", out)
	}
}

func TestPrintIssuesTableRespectsColumnSelection(t *testing.T) {
	issue := core.Issue{
		ID:        "issue-1",
		ShortID:   "afc-1",
		Status:    "open",
		IssueType: "feature",
		Title:     "Narrow row",
		Tags:      []string{"area/frontend"},
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printIssuesTable([]core.Issue{issue}, []string{"short", "status", "title"})
	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{"SHORT", "STATUS", "TITLE", "afc-1", "open"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q: %q", want, out)
		}
	}
	for _, unwanted := range []string{"ID", "TYPE", "ASSIGNEE", "CLAIMED", "BLOCKED BY", "DEPS", "TAGS"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("table output should omit unselected column %q: %q", unwanted, out)
		}
	}
	header := strings.SplitN(out, "\n", 2)[0]
	fullHeaderWidth := 0
	for _, col := range issueColumnDefs() {
		fullHeaderWidth += col.width + 1
	}
	if len(header) >= fullHeaderWidth {
		t.Fatalf("narrow column selection should be shorter than the full %d-char header, got %d chars: %q", fullHeaderWidth, len(header), header)
	}
}

func TestPrintIssueFullShowsExternalKey(t *testing.T) {
	issue := core.Issue{
		ID:          "issue-1",
		ShortID:     "afc-1",
		Status:      "open",
		IssueType:   "task",
		Title:       "Test issue",
		ExternalKey: "gh://abevz/af-coordinator/issues/26",
		Priority:    3,
		ScopeKind:   "project",
		Version:     1,
		CreatedAt:   "2026-07-07T18:00:00Z",
		UpdatedAt:   "2026-07-07T18:00:00Z",
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	printIssueFull(issue, nil, nil, nil, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "External Key:  gh://abevz/af-coordinator/issues/26") {
		t.Fatalf("expected external key in output, got %q", out)
	}
}
