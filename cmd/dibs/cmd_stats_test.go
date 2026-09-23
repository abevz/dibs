package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/report"
	"github.com/abevz/dibs/internal/testsocket"
)

func TestParseStatsArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		want     report.Query
		wantHelp bool
		wantErr  string
	}{
		{
			name: "all filters",
			args: []string{"--project", "afc", "--repo", "repo-id", "--since", "24h", "--until", "2026-07-14T00:00:00Z"},
			want: report.Query{Project: "afc", Repo: "repo-id", Since: "24h", Until: "2026-07-14T00:00:00Z"},
		},
		{name: "help", args: []string{"--help"}, wantHelp: true},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "unknown flag"},
		{name: "missing value", args: []string{"--project"}, wantErr: "requires a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, help, err := parseStatsArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || help != tt.wantHelp || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got query=%#v help=%v err=%v, want query=%#v help=%v", got, help, err, tt.want, tt.wantHelp)
			}
		})
	}
}

func TestStatsHelpDoesNotRequireClient(t *testing.T) {
	if err := runStats(context.Background(), nil, []string{"--help"}); err != nil {
		t.Fatalf("stats help: %v", err)
	}
}

func TestWriteStatsIncludesMetricsAndDataQuality(t *testing.T) {
	var out bytes.Buffer
	writeStats(&out, report.Report{
		Version: "v1",
		Window:  report.Window{Since: "2026-07-13T00:00:00Z", Until: "2026-07-14T00:00:00Z"},
		Scope:   report.Scope{ProjectKey: "afc"},
		Inventory: report.Inventory{Total: 2, Ready: 1, InProgress: 1, ByStatus: map[string]int{
			"open": 1, "in_progress": 1,
		}},
		Flow: report.Flow{Created: 2, Closed: 1, LeadTime: report.Percentiles{SampleSize: 1, P50Seconds: 60, P75Seconds: 60, P90Seconds: 60}},
		Attempts: report.Attempts{
			Claims: 1, Completed: 1,
			Outcomes: map[string]int{"done": 1, "released": 0},
			Duration: report.Percentiles{SampleSize: 1, P50Seconds: 30, P75Seconds: 30, P90Seconds: 30},
			Churn:    report.Coverage{Numerator: 0, Denominator: 1},
		},
		Handoff: report.Coverage{Numerator: 0, Denominator: 1},
		Coverage: report.CoverageSet{
			Notes: report.Coverage{Numerator: 1, Denominator: 2, Ratio: 0.5},
		},
		DataQuality: report.DataQuality{ExactOrderingFromSequence: 17, LegacyEventCount: 2, LegacyEventsIncluded: true},
	})

	for _, want := range []string{
		"Execution statistics (v1)", "Project: afc", "Inventory: 2 total, 1 ready, 1 in progress",
		"Lead time: n=1 p50=60s", "Attempt duration: n=1 p50=30s", "Outcomes: done=1 released=0",
		"Data quality: 2 legacy events in scope; exact ordering starts at sequence 17",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stats output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunStatsJSON(t *testing.T) {
	socketPath := testsocket.PathNamed(t, "stats")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stats" || r.URL.Query().Get("project") != "afc" {
			t.Fatalf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"report": report.Report{Version: "v1"}})
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	if err := runStats(context.Background(), client.New(socketPath), []string{"--project", "afc"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var got report.Report
	if err := json.NewDecoder(reader).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1" {
		t.Fatalf("JSON report version = %q, want v1", got.Version)
	}
}
