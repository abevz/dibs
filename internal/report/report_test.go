package report

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/abevz/dibs/internal/core"
	coordinatorexport "github.com/abevz/dibs/internal/export"
)

func TestBuild(t *testing.T) {
	t.Parallel()
	now := mustTime(t, "2026-07-14T12:00:00Z")

	tests := []struct {
		name   string
		source fixtureSource
		query  Query
		check  func(t *testing.T, got Report)
	}{
		{
			name:   "empty source",
			source: fixtureSource{},
			check: func(t *testing.T, got Report) {
				if got.Version != Version || got.Inventory.Total != 0 || got.Flow.LeadTime.SampleSize != 0 {
					t.Fatalf("unexpected empty report: %#v", got)
				}
				if got.Inventory.ByStatus["open"] != 0 || got.Attempts.Outcomes["handoff"] != 0 {
					t.Fatalf("empty report did not preserve stable zero counts: %#v", got)
				}
			},
		},
		{
			name:   "project scope aggregates flow attempts and coverage",
			source: richFixture(),
			query:  Query{Project: "p1", Since: "2026-07-13T00:00:00Z", Until: "2026-07-14T00:00:00Z"},
			check: func(t *testing.T, got Report) {
				if got.Scope.ProjectKey != "p1" || got.Inventory.Total != 4 || got.Inventory.Ready != 1 || got.Inventory.InProgress != 0 {
					t.Fatalf("inventory = %#v, scope = %#v", got.Inventory, got.Scope)
				}
				if got.Inventory.ByStatus["open"] != 2 || got.Inventory.ByStatus["cancelled"] != 1 || got.Inventory.ByStatus["deferred"] != 1 {
					t.Fatalf("status counts = %#v", got.Inventory.ByStatus)
				}
				if got.Flow.Created != 3 || got.Flow.Closed != 2 || got.Flow.Cancelled != 1 || got.Flow.Reopened != 1 || got.Flow.LeadTime != (Percentiles{SampleSize: 2, P50Seconds: 4200, P75Seconds: 5400, P90Seconds: 5400}) {
					t.Fatalf("flow = %#v", got.Flow)
				}
				if got.Attempts.Claims != 3 || got.Attempts.Completed != 3 || got.Attempts.Outcomes["done"] != 1 || got.Attempts.Outcomes["handoff"] != 1 || got.Attempts.Outcomes["expired"] != 1 {
					t.Fatalf("attempts = %#v", got.Attempts)
				}
				if got.Attempts.Duration != (Percentiles{SampleSize: 3, P50Seconds: 120, P75Seconds: 600, P90Seconds: 600}) || got.Attempts.Churn != (Coverage{Numerator: 1, Denominator: 2, Ratio: 0.5}) {
					t.Fatalf("attempt durations/churn = %#v", got.Attempts)
				}
				if got.Handoff != (Coverage{Numerator: 1, Denominator: 1, Ratio: 1}) {
					t.Fatalf("handoff = %#v", got.Handoff)
				}
				if got.Coverage.Notes != (Coverage{Numerator: 2, Denominator: 4, Ratio: 0.5}) || got.Coverage.SpecLinks != (Coverage{Numerator: 1, Denominator: 4, Ratio: 0.25}) || got.Coverage.SCMCloseMetadata != (Coverage{Numerator: 1, Denominator: 2, Ratio: 0.5}) {
					t.Fatalf("coverage = %#v", got.Coverage)
				}
				if got.DataQuality.ExactOrderingFromSequence != 2 || got.DataQuality.LegacyEventCount != 1 || !got.DataQuality.LegacyEventsIncluded {
					t.Fatalf("data quality = %#v", got.DataQuality)
				}
			},
		},
		{
			name:   "repository scope excludes project-scoped issues",
			source: richFixture(),
			query:  Query{Project: "p1", Repo: "repo-one"},
			check: func(t *testing.T, got Report) {
				if got.Inventory.Total != 2 || got.Scope.RepositoryID != "repo-1" {
					t.Fatalf("repository report = %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Build(context.Background(), tt.source, tt.query, now)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, got)
		})
	}
}

func TestBuildRejectsInvalidAndAmbiguousFilters(t *testing.T) {
	t.Parallel()
	source := richFixture()
	now := mustTime(t, "2026-07-14T12:00:00Z")
	tests := []struct {
		name  string
		query Query
		code  string
	}{
		{name: "unknown project", query: Query{Project: "missing"}, code: core.ErrNotFound},
		{name: "invalid since", query: Query{Since: "tomorrow"}, code: core.ErrValidationFailed},
		{name: "until before since", query: Query{Since: "2026-07-14T00:00:00Z", Until: "2026-07-13T00:00:00Z"}, code: core.ErrValidationFailed},
		{name: "ambiguous repository", query: Query{Repo: "shared"}, code: core.ErrValidationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(context.Background(), source, tt.query, now)
			var apiErr core.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tt.code {
				t.Fatalf("error = %v, want API code %q", err, tt.code)
			}
		})
	}
}

func TestBuildReconcilesSanitizedExportFixture(t *testing.T) {
	t.Parallel()
	source := loadExportFixture(t, "testdata/sanitized-export.jsonl")
	report, err := Build(context.Background(), source, Query{
		Project: "fixture",
		Since:   "2026-07-13T00:00:00Z",
		Until:   "2026-07-14T00:00:00Z",
	}, mustTime(t, "2026-07-14T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Inventory.Total != 1 || report.Flow.Created != 1 || report.Flow.Closed != 1 || report.Attempts.Completed != 1 {
		t.Fatalf("fixture report did not reconcile: %#v", report)
	}
	if report.Coverage.Notes != (Coverage{Numerator: 1, Denominator: 1, Ratio: 1}) || report.Coverage.SpecLinks != (Coverage{Numerator: 1, Denominator: 1, Ratio: 1}) || report.Coverage.SCMCloseMetadata != (Coverage{Numerator: 1, Denominator: 1, Ratio: 1}) {
		t.Fatalf("fixture coverage = %#v", report.Coverage)
	}
}

type fixtureSource struct {
	projects   []core.Project
	repos      []core.Repository
	issues     []core.Issue
	ready      []core.Issue
	references []coordinatorexport.Reference
	notes      []core.Note
	events     []core.Event
}

func (f fixtureSource) ListProjects(context.Context) ([]core.Project, error) { return f.projects, nil }
func (f fixtureSource) ListRepos(context.Context, string) ([]core.Repository, error) {
	return f.repos, nil
}
func (f fixtureSource) ListIssues(context.Context, core.IssueListParams) ([]core.Issue, error) {
	return f.issues, nil
}
func (f fixtureSource) ListReadyIssues(context.Context, string, string, []string) ([]core.Issue, error) {
	return f.ready, nil
}
func (f fixtureSource) ListReferences(context.Context) ([]coordinatorexport.Reference, error) {
	return f.references, nil
}
func (f fixtureSource) ListAllNotes(context.Context) ([]core.Note, error)   { return f.notes, nil }
func (f fixtureSource) ListAllEvents(context.Context) ([]core.Event, error) { return f.events, nil }

func richFixture() fixtureSource {
	return fixtureSource{
		projects: []core.Project{{ID: "project-1", Key: "p1"}, {ID: "project-2", Key: "p2"}},
		repos: []core.Repository{
			{ID: "repo-1", ProjectID: "project-1", LogicalName: "repo-one"},
			{ID: "repo-2", ProjectID: "project-2", LogicalName: "shared"},
			{ID: "repo-3", ProjectID: "project-1", LogicalName: "shared"},
		},
		issues: []core.Issue{
			{ID: "issue-1", ProjectID: "project-1", RepositoryID: "repo-1", Status: "open", CreatedAt: "2026-07-13T09:00:00Z"},
			{ID: "issue-2", ProjectID: "project-1", RepositoryID: "repo-1", Status: "open", CreatedAt: "2026-07-13T09:00:00Z"},
			{ID: "issue-3", ProjectID: "project-1", Status: "cancelled", CreatedAt: "2026-07-13T09:00:00Z"},
			{ID: "issue-5", ProjectID: "project-1", Status: "deferred", CreatedAt: "2026-07-12T09:00:00Z"},
			{ID: "issue-4", ProjectID: "project-2", RepositoryID: "repo-2", Status: "done", CreatedAt: "2026-07-13T09:00:00Z"},
		},
		ready: []core.Issue{{ID: "issue-2", ProjectID: "project-1", RepositoryID: "repo-1", Status: "open"}},
		references: []coordinatorexport.Reference{
			{IssueID: "issue-1", ArtifactPath: "docs/specs/008/tasks.md", ArtifactKind: "tasks", Relation: "implements"},
			{IssueID: "issue-2", ArtifactPath: "README.md", ArtifactKind: "doc", Relation: "related"},
			{IssueID: "issue-4", ArtifactPath: "docs/specs/other.md", ArtifactKind: "spec", Relation: "implements"},
		},
		notes: []core.Note{{ID: "note-1", IssueID: "issue-1"}, {ID: "note-2", IssueID: "issue-2"}, {ID: "note-3", IssueID: "issue-4"}},
		events: []core.Event{
			{Sequence: 1, ID: "event-legacy", IssueID: "issue-1", EventType: "issue_created", PayloadJSON: "{}", CreatedAt: "2026-07-13T09:00:00Z"},
			{Sequence: 2, ID: "event-cutoff", EventType: "event_ordering_enabled", PayloadJSON: `{"legacy_ordering":"deterministic_not_causal"}`, CreatedAt: "2026-07-13T09:01:00Z"},
			{Sequence: 3, ID: "event-claim-1", IssueID: "issue-1", EventType: "issue_claimed", PayloadJSON: `{"attempt_id":"attempt-1"}`, CreatedAt: "2026-07-13T10:00:00Z"},
			{Sequence: 4, ID: "event-close-1", IssueID: "issue-1", EventType: "issue_closed", PayloadJSON: `{"attempt_id":"attempt-1","resolution":"done","branch":"work/p1","pr_url":"https://example.test/p1","commit_sha":"abc"}`, CreatedAt: "2026-07-13T10:10:00Z"},
			{Sequence: 5, ID: "event-claim-2", IssueID: "issue-2", EventType: "issue_claimed", PayloadJSON: `{"attempt_id":"attempt-2"}`, CreatedAt: "2026-07-13T10:00:00Z"},
			{Sequence: 6, ID: "event-release", IssueID: "issue-2", EventType: "issue_released", PayloadJSON: `{"attempt_id":"attempt-2","end_reason":"handoff"}`, CreatedAt: "2026-07-13T10:01:00Z"},
			{Sequence: 7, ID: "event-claim-3", IssueID: "issue-2", EventType: "issue_claimed", PayloadJSON: `{"attempt_id":"attempt-3"}`, CreatedAt: "2026-07-13T10:02:00Z"},
			{Sequence: 8, ID: "event-expired", IssueID: "issue-2", EventType: "lease_expired", PayloadJSON: `{"attempt_id":"attempt-3"}`, CreatedAt: "2026-07-13T10:04:00Z"},
			{Sequence: 9, ID: "event-close-2", IssueID: "issue-3", EventType: "issue_operator_closed", PayloadJSON: `{"resolution":"cancelled"}`, CreatedAt: "2026-07-13T10:30:00Z"},
			{Sequence: 10, ID: "event-p2-close", IssueID: "issue-4", EventType: "issue_closed", PayloadJSON: `{"resolution":"done"}`, CreatedAt: "2026-07-13T10:10:00Z"},
			{Sequence: 11, ID: "event-reopen-1", IssueID: "issue-1", EventType: "issue_reopened", PayloadJSON: `{"from_status":"done","to_status":"open"}`, CreatedAt: "2026-07-13T10:20:00Z"},
		},
	}
}

func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func loadExportFixture(t *testing.T, path string) fixtureSource {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var source fixtureSource
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		switch record.Type {
		case "project":
			var value core.Project
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.projects = append(source.projects, value)
		case "repository":
			var value core.Repository
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.repos = append(source.repos, value)
		case "issue":
			var value core.Issue
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.issues = append(source.issues, value)
		case "reference":
			var value coordinatorexport.Reference
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.references = append(source.references, value)
		case "note":
			var value core.Note
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.notes = append(source.notes, value)
		case "event":
			var value core.Event
			if err := json.Unmarshal(record.Payload, &value); err != nil {
				t.Fatal(err)
			}
			source.events = append(source.events, value)
		default:
			t.Fatalf("unexpected fixture record type %q", record.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return source
}
