package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
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

func TestCloseLatestHTTPRequiresLeaseAndReportsConflict(t *testing.T) {
	server, db := newTestServer(t)
	ctx := t.Context()
	if _, err := sqlite.CreateProject(ctx, db, "latest", "Latest", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := sqlite.CreateIssue(ctx, db, "latest", core.CreateIssueRequest{ScopeKind: "project", Title: "Close"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := sqlite.ClaimIssue(ctx, db, issue.ID, "worker", 900)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version int
		token   string
		status  int
		code    string
	}{
		{core.LatestIssueVersion, "other", http.StatusGone, core.ErrLeaseExpired},
		{issue.Version, claim.LeaseToken, http.StatusConflict, core.ErrConflict},
		{core.LatestIssueVersion, claim.LeaseToken, http.StatusOK, ""},
	} {
		data, err := json.Marshal(core.CloseIssueRequest{
			Resolution: "done", Actor: "worker", ExpectedVersion: tc.version,
			LeaseToken: tc.token, LeaseGeneration: claim.LeaseGeneration,
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(server.URL+"/v1/issues/"+issue.ShortID+"/close", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status {
			t.Fatalf("status = %d, want %d, body = %s, err = %v", resp.StatusCode, tc.status, body, err)
		}
		if tc.code != "" {
			var envelope core.APIErrorResponse
			if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != tc.code {
				t.Fatalf("close error = %s, want %s, decode = %v", body, tc.code, err)
			}
			if tc.code == core.ErrConflict {
				for _, want := range []string{fmt.Sprintf("current version is %d", claim.Version),
					fmt.Sprintf("current lease generation is %d", claim.LeaseGeneration)} {
					if !strings.Contains(envelope.Error.Message, want) {
						t.Errorf("conflict message = %q, missing %q", envelope.Error.Message, want)
					}
				}
			}
		}
	}
}
