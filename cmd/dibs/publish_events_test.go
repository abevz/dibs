package main

import (
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestLatestCloseAndClosingNote(t *testing.T) {
	const when = "2026-09-26T16:45:00Z"
	oldClose := core.Event{Sequence: 4, EventType: "issue_closed", Actor: "agent", CreatedAt: "2026-09-25T16:45:00Z", PayloadJSON: `{"resolution":"done"}`}
	closeEvent := core.Event{Sequence: 12, EventType: "issue_closed", Actor: "agent", CreatedAt: when, PayloadJSON: `{"resolution":"cancelled","branch":"fix/one","pr_url":"https://github.com/o/r/pull/2","commit_sha":"abc123"}`}
	precedingNote := core.Event{Sequence: 11, EventType: "note_added", Actor: "agent", CreatedAt: when}
	events := []core.Event{oldClose, precedingNote, closeEvent}
	record, err := latestClose(events)
	if err != nil || record.Event.Sequence != 12 || record.Resolution != "cancelled" || record.Branch != "fix/one" || record.PRURL == "" || record.CommitSHA != "abc123" {
		t.Fatalf("latestClose = %+v, %v", record, err)
	}
	notes := []core.Note{
		{Author: "agent", CreatedAt: when, Body: "earlier same-second note"},
		{Author: "other", CreatedAt: when, Body: "another author"},
		{Author: "agent", CreatedAt: when, Body: "closing note"},
	}
	if got := closingNote(events, notes, closeEvent); got != "closing note" {
		t.Fatalf("closingNote = %q", got)
	}
	withOperatorClose := append(append([]core.Event(nil), events...), core.Event{ID: "operator-close", Sequence: 14, EventType: "issue_operator_closed", CreatedAt: when})
	if _, err := latestClose(withOperatorClose); err == nil || !strings.Contains(err.Error(), "operator-close") {
		t.Fatalf("latestClose accepted older ordinary close after operator-close: %v", err)
	}
	for _, tc := range []struct {
		name string
		evt  core.Event
	}{
		{"nonadjacent", core.Event{Sequence: 10, EventType: "note_added", Actor: "agent", CreatedAt: when}},
		{"different actor", core.Event{Sequence: 11, EventType: "note_added", Actor: "other", CreatedAt: when}},
		{"different time", core.Event{Sequence: 11, EventType: "note_added", Actor: "agent", CreatedAt: "2026-09-26T16:44:59Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := closingNote([]core.Event{tc.evt, closeEvent}, notes, closeEvent); got != "" {
				t.Fatalf("closingNote = %q, expected none", got)
			}
		})
	}
}

func TestRenderPublishComment(t *testing.T) {
	record := closeRecord{Resolution: "done", Branch: "fix/login", PRURL: "https://github.com/o/r/pull/3", CommitSHA: "abc123"}
	marker := "<!-- dibs:publish issue=uuid close_event=event-one -->"
	got := renderPublishComment("app-7", record, "fixed bug\nsee /local/path", marker)
	for _, part := range []string{"**dibs:** `app-7` closed as **done**.", "- PR: https://github.com/o/r/pull/3", "- Commit: `abc123` on `fix/login`", "> fixed bug\n> see /local/path", marker} {
		if !strings.Contains(got, part) {
			t.Fatalf("comment lacks %q: %s", part, got)
		}
	}
	if strings.Contains(got, "lease_host") || strings.Contains(got, "attempt_id") || strings.Contains(got, "session_id") {
		t.Fatalf("internal metadata leaked: %s", got)
	}
	long := renderPublishComment("app-7", record, strings.Repeat("é", 2001), marker)
	if strings.Count(long, "é") != 1999 {
		t.Fatal("closing note was not limited to 2000 runes")
	}
	if !strings.Contains(long, strings.Repeat("é", 1999)+"…") {
		t.Fatal("long note lacks rune-safe ellipsis")
	}
}

func TestValidatePublicTextExactTokens(t *testing.T) {
	t.Setenv("DIBS_LEASE_TOKEN", "")
	t.Setenv("DIBS_OPERATOR_TOKEN", "")
	t.Setenv("AF_OPERATOR_TOKEN", "")
	if err := validatePublicText("ordinary note", "fix/one"); err != nil {
		t.Fatal(err)
	}
	if err := validatePublicText("note with active-secret", "fix/one", "active-secret"); err == nil || strings.Contains(err.Error(), "active-secret") {
		t.Fatalf("active claim token check = %v", err)
	}
	for _, key := range []string{"DIBS_LEASE_TOKEN", "DIBS_OPERATOR_TOKEN", "AF_OPERATOR_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "sample-secret-value")
			if err := validatePublicText("note with sample-secret-value", "fix/one"); err == nil || strings.Contains(err.Error(), "sample-secret-value") {
				t.Fatalf("note check = %v", err)
			}
			if err := validatePublicText("ordinary note", "fix/sample-secret-value"); err == nil || strings.Contains(err.Error(), "sample-secret-value") {
				t.Fatalf("branch check = %v", err)
			}
		})
	}
}
