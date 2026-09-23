package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestCreateOperationHTTPReplayAndConflict(t *testing.T) {
	server, db := newTestServer(t)
	if _, err := sqlite.CreateProject(context.Background(), db, "create-http", "Create HTTP", ""); err != nil {
		t.Fatal(err)
	}
	request := core.CreateIssueRequest{
		OperationID: "http-create-0001", Project: "create-http", ScopeKind: "project",
		Title: "HTTP create", Actor: "worker-a",
	}
	post := func(req core.CreateIssueRequest) (int, []byte) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(server.URL+"/v1/issues", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var payload json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, payload
	}
	status, firstPayload := post(request)
	if status != http.StatusCreated {
		t.Fatalf("first status = %d, body %s", status, firstPayload)
	}
	status, replayPayload := post(request)
	if status != http.StatusCreated {
		t.Fatalf("replay status = %d, body %s", status, replayPayload)
	}
	var first, replay struct {
		Issue core.Issue `json:"issue"`
	}
	if err := json.Unmarshal(firstPayload, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replayPayload, &replay); err != nil {
		t.Fatal(err)
	}
	if first.Issue.ID != replay.Issue.ID || first.Issue.ShortID != replay.Issue.ShortID || first.Issue.CreatedAt != replay.Issue.CreatedAt {
		t.Fatalf("replay allocated a different issue: %+v vs %+v", first.Issue, replay.Issue)
	}
	request.Title = "changed"
	status, conflictPayload := post(request)
	if status != http.StatusConflict {
		t.Fatalf("conflict status = %d, body %s", status, conflictPayload)
	}
	var conflict core.APIErrorResponse
	if err := json.Unmarshal(conflictPayload, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Error.Code != core.ErrIdempotencyConflict {
		t.Fatalf("conflict code = %s", conflict.Error.Code)
	}
	request.OperationID = "short"
	status, weakPayload := post(request)
	if status != http.StatusBadRequest {
		t.Fatalf("weak id status = %d, body %s", status, weakPayload)
	}

	var issues, events, ledger, nextSeq int
	for _, check := range []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM issues`, &issues},
		{`SELECT count(*) FROM events WHERE event_type = 'issue_created'`, &events},
		{`SELECT count(*) FROM operations WHERE operation_kind = 'create'`, &ledger},
		{`SELECT next_issue_seq FROM projects WHERE key = 'create-http'`, &nextSeq},
	} {
		if err := db.QueryRow(check.query).Scan(check.dest); err != nil {
			t.Fatal(err)
		}
	}
	if issues != 1 || events != 1 || ledger != 1 || nextSeq != 2 {
		t.Fatalf("effects issues=%d events=%d ledger=%d seq=%d", issues, events, ledger, nextSeq)
	}
}
