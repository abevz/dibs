// Package report derives read-only execution-flow statistics from coordinator
// records. It deliberately owns no database connection or mutable state.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/abevz/dibs/internal/core"
	coordinatorexport "github.com/abevz/dibs/internal/export"
)

const Version = "v1"

// Query describes the public GET /v1/stats filters. Since accepts RFC 3339 or
// a positive Go duration such as 24h; Until accepts RFC 3339.
type Query struct {
	Project string
	Repo    string
	Since   string
	Until   string
}

// Source is the narrow, read-only coordinator contract required for reports.
// It is intentionally independent of SQLite so aggregation can be tested from
// sanitized fixtures.
type Source interface {
	ListProjects(context.Context) ([]core.Project, error)
	ListRepos(context.Context, string) ([]core.Repository, error)
	ListIssues(context.Context, core.IssueListParams) ([]core.Issue, error)
	ListReadyIssues(context.Context, string, string, []string) ([]core.Issue, error)
	ListReferences(context.Context) ([]coordinatorexport.Reference, error)
	ListAllNotes(context.Context) ([]core.Note, error)
	ListAllEvents(context.Context) ([]core.Event, error)
}

// Report is the stable, versioned execution-statistics response.
type Report struct {
	Version     string      `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	Scope       Scope       `json:"scope"`
	Window      Window      `json:"window"`
	DataQuality DataQuality `json:"data_quality"`
	Inventory   Inventory   `json:"inventory"`
	Flow        Flow        `json:"flow"`
	Attempts    Attempts    `json:"attempts"`
	Handoff     Coverage    `json:"handoff"`
	Coverage    CoverageSet `json:"coverage"`
	Definitions Definitions `json:"definitions"`
}

type Scope struct {
	ProjectKey     string `json:"project_key,omitempty"`
	RepositoryID   string `json:"repository_id,omitempty"`
	RepositoryName string `json:"repository_name,omitempty"`
}

type Window struct {
	Since string `json:"since,omitempty"`
	Until string `json:"until"`
}

type DataQuality struct {
	ExactOrderingFromSequence int64 `json:"exact_ordering_from_sequence"`
	LegacyEventCount          int   `json:"legacy_event_count"`
	LegacyEventsIncluded      bool  `json:"legacy_events_included"`
	MalformedPayloadCount     int   `json:"malformed_payload_count"`
}

type Inventory struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"by_status"`
	Ready      int            `json:"ready"`
	InProgress int            `json:"in_progress"`
}

type Flow struct {
	Created   int               `json:"created"`
	Closed    int               `json:"closed"`
	Cancelled int               `json:"cancelled"`
	Reopened  int               `json:"reopened"`
	Daily     []DailyThroughput `json:"daily"`
	LeadTime  Percentiles       `json:"lead_time"`
}

type DailyThroughput struct {
	Date      string `json:"date"`
	Created   int    `json:"created"`
	Closed    int    `json:"closed"`
	Cancelled int    `json:"cancelled"`
}

// Percentiles holds nearest-rank percentile values in seconds. SampleSize is
// always present so zero-valued percentiles remain unambiguous for empty data.
type Percentiles struct {
	SampleSize int     `json:"sample_size"`
	P50Seconds float64 `json:"p50_seconds"`
	P75Seconds float64 `json:"p75_seconds"`
	P90Seconds float64 `json:"p90_seconds"`
}

type Attempts struct {
	Claims             int            `json:"claims"`
	Completed          int            `json:"completed"`
	Outcomes           map[string]int `json:"outcomes"`
	Duration           Percentiles    `json:"duration"`
	IssuesWithMultiple int            `json:"issues_with_multiple_attempts"`
	IssuesWithAttempts int            `json:"issues_with_attempts"`
	Churn              Coverage       `json:"churn"`
}

// Coverage expresses a numerator, denominator, and ratio. Ratio is zero when
// Denominator is zero; consumers should use the denominator to distinguish no
// data from zero coverage.
type Coverage struct {
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Ratio       float64 `json:"ratio"`
}

type CoverageSet struct {
	Notes            Coverage `json:"notes"`
	SpecLinks        Coverage `json:"spec_links"`
	SCMCloseMetadata Coverage `json:"scm_close_metadata"`
}

type Definitions struct {
	DurationUnit      string `json:"duration_unit"`
	PercentileMethod  string `json:"percentile_method"`
	WindowTreatment   string `json:"window_treatment"`
	CoverageTreatment string `json:"coverage_treatment"`
	LegacyOrdering    string `json:"legacy_ordering"`
}

type parsedQuery struct {
	project *core.Project
	repo    *core.Repository
	since   time.Time
	until   time.Time
}

type attempt struct {
	started time.Time
}

type terminalClose struct {
	at      time.Time
	payload map[string]any
}

// Build aggregates one report against the supplied point-in-time source.
func Build(ctx context.Context, source Source, query Query, now time.Time) (Report, error) {
	projects, err := source.ListProjects(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list projects: %w", err)
	}
	repositories, err := source.ListRepos(ctx, "")
	if err != nil {
		return Report{}, fmt.Errorf("list repositories: %w", err)
	}
	filters, err := parseQuery(query, projects, repositories, now)
	if err != nil {
		return Report{}, err
	}

	issues, err := source.ListIssues(ctx, core.IssueListParams{})
	if err != nil {
		return Report{}, fmt.Errorf("list issues: %w", err)
	}
	issues = filterIssues(issues, filters)
	issueIDs := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		issueIDs[issue.ID] = struct{}{}
	}

	readyProjectID, readyRepoID := "", ""
	if filters.project != nil {
		readyProjectID = filters.project.ID
	}
	if filters.repo != nil {
		readyRepoID = filters.repo.ID
	}
	readyIssues, err := source.ListReadyIssues(ctx, readyProjectID, readyRepoID, nil)
	if err != nil {
		return Report{}, fmt.Errorf("list ready issues: %w", err)
	}
	readyIssues = filterIssues(readyIssues, filters)

	references, err := source.ListReferences(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list references: %w", err)
	}
	notes, err := source.ListAllNotes(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list notes: %w", err)
	}
	events, err := source.ListAllEvents(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list events: %w", err)
	}

	report := Report{
		Version:     Version,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Scope:       makeScope(filters),
		Window: Window{
			Since: formatOptionalTime(filters.since),
			Until: filters.until.UTC().Format(time.RFC3339),
		},
		Inventory: Inventory{ByStatus: emptyStatusCounts()},
		Attempts:  Attempts{Outcomes: emptyOutcomeCounts()},
		Definitions: Definitions{
			DurationUnit:      "seconds",
			PercentileMethod:  "nearest_rank",
			WindowTreatment:   "flow events and issue creation are included when their timestamp is within [since, until]; omitted since includes all retained history",
			CoverageTreatment: "notes and spec links are current-scope snapshots; SCM metadata covers latest terminal closes in the selected window",
			LegacyOrdering:    "events before exact_ordering_from_sequence are deterministic but not causally ordered",
		},
	}

	for _, issue := range issues {
		report.Inventory.Total++
		report.Inventory.ByStatus[issue.Status]++
		createdAt, err := parseTimestamp(issue.CreatedAt)
		if err != nil {
			return Report{}, fmt.Errorf("parse issue %s created_at: %w", issue.ID, err)
		}
		if inWindow(createdAt, filters.since, filters.until) {
			report.Flow.Created++
			addDaily(&report.Flow.Daily, createdAt, func(bucket *DailyThroughput) { bucket.Created++ })
		}
	}
	report.Inventory.Ready = len(readyIssues)
	report.Inventory.InProgress = report.Inventory.ByStatus["in_progress"]

	notesByIssue := make(map[string]struct{})
	for _, note := range notes {
		if _, ok := issueIDs[note.IssueID]; ok {
			notesByIssue[note.IssueID] = struct{}{}
		}
	}
	report.Coverage.Notes = coverage(len(notesByIssue), len(issues))

	specLinksByIssue := make(map[string]struct{})
	for _, reference := range references {
		if _, ok := issueIDs[reference.IssueID]; ok && isSpecReference(reference) {
			specLinksByIssue[reference.IssueID] = struct{}{}
		}
	}
	report.Coverage.SpecLinks = coverage(len(specLinksByIssue), len(issues))

	_, scopedEvents, windowEvents, cutoff, malformed, err := classifyEvents(events, issueIDs, filters)
	if err != nil {
		return Report{}, err
	}
	report.DataQuality.ExactOrderingFromSequence = cutoff
	report.DataQuality.LegacyEventCount = legacyEventCount(scopedEvents, cutoff)
	report.DataQuality.LegacyEventsIncluded = legacyEventsIncluded(windowEvents, cutoff)
	report.DataQuality.MalformedPayloadCount = malformed

	applyFlowAndAttemptMetrics(&report, issues, scopedEvents, filters)
	report.Coverage.SCMCloseMetadata = scmMetadataCoverage(issues, scopedEvents, filters)
	sort.Slice(report.Flow.Daily, func(i, j int) bool { return report.Flow.Daily[i].Date < report.Flow.Daily[j].Date })
	return report, nil
}

func parseQuery(query Query, projects []core.Project, repositories []core.Repository, now time.Time) (parsedQuery, error) {
	filters := parsedQuery{until: now.UTC()}
	if query.Since != "" {
		since, err := parseSince(query.Since, now)
		if err != nil {
			return parsedQuery{}, core.NewAPIError(core.ErrValidationFailed, "since must be RFC 3339 or a positive duration")
		}
		filters.since = since
	}
	if query.Until != "" {
		until, err := parseTimestamp(query.Until)
		if err != nil {
			return parsedQuery{}, core.NewAPIError(core.ErrValidationFailed, "until must be RFC 3339")
		}
		filters.until = until
	}
	if !filters.since.IsZero() && filters.until.Before(filters.since) {
		return parsedQuery{}, core.NewAPIError(core.ErrValidationFailed, "until must not be before since")
	}

	if query.Project != "" {
		for i := range projects {
			if projects[i].Key == query.Project {
				filters.project = &projects[i]
				break
			}
		}
		if filters.project == nil {
			return parsedQuery{}, core.NewAPIError(core.ErrNotFound, "project not found: "+query.Project)
		}
	}
	if query.Repo != "" {
		matches := make([]*core.Repository, 0, 1)
		for i := range repositories {
			repo := &repositories[i]
			if filters.project != nil && repo.ProjectID != filters.project.ID {
				continue
			}
			if repo.ID == query.Repo || repo.LogicalName == query.Repo {
				matches = append(matches, repo)
			}
		}
		switch len(matches) {
		case 0:
			return parsedQuery{}, core.NewAPIError(core.ErrNotFound, "repository not found: "+query.Repo)
		case 1:
			filters.repo = matches[0]
		default:
			return parsedQuery{}, core.NewAPIError(core.ErrValidationFailed,
				"repository name is ambiguous; add a project filter or use the repository UUID: "+query.Repo)
		}
	}
	return filters, nil
}

func parseSince(raw string, now time.Time) (time.Time, error) {
	if timestamp, err := parseTimestamp(raw); err == nil {
		return timestamp, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return time.Time{}, fmt.Errorf("invalid since")
	}
	return now.UTC().Add(-duration), nil
}

func parseTimestamp(raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return value.UTC(), nil
}

func filterIssues(issues []core.Issue, filters parsedQuery) []core.Issue {
	filtered := make([]core.Issue, 0, len(issues))
	for _, issue := range issues {
		if filters.project != nil && issue.ProjectID != filters.project.ID {
			continue
		}
		if filters.repo != nil && issue.RepositoryID != filters.repo.ID {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered
}

func makeScope(filters parsedQuery) Scope {
	scope := Scope{}
	if filters.project != nil {
		scope.ProjectKey = filters.project.Key
	}
	if filters.repo != nil {
		scope.RepositoryID = filters.repo.ID
		scope.RepositoryName = filters.repo.LogicalName
	}
	return scope
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func emptyStatusCounts() map[string]int {
	return map[string]int{
		"open":        0,
		"in_progress": 0,
		"blocked":     0,
		"deferred":    0,
		"done":        0,
		"cancelled":   0,
	}
}

func emptyOutcomeCounts() map[string]int {
	return map[string]int{
		"done":      0,
		"cancelled": 0,
		"released":  0,
		"handoff":   0,
		"expired":   0,
	}
}

func inWindow(value, since, until time.Time) bool {
	if !since.IsZero() && value.Before(since) {
		return false
	}
	return !value.After(until)
}

func addDaily(days *[]DailyThroughput, at time.Time, increment func(*DailyThroughput)) {
	date := at.UTC().Format("2006-01-02")
	for i := range *days {
		if (*days)[i].Date == date {
			increment(&(*days)[i])
			return
		}
	}
	bucket := DailyThroughput{Date: date}
	increment(&bucket)
	*days = append(*days, bucket)
}

func isSpecReference(reference coordinatorexport.Reference) bool {
	if reference.Relation != "implements" {
		return false
	}
	if strings.HasPrefix(reference.ArtifactPath, "docs/specs/") {
		return true
	}
	switch reference.ArtifactKind {
	case "requirements", "design", "tasks", "review", "spec", "sdd":
		return true
	default:
		return false
	}
}

func classifyEvents(events []core.Event, issueIDs map[string]struct{}, filters parsedQuery) ([]core.Event, []core.Event, []core.Event, int64, int, error) {
	all := append([]core.Event(nil), events...)
	var cutoff int64
	for _, event := range all {
		if event.EventType == "event_ordering_enabled" {
			cutoff = event.Sequence
			break
		}
	}

	scoped := make([]core.Event, 0, len(all))
	window := make([]core.Event, 0, len(all))
	malformed := 0
	for _, event := range all {
		if _, ok := issueIDs[event.IssueID]; !ok {
			continue
		}
		at, err := parseTimestamp(event.CreatedAt)
		if err != nil {
			return nil, nil, nil, 0, 0, fmt.Errorf("parse event %s created_at: %w", event.ID, err)
		}
		scoped = append(scoped, event)
		if inWindow(at, filters.since, filters.until) {
			if !payloadValid(event.PayloadJSON) {
				malformed++
			}
			window = append(window, event)
		}
	}
	return all, scoped, window, cutoff, malformed, nil
}

func legacyEventCount(events []core.Event, cutoff int64) int {
	if cutoff == 0 {
		return 0
	}
	count := 0
	for _, event := range events {
		if event.Sequence < cutoff {
			count++
		}
	}
	return count
}

func legacyEventsIncluded(events []core.Event, cutoff int64) bool {
	if cutoff == 0 {
		return false
	}
	for _, event := range events {
		if event.Sequence < cutoff {
			return true
		}
	}
	return false
}

func applyFlowAndAttemptMetrics(report *Report, issues []core.Issue, events []core.Event, filters parsedQuery) {
	issueByID := make(map[string]core.Issue, len(issues))
	for _, issue := range issues {
		issueByID[issue.ID] = issue
	}

	attempts := make(map[string]attempt)
	attemptsByIssue := make(map[string]map[string]struct{})
	latestTerminal := make(map[string]terminalClose)
	var leadTimes []float64
	var attemptDurations []float64
	handoffDenominator, handoffNumerator := 0, 0

	for _, event := range events {
		at, _ := parseTimestamp(event.CreatedAt)
		withinWindow := inWindow(at, filters.since, filters.until)
		payload, valid := parsePayload(event.PayloadJSON)
		switch event.EventType {
		case "issue_claimed":
			attemptID := payloadString(payload, "attempt_id")
			if !valid || attemptID == "" {
				continue
			}
			if withinWindow {
				report.Attempts.Claims++
			}
			attempts[attemptID] = attempt{started: at}
			if withinWindow {
				if attemptsByIssue[event.IssueID] == nil {
					attemptsByIssue[event.IssueID] = make(map[string]struct{})
				}
				attemptsByIssue[event.IssueID][attemptID] = struct{}{}
			}
		case "issue_closed", "issue_operator_closed":
			if !withinWindow {
				continue
			}
			report.Flow.Closed++
			if payloadString(payload, "resolution") == "cancelled" {
				report.Flow.Cancelled++
			}
			addDaily(&report.Flow.Daily, at, func(bucket *DailyThroughput) {
				bucket.Closed++
				if payloadString(payload, "resolution") == "cancelled" {
					bucket.Cancelled++
				}
			})
			latestTerminal[event.IssueID] = terminalClose{at: at, payload: payload}
			outcome := payloadString(payload, "resolution")
			if outcome == "" {
				outcome = "done"
			}
			completeAttempt(report, attempts, payload, at, outcome, &attemptDurations)
		case "issue_released":
			if !withinWindow {
				continue
			}
			endReason := payloadString(payload, "end_reason")
			if endReason == "" {
				endReason = "released"
			}
			if endReason == "handoff" {
				handoffNumerator++
			}
			handoffDenominator++
			completeAttempt(report, attempts, payload, at, endReason, &attemptDurations)
		case "lease_expired":
			if !withinWindow {
				continue
			}
			completeAttempt(report, attempts, payload, at, "expired", &attemptDurations)
		case "issue_reopened":
			if withinWindow {
				report.Flow.Reopened++
			}
		}
	}

	for issueID, latest := range latestTerminal {
		issue, ok := issueByID[issueID]
		if !ok || !inWindow(latest.at, filters.since, filters.until) {
			continue
		}
		createdAt, err := parseTimestamp(issue.CreatedAt)
		if err != nil || latest.at.Before(createdAt) {
			continue
		}
		leadTimes = append(leadTimes, latest.at.Sub(createdAt).Seconds())
	}
	for _, attemptIDs := range attemptsByIssue {
		report.Attempts.IssuesWithAttempts++
		if len(attemptIDs) > 1 {
			report.Attempts.IssuesWithMultiple++
		}
	}
	report.Attempts.Churn = coverage(report.Attempts.IssuesWithMultiple, report.Attempts.IssuesWithAttempts)
	report.Attempts.Duration = percentiles(attemptDurations)
	report.Flow.LeadTime = percentiles(leadTimes)
	report.Handoff = coverage(handoffNumerator, handoffDenominator)
}

func completeAttempt(report *Report, attempts map[string]attempt, payload map[string]any, endedAt time.Time, outcome string, durations *[]float64) {
	attemptID := payloadString(payload, "attempt_id")
	if attemptID == "" {
		return
	}
	entry, ok := attempts[attemptID]
	if !ok || endedAt.Before(entry.started) {
		return
	}
	report.Attempts.Completed++
	if _, ok := report.Attempts.Outcomes[outcome]; !ok {
		report.Attempts.Outcomes[outcome] = 0
	}
	report.Attempts.Outcomes[outcome]++
	*durations = append(*durations, endedAt.Sub(entry.started).Seconds())
}

func scmMetadataCoverage(issues []core.Issue, events []core.Event, filters parsedQuery) Coverage {
	issueIDs := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		issueIDs[issue.ID] = struct{}{}
	}
	latest := make(map[string]terminalClose)
	for _, event := range events {
		if _, ok := issueIDs[event.IssueID]; !ok || (event.EventType != "issue_closed" && event.EventType != "issue_operator_closed") {
			continue
		}
		at, err := parseTimestamp(event.CreatedAt)
		if err != nil || !inWindow(at, filters.since, filters.until) {
			continue
		}
		payload, _ := parsePayload(event.PayloadJSON)
		if previous, ok := latest[event.IssueID]; !ok || at.After(previous.at) {
			latest[event.IssueID] = terminalClose{at: at, payload: payload}
		}
	}
	complete := 0
	for _, close := range latest {
		if payloadString(close.payload, "branch") != "" && payloadString(close.payload, "pr_url") != "" && payloadString(close.payload, "commit_sha") != "" {
			complete++
		}
	}
	return coverage(complete, len(latest))
}

func payloadValid(raw string) bool {
	_, valid := parsePayload(raw)
	return valid
}

func parsePayload(raw string) (map[string]any, bool) {
	if raw == "" {
		return map[string]any{}, true
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return map[string]any{}, false
	}
	return payload, true
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

func coverage(numerator, denominator int) Coverage {
	result := Coverage{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		result.Ratio = float64(numerator) / float64(denominator)
	}
	return result
}

func percentiles(values []float64) Percentiles {
	result := Percentiles{SampleSize: len(values)}
	if len(values) == 0 {
		return result
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	result.P50Seconds = nearestRank(sorted, 0.50)
	result.P75Seconds = nearestRank(sorted, 0.75)
	result.P90Seconds = nearestRank(sorted, 0.90)
	return result
}

func nearestRank(values []float64, percentile float64) float64 {
	index := int(math.Ceil(percentile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}
