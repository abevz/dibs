package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
)

func TestLifecycleOperationHTTPReplayAndConflict(t *testing.T) {
	for _, kind := range []string{"heartbeat", "release", "handoff", "update", "close"} {
		t.Run(kind, func(t *testing.T) {
			server, db := newTestServer(t)
			ctx := context.Background()
			if _, err := sqlite.CreateProject(ctx, db, "lifecycle-http", "Lifecycle HTTP", ""); err != nil {
				t.Fatal(err)
			}
			issue, err := sqlite.CreateIssue(ctx, db, "lifecycle-http", core.CreateIssueRequest{ScopeKind: "project", Title: kind, Actor: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := sqlite.ClaimIssue(ctx, db, issue.ID, "worker", 3600)
			if err != nil {
				t.Fatal(err)
			}
			operationID := "http-" + kind + "-0001"
			method, path := http.MethodPost, "/v1/issues/"+issue.ID+"/"+kind
			var body, changed any
			switch kind {
			case "heartbeat":
				base := core.HeartbeatRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TTLSeconds: 1800, OperationID: operationID}
				body = base
				base.TTLSeconds++
				changed = base
			case "release":
				base := core.ReleaseRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, OperationID: operationID}
				body = base
				base.LeaseGeneration++
				changed = base
			case "handoff":
				base := core.HandoffRequest{LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "HANDOFF: review", OperationID: operationID}
				body = base
				base.Note = "HANDOFF: different"
				changed = base
			case "update":
				method, path = http.MethodPatch, "/v1/issues/"+issue.ID
				base := core.UpdateIssueRequest{Title: "updated", Actor: "worker", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, OperationID: operationID}
				body = base
				base.Title = "different"
				changed = base
			case "close":
				base := core.CloseIssueRequest{Resolution: "done", Actor: "worker", ExpectedVersion: claim.Version, LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Note: "finished", OperationID: operationID}
				body = base
				base.Resolution = "cancelled"
				changed = base
			}
			send := func(payload any) (int, []byte) {
				t.Helper()
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				request, err := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return response.StatusCode, data
			}
			firstStatus, first := send(body)
			replayStatus, replay := send(body)
			wantStatus := http.StatusOK
			if kind == "release" {
				wantStatus = http.StatusNoContent
			}
			if firstStatus != wantStatus || replayStatus != wantStatus || !reflect.DeepEqual(first, replay) {
				t.Fatalf("first/replay: (%d,%s) / (%d,%s)", firstStatus, first, replayStatus, replay)
			}
			status, conflict := send(changed)
			if status != http.StatusConflict {
				t.Fatalf("mismatch status = %d, body = %s", status, conflict)
			}
			var envelope core.APIErrorResponse
			if err := json.Unmarshal(conflict, &envelope); err != nil || envelope.Error.Code != core.ErrIdempotencyConflict {
				t.Fatalf("mismatch error = %+v, decode = %v", envelope, err)
			}
		})
	}
}
