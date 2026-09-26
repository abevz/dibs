package watch

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// Render draws one terminal-sized, read-only dashboard frame. A non-nil
// refresh error keeps the previous snapshot visible and labels it stale.
func Render(snapshot Snapshot, refreshErr error, now time.Time, width, height int) string {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	project := "all projects"
	if snapshot.Project != "" {
		project = snapshot.Project
	}
	lines := []string{clip("dibs watch · "+project, width)}
	status := "CONNECTING · waiting for daemon API"
	if !snapshot.UpdatedAt.IsZero() {
		status = "LIVE · updated " + snapshot.UpdatedAt.Local().Format("15:04:05") + " · refresh every 2s"
	}
	if refreshErr != nil {
		status = "ERROR · data is stale · " + refreshErr.Error()
		var transportErr *url.Error
		if errors.As(refreshErr, &transportErr) {
			status = "DISCONNECTED · data is stale · " + refreshErr.Error()
		}
	}
	lines = append(lines, clip(status, width))
	if width < 38 || height < 12 {
		lines = append(lines, "", clip("Enlarge terminal to show the board.", width))
		return strings.Join(lines, "\n")
	}

	sectionRows := (height - 4) / 4
	if sectionRows < 2 {
		sectionRows = 2
	}
	lines = append(lines, "")
	lines = appendSection(lines, fmt.Sprintf("READY (%d)", len(snapshot.Ready)), sectionRows, width, func(i int) string {
		issue := snapshot.Ready[i]
		return fmt.Sprintf("%-12s %s", issue.ShortID, issue.Title)
	}, len(snapshot.Ready))
	lines = appendSection(lines, fmt.Sprintf("ACTIVE LEASES (%d)", len(snapshot.Active)), sectionRows, width, func(i int) string {
		issue := snapshot.Active[i]
		process := "PID ?"
		if issue.LeasePID > 0 && issue.LeaseHost != "" {
			process = fmt.Sprintf("%d@%s", issue.LeasePID, issue.LeaseHost)
		}
		if width < 80 {
			return fmt.Sprintf("%s  %s  %s  %s  %s", issue.ShortID, remaining(issue.LeaseExpiresAt, now), process, issue.Holder, issue.Title)
		}
		return fmt.Sprintf("%-12s %-18s %8s  %-20s %s", issue.ShortID, issue.Holder, remaining(issue.LeaseExpiresAt, now), process, issue.Title)
	}, len(snapshot.Active))
	lines = appendSection(lines, fmt.Sprintf("BLOCKED (%d)", len(snapshot.Blocked)), sectionRows, width, func(i int) string {
		issue := snapshot.Blocked[i]
		return fmt.Sprintf("%-12s by %-18s %s", issue.Issue.ShortID, strings.Join(issue.BlockedBy, ","), issue.Issue.Title)
	}, len(snapshot.Blocked))
	eventHeader := fmt.Sprintf("RECENT EVENTS (%d)", len(snapshot.Events))
	if snapshot.CatchingUp {
		eventHeader = "RECENT EVENTS (loading history)"
	}
	lines = appendSection(lines, eventHeader, sectionRows, width, func(i int) string {
		event := snapshot.Events[len(snapshot.Events)-1-i]
		when := event.CreatedAt
		if parsed, err := time.Parse(time.RFC3339, event.CreatedAt); err == nil {
			when = parsed.Local().Format("15:04:05")
		}
		issue := snapshot.IssueNames[event.IssueID]
		if issue == "" {
			issue = "·"
		}
		return fmt.Sprintf("%-8s %-12s %-22s %s", when, issue, event.EventType, event.Actor)
	}, len(snapshot.Events))
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, clip("q quit  ·  r refresh  ·  PID self-reported  ·  no writes or claims", width))
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func appendSection(lines []string, heading string, rows, width int, entry func(int) string, count int) []string {
	lines = append(lines, clip(heading, width))
	if count == 0 {
		return append(lines, "  ·")
	}
	for i := 0; i < count && i < rows-1; i++ {
		lines = append(lines, clip("  "+entry(i), width))
	}
	return lines
}

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(value, width, "…")
}

func remaining(expiresAt string, now time.Time) string {
	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "unknown"
	}
	left := expires.Sub(now)
	if left <= 0 {
		return "expired"
	}
	return left.Round(time.Second).String()
}
