package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

// invocationEventPayload returns the decoded payload of the last event of the
// given type recorded against issueID.
func invocationEventPayload(t *testing.T, db *sql.DB, issueID, eventType string) map[string]any {
	t.Helper()
	events, err := ListEvents(context.Background(), db, issueID)
	if err != nil {
		t.Fatal(err)
	}
	var payloadJSON string
	for _, event := range events {
		if event.EventType == eventType {
			payloadJSON = event.PayloadJSON
		}
	}
	if payloadJSON == "" {
		t.Fatalf("no %s event was recorded", eventType)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode %s payload %q: %v", eventType, payloadJSON, err)
	}
	return payload
}

// TestClaimRecordsInvocationModeInTheAuditTrail is the afc-95 regression. The
// audit chain answered "who" and was silent on "how": a claim from a human
// running a one-shot launcher and a claim from an unattended daemon were
// recorded identically, and a reader drew a false conclusion from that silence
// which reached a closing note and a P1 issue.
//
// The mode must therefore reach the event payload, where history is read --
// not merely exist as a request field.
func TestClaimRecordsInvocationModeInTheAuditTrail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
		want string
	}{
		{"caller declares interactive", core.InvocationModeInteractive, core.InvocationModeInteractive},
		{"caller declares scheduled", core.InvocationModeScheduled, core.InvocationModeScheduled},
		{"caller declares nothing", "", core.InvocationModeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
				ScopeKind: "project", Title: "invocation mode",
			})
			if err != nil {
				t.Fatal(err)
			}

			if _, err := ClaimIssueWithMode(context.Background(), db, issue.ID, "tester", 3600, "", tt.mode); err != nil {
				t.Fatal(err)
			}

			payload := invocationEventPayload(t, db, issue.ID, "issue_claimed")
			got, ok := payload["invocation_mode"]
			if !ok {
				t.Fatalf("issue_claimed payload has no invocation_mode: %v", payload)
			}
			if got != tt.want {
				t.Errorf("recorded invocation_mode = %v, want %q", got, tt.want)
			}
		})
	}
}

// TestClaimRejectsUnrecognizedInvocationMode pins that an unknown value is
// refused rather than silently normalized. A mode nobody declared must read as
// absence; a mode somebody mistyped must not read as anything at all.
func TestClaimRejectsUnrecognizedInvocationMode(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "invocation mode",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ClaimIssueWithMode(context.Background(), db, issue.ID, "tester", 3600, "", "daemon"); err == nil {
		t.Fatal("ClaimIssueWithMode accepted an unrecognized invocation mode")
	}
}

// TestClaimIssueDefaultsToUnknown pins the compatibility entry point: a caller
// using the older signature records absence, never an inferred mode.
func TestClaimIssueDefaultsToUnknown(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "invocation mode",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ClaimIssue(context.Background(), db, issue.ID, "tester", 3600); err != nil {
		t.Fatal(err)
	}

	payload := invocationEventPayload(t, db, issue.ID, "issue_claimed")
	if payload["invocation_mode"] != core.InvocationModeUnknown {
		t.Errorf("legacy ClaimIssue recorded %v, want %q", payload["invocation_mode"], core.InvocationModeUnknown)
	}
}

// TestRejectedSameHolderClaimDoesNotAppendInvocationEvent proves an
// unauthenticated holder-only retry cannot manufacture a lifecycle event or
// renew the current attempt under a different invocation mode.
func TestRejectedSameHolderClaimDoesNotAppendInvocationEvent(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "invocation mode",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ClaimIssueWithMode(context.Background(), db, issue.ID, "tester", 3600, "", core.InvocationModeInteractive); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssueWithMode(context.Background(), db, issue.ID, "tester", 3600, "", core.InvocationModeScheduled); err == nil {
		t.Fatal("same-holder claim unexpectedly succeeded")
	}

	events, err := ListEvents(context.Background(), db, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventType == "lease_reattached" {
			t.Fatalf("rejected same-holder claim appended lease_reattached: %+v", event)
		}
	}
}

// TestNoteRecordsInvocationMode covers the note path named by the criterion:
// "a claim, note, or close records the invocation mode alongside the actor".
func TestNoteRecordsInvocationMode(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if _, err := CreateProject(context.Background(), db, "test", "Test", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := CreateIssue(context.Background(), db, "test", core.CreateIssueRequest{
		ScopeKind: "project", Title: "invocation mode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimIssueWithMode(context.Background(), db, issue.ID, "tester", 3600, "", core.InvocationModeInteractive); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateNote(context.Background(), db, issue.ID, core.CreateNoteRequest{
		Author: "tester", Body: "a note", InvocationMode: core.InvocationModeScheduled,
	}); err != nil {
		t.Fatal(err)
	}

	payload := invocationEventPayload(t, db, issue.ID, "note_added")
	if payload["invocation_mode"] != core.InvocationModeScheduled {
		t.Errorf("note_added recorded %v, want %q", payload["invocation_mode"], core.InvocationModeScheduled)
	}
}
