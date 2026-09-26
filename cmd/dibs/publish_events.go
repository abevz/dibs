package main

import (
	"encoding/json"
	"fmt"

	"github.com/abevz/dibs/internal/core"
)

type closeRecord struct {
	Event      core.Event
	Resolution string `json:"resolution"`
	Branch     string `json:"branch"`
	PRURL      string `json:"pr_url"`
	CommitSHA  string `json:"commit_sha"`
}

func latestClose(events []core.Event) (closeRecord, error) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType == "issue_operator_closed" {
			return closeRecord{}, fmt.Errorf("latest close was operator-close; GitHub publication supports issue close and issue run")
		}
		if events[i].EventType != "issue_closed" {
			continue
		}
		var record closeRecord
		if err := json.Unmarshal([]byte(events[i].PayloadJSON), &record); err != nil {
			return closeRecord{}, fmt.Errorf("invalid close event: %w", err)
		}
		record.Event = events[i]
		if record.Resolution != "done" && record.Resolution != "cancelled" {
			return closeRecord{}, fmt.Errorf("close event has no valid resolution")
		}
		return record, nil
	}
	return closeRecord{}, fmt.Errorf("closed issue has no issue_closed event")
}

// closingNote requires the note_added event inserted by the same close
// transaction. Matching only by timestamp could publish an unrelated note
// written in the same second when the close itself had no note.
func closingNote(events []core.Event, notes []core.Note, closed core.Event) string {
	if !hasCloseNoteEvent(events, closed) {
		return ""
	}
	// The API orders notes by created_at. With timestamp precision of one
	// second, another note by this author in that second remains ambiguous.
	var body string
	for _, note := range notes {
		if note.Author == closed.Actor && note.CreatedAt == closed.CreatedAt {
			body = note.Body
		}
	}
	return body
}

func hasCloseNoteEvent(events []core.Event, closed core.Event) bool {
	for _, event := range events {
		if event.Sequence == closed.Sequence-1 && event.EventType == "note_added" &&
			event.Actor == closed.Actor && event.CreatedAt == closed.CreatedAt {
			return true
		}
	}
	return false
}
