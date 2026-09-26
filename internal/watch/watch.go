// Package watch builds a read-only view of coordinator activity from daemon APIs.
package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/abevz/dibs/internal/core"
)

const (
	eventPageSize = 500
	maxEventPages = 20
	maxRecent     = 8
)

// Source contains only read operations, so refresh cannot mutate coordinator state.
type Source interface {
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

type Snapshot struct {
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

	byID := make(map[string]core.Issue, len(issues))
	names := make(map[string]string, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
		names[issue.ID] = issue.ShortID
	}

	snapshot := Snapshot{
		Project:    s.project,
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

func leaseActive(expiresAt string, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339, expiresAt)
	return err == nil && expires.After(now)
}
