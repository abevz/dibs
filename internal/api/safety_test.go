package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/store/sqlite"
	"github.com/abevz/dibs/migrations"
)

func TestDaemonSafetyFieldsAndMutationLogs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "safety.db")
	socket := filepath.Join(dir, "dibsd.sock")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	st := sqlite.NewStore(db)
	if _, err := st.CreateProject(ctx, "safety", "Safety", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := st.CreateIssue(ctx, "safety", core.CreateIssueRequest{ScopeKind: "project", Title: "test", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "first", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunDaemon(serverCtx, logger, config.Config{
			DBPath: dbPath, SocketPath: socket, SingletonLockHeld: true,
			MigrationsVerifiedAtStartup: true, IntegrityVerifiedAtStartup: true,
		}, st)
	}()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("http://unix/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	postBody := `{"holder":"second","ttl_seconds":3600}`
	resp, err := client.Post("http://unix/v1/issues/"+issue.ID+"/claim", "application/json", strings.NewReader(postBody))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || resp.Header.Get("X-Dibs-Error-Code") != core.ErrLeaseHeld {
		t.Fatalf("claim conflict = %d, %v", resp.StatusCode, resp.Header)
	}
	resp, err = client.Get("http://unix/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Status  string            `json:"status"`
		Version string            `json:"version"`
		Safety  core.SafetyHealth `json:"safety"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if health.Status != "ok" || health.Version != build.Version || health.Safety.ActiveLeases != 1 || health.Safety.MutationCounters.ClaimConflicts != 1 || health.Safety.DurableClaimConflicts != 1 ||
		!health.Safety.SingletonLockHeld || !health.Safety.MigrationsVerifiedAtStartup || !health.Safety.IntegrityVerifiedAtStartup ||
		health.Safety.LatestMigration != "0011_rejection_counts.sql" {
		t.Fatalf("health = %+v", health)
	}
	resp, err = client.Get("http://unix/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	var stats map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !bytes.Contains(stats["safety"], []byte(`"top_stale_holders"`)) {
		t.Fatalf("stats safety = %s", stats["safety"])
	}
	if !strings.Contains(logs.String(), `"result_code":"lease_held"`) ||
		!strings.Contains(logs.String(), `"latency_ms"`) ||
		strings.Contains(logs.String(), claim.LeaseToken) {
		t.Fatalf("unsafe or missing mutation log: %s", logs.String())
	}
	client.CloseIdleConnections()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBusyMutationGetsStableTelemetryCode(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	st := sqlite.NewStore(db)
	if _, err := st.CreateProject(ctx, "busy", "Busy", ""); err != nil {
		t.Fatal(err)
	}
	issue, err := st.CreateIssue(ctx, "busy", core.CreateIssueRequest{ScopeKind: "project", Title: "busy", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer other.ExecContext(ctx, `ROLLBACK`)
	_, err = st.ClaimIssueWithOperation(ctx, issue.ID, core.ClaimRequest{Holder: "worker", TTLSeconds: 300})
	if err == nil {
		t.Fatal("expected SQLite busy")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	counters := &mutationCounters{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/issues/{issue_id}/claim", func(w http.ResponseWriter, _ *http.Request) {
		writeIssueMutationError(w, logger, "claim issue", issue.ID, err)
	})
	mux.HandleFunc("POST /v1/projects", handleCreateProject(st, logger))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/issues/"+issue.ID+"/claim", nil)
	observeMutations(mux, logger, counters).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("X-Dibs-Result-Code") != "db_busy" {
		t.Fatalf("busy response = %d %v: %s", recorder.Code, recorder.Header(), recorder.Body)
	}
	snapshot := counters.snapshot()
	if snapshot.DBBusyFailures != 1 || snapshot.TransactionFailures != 1 || !strings.Contains(logs.String(), `"result_code":"db_busy"`) {
		t.Fatalf("busy counters/logs = %+v %s", snapshot, logs.String())
	}
	// The same classification applies to non-issue writes, not only lifecycle
	// handlers, while the public error body remains internal_error.
	project := httptest.NewRecorder()
	projectRequest := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"key":"second","name":"Second"}`))
	observeMutations(mux, logger, counters).ServeHTTP(project, projectRequest)
	if project.Code != http.StatusInternalServerError || project.Header().Get("X-Dibs-Result-Code") != "db_busy" ||
		!strings.Contains(project.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("busy project response = %d %v: %s", project.Code, project.Header(), project.Body)
	}
	snapshot = counters.snapshot()
	if snapshot.DBBusyFailures != 2 || snapshot.TransactionFailures != 2 {
		t.Fatalf("non-issue busy counters = %+v", snapshot)
	}
}
