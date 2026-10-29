// Package watch builds a read-only view of coordinator activity from daemon APIs.
package watch

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/report"
)

const (
	eventPageSize = 500
	maxEventPages = 20
	maxRecent     = 8
)

// Source contains only read operations, so refresh cannot mutate coordinator state.
type Source interface {
	GetStats(context.Context, report.Query) (report.Report, error)
	GetIssue(context.Context, string) (core.Issue, *core.IssueLease, error)
	ListNotes(context.Context, string) ([]core.Note, error)
	ListProjects(context.Context) ([]core.Project, error)
	ListIssuesWithFilters(context.Context, core.IssueListParams) ([]core.Issue, error)
	ListReadyIssues(context.Context, string, string, []string) ([]core.Issue, error)
	RecentEvents(context.Context, int) (core.EventPage, error)
	WatchEvents(context.Context, string, int, int) (core.EventPage, error)
}

type BlockedIssue struct {
	Issue     core.Issue `json:"issue"`
	BlockedBy []string   `json:"blocked_by"`
}

type ProjectSummary struct {
	Key string `json:"key"`
	report.ProjectStats
}

type StaleIssue struct {
	Issue  core.Issue `json:"issue"`
	Since  string     `json:"since,omitempty"`
	Reason string     `json:"reason"`
}

type Detail struct {
	Issue core.Issue
	Notes []core.Note
}

type Snapshot struct {
	Projects   []ProjectSummary  `json:"projects"`
	Stale      []StaleIssue      `json:"stale"`
	Issues     []core.Issue      `json:"issues"`
	Project    string            `json:"project,omitempty"`
	Ready      []core.Issue      `json:"ready"`
	Active     []core.Issue      `json:"active"`
	Blocked    []BlockedIssue    `json:"blocked"`
	Events     []core.Event      `json:"events"`
	IssueNames map[string]string `json:"issue_names"`
	UpdatedAt  time.Time         `json:"updated_at"`
	CatchingUp bool              `json:"catching_up_events"`
}

type Service struct {
	source  Source
	project string
	cursor  string
	events  []core.Event
}

func New(source Source, project string) *Service {
	return &Service{source: source, project: project}
}

// Refresh reads a new snapshot and advances the event cursor only after every
// API call succeeds. A failed refresh leaves the last complete view intact.
func (s *Service) Refresh(ctx context.Context, now time.Time) (Snapshot, error) {
	issues, err := s.source.ListIssuesWithFilters(ctx, core.IssueListParams{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("list issues: %w", err)
	}
	ready, err := s.source.ListReadyIssues(ctx, s.project, "", nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list ready issues: %w", err)
	}

	projectID := ""
	if s.project != "" {
		projects, err := s.source.ListProjects(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("list projects: %w", err)
		}
		for _, project := range projects {
			if project.Key == s.project || project.ID == s.project {
				projectID = project.ID
				break
			}
		}
		if projectID == "" {
			return Snapshot{}, fmt.Errorf("project not found: %s", s.project)
		}
	}

	var summaries []ProjectSummary
	if projectID == "" {
		stats, err := s.source.GetStats(ctx, report.Query{})
		if err != nil {
			return Snapshot{}, fmt.Errorf("project summary: %w", err)
		}
		for key, row := range stats.ByProject {
			summaries = append(summaries, ProjectSummary{Key: key, ProjectStats: row})
		}
		sort.Slice(summaries, func(i, j int) bool {
			a, b := summaries[i], summaries[j]
			aDormant, bDormant := a.Open+a.Blocked+a.InProgress == 0, b.Open+b.Blocked+b.InProgress == 0
			if aDormant != bDormant {
				return !aDormant
			}
			return a.Key < b.Key
		})
	}
	byID := make(map[string]core.Issue, len(issues))
	names := make(map[string]string, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
		names[issue.ID] = issue.ShortID
	}

	snapshot := Snapshot{
		Project:    s.project,
		Projects:   summaries,
		Stale:      []StaleIssue{},
		Issues:     []core.Issue{},
		Ready:      make([]core.Issue, 0, len(ready)),
		Active:     []core.Issue{},
		Blocked:    []BlockedIssue{},
		Events:     []core.Event{},
		IssueNames: names,
		UpdatedAt:  now,
	}
	for _, issue := range ready {
		if projectID == "" || issue.ProjectID == projectID {
			snapshot.Ready = append(snapshot.Ready, issue)
		}
	}
	for _, issue := range issues {
		if projectID != "" && issue.ProjectID != projectID {
			continue
		}
		if issue.Status == "done" || issue.Status == "cancelled" || issue.Status == "deferred" {
			continue
		}
		snapshot.Issues = append(snapshot.Issues, issue)
		if stale, ok := staleIssue(issue, now); ok {
			snapshot.Stale = append(snapshot.Stale, stale)
		}
		if issue.Holder != "" && leaseActive(issue.LeaseExpiresAt, now) {
			snapshot.Active = append(snapshot.Active, issue)
		}
		var blockers []string
		for _, dependency := range issue.Dependencies {
			if dependency.Kind != "blocks" {
				continue
			}
			blocker, ok := byID[dependency.DependsOnID]
			if !ok || (blocker.Status != "done" && blocker.Status != "cancelled") {
				blockers = append(blockers, dependency.DependsOnShortID)
			}
		}
		if len(blockers) > 0 {
			snapshot.Blocked = append(snapshot.Blocked, BlockedIssue{Issue: issue, BlockedBy: blockers})
		}
	}

	cursor := s.cursor
	events := append([]core.Event(nil), s.events...)
	for pageNumber := 0; pageNumber < maxEventPages; pageNumber++ {
		var page core.EventPage
		if cursor == "" {
			page, err = s.source.RecentEvents(ctx, eventPageSize)
		} else {
			page, err = s.source.WatchEvents(ctx, cursor, eventPageSize, 0)
		}
		if err != nil {
			return Snapshot{}, fmt.Errorf("list recent events: %w", err)
		}
		for _, event := range page.Events {
			if projectID != "" {
				issue, ok := byID[event.IssueID]
				if !ok || issue.ProjectID != projectID {
					continue
				}
			}
			events = append(events, event)
			if len(events) > maxRecent {
				events = events[len(events)-maxRecent:]
			}
		}
		if page.NextSince != "" {
			cursor = page.NextSince
		}
		if len(page.Events) < eventPageSize || s.cursor == "" {
			break
		}
		if pageNumber == maxEventPages-1 {
			snapshot.CatchingUp = true
		}
	}
	s.cursor = cursor
	s.events = events
	snapshot.Events = append(snapshot.Events, events...)
	return snapshot, nil
}

// Cursor returns the event cursor reached by the last complete refresh.
func (s *Service) Cursor() string { return s.cursor }

// WaitForEvents long-polls the daemon for events after cursor and reports
// whether any arrived. It reads one indexed event page instead of a full
// snapshot, so an idle board does not re-read the whole coordinator. An empty
// cursor means no snapshot has been taken yet and always reports a change.
func (s *Service) WaitForEvents(ctx context.Context, cursor string, wait time.Duration) (bool, error) {
	if cursor == "" {
		return true, nil
	}
	page, err := s.source.WatchEvents(ctx, cursor, 1, int(wait/time.Millisecond))
	if err != nil {
		return false, fmt.Errorf("wait for events: %w", err)
	}
	return len(page.Events) > 0, nil
}

// NextLeaseExpiry returns the earliest active lease expiry after now. Lease
// expiry emits no event but can make an issue ready again.
func (snapshot Snapshot) NextLeaseExpiry(now time.Time) (time.Time, bool) {
	var next time.Time
	for _, issue := range snapshot.Active {
		expires, err := time.Parse(time.RFC3339, issue.LeaseExpiresAt)
		if err != nil || !expires.After(now) {
			continue
		}
		if next.IsZero() || expires.Before(next) {
			next = expires
		}
	}
	return next, !next.IsZero()
}

func leaseActive(expiresAt string, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339, expiresAt)
	return err == nil && expires.After(now)
}

func staleIssue(issue core.Issue, now time.Time) (StaleIssue, bool) {
	if issue.Status == "done" || issue.Status == "cancelled" || issue.Status == "deferred" {
		return StaleIssue{}, false
	}
	if issue.Holder != "" && leaseActive(issue.LeaseExpiresAt, now) {
		return StaleIssue{}, false
	}
	if expires, err := time.Parse(time.RFC3339, issue.LeaseExpiresAt); err == nil && !expires.After(now) {
		return StaleIssue{Issue: issue, Since: issue.LeaseExpiresAt, Reason: "since expiry"}, true
	}
	if issue.Status == "in_progress" {
		return StaleIssue{Issue: issue, Since: issue.UpdatedAt, Reason: "since update (no active lease)"}, true
	}
	return StaleIssue{}, false
}

// At refreshes TTL classifications on clock ticks without coordinator calls.
func (snapshot Snapshot) At(now time.Time) Snapshot {
	snapshot.Active = []core.Issue{}
	snapshot.Stale = []StaleIssue{}
	for _, issue := range snapshot.Issues {
		if issue.Holder != "" && leaseActive(issue.LeaseExpiresAt, now) {
			snapshot.Active = append(snapshot.Active, issue)
		}
		if stale, ok := staleIssue(issue, now); ok {
			snapshot.Stale = append(snapshot.Stale, stale)
		}
	}
	return snapshot
}

func (s *Service) Detail(ctx context.Context, id string) (Detail, error) {
	issue, _, err := s.source.GetIssue(ctx, id)
	if err != nil {
		return Detail{}, fmt.Errorf("get issue: %w", err)
	}
	notes, err := s.source.ListNotes(ctx, id)
	if err != nil {
		return Detail{}, fmt.Errorf("list notes: %w", err)
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].CreatedAt < notes[j].CreatedAt })
	if len(notes) > 5 {
		notes = notes[len(notes)-5:]
	}
	return Detail{Issue: issue, Notes: notes}, nil
}

func (s *Service) Scoped(project string) *Service { return New(s.source, project) }
