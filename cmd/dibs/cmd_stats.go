package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/report"
)

const statsUsage = `Usage: dibs stats [filters]

Filters:
  --project <key>                    Limit to one project
  --repo <repository-id-or-name>      Limit to one repository
  --since <RFC3339|duration>          Start of the flow window, e.g. 24h
  --until <RFC3339>                   End of the flow window (default: now)
`

func runStats(ctx context.Context, c *client.Client, args []string) error {
	query, help, err := parseStatsArgs(args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(os.Stdout, statsUsage)
		return nil
	}

	stats, err := c.GetStats(ctx, query)
	if err != nil {
		fail(err)
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(stats)
	}
	printStats(stats)
	return nil
}

func parseStatsArgs(args []string) (report.Query, bool, error) {
	var query report.Query
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--help" || flag == "-h" {
			return report.Query{}, true, nil
		}
		switch flag {
		case "--project", "--repo", "--since", "--until":
		default:
			return report.Query{}, false, fmt.Errorf("unknown flag: %s", flag)
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return report.Query{}, false, fmt.Errorf("%s requires a value", flag)
		}
		value := args[i+1]
		switch flag {
		case "--project":
			query.Project = value
		case "--repo":
			query.Repo = value
		case "--since":
			query.Since = value
		case "--until":
			query.Until = value
		}
		i++
	}
	return query, false, nil
}

func printStats(stats report.Report) {
	writeStats(os.Stdout, stats)
}

func writeStats(w io.Writer, stats report.Report) {
	window := stats.Window.Until
	if stats.Window.Since != "" {
		window = stats.Window.Since + " to " + stats.Window.Until
	}
	fmt.Fprintf(w, "Execution statistics (%s)\n", stats.Version)
	fmt.Fprintf(w, "Window: %s\n", window)
	if stats.Scope.ProjectKey != "" {
		fmt.Fprintf(w, "Project: %s\n", stats.Scope.ProjectKey)
	}
	if stats.Scope.RepositoryName != "" {
		fmt.Fprintf(w, "Repository: %s (%s)\n", stats.Scope.RepositoryName, stats.Scope.RepositoryID)
	}
	fmt.Fprintf(w, "\nInventory: %d total, %d ready, %d in progress\n", stats.Inventory.Total, stats.Inventory.Ready, stats.Inventory.InProgress)
	statuses := make([]string, 0, len(stats.Inventory.ByStatus))
	for status := range stats.Inventory.ByStatus {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		fmt.Fprintf(w, "  %-12s %d\n", status+":", stats.Inventory.ByStatus[status])
	}

	fmt.Fprintf(w, "\nFlow: %d created, %d closed, %d cancelled, %d reopened\n", stats.Flow.Created, stats.Flow.Closed, stats.Flow.Cancelled, stats.Flow.Reopened)
	writePercentiles(w, "Lead time", stats.Flow.LeadTime)
	writePercentiles(w, "Attempt duration", stats.Attempts.Duration)
	fmt.Fprintf(w, "Attempts: %d claims, %d completed, %d/%d multi-attempt issues (%.1f%%)\n",
		stats.Attempts.Claims,
		stats.Attempts.Completed,
		stats.Attempts.Churn.Numerator,
		stats.Attempts.Churn.Denominator,
		stats.Attempts.Churn.Ratio*100,
	)
	outcomes := make([]string, 0, len(stats.Attempts.Outcomes))
	for outcome := range stats.Attempts.Outcomes {
		outcomes = append(outcomes, outcome)
	}
	sort.Strings(outcomes)
	fmt.Fprint(w, "Outcomes:")
	for _, outcome := range outcomes {
		fmt.Fprintf(w, " %s=%d", outcome, stats.Attempts.Outcomes[outcome])
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Handoff: %d/%d releases (%.1f%%)\n", stats.Handoff.Numerator, stats.Handoff.Denominator, stats.Handoff.Ratio*100)
	fmt.Fprintf(w, "Coverage: notes %d/%d (%.1f%%), spec links %d/%d (%.1f%%), SCM metadata %d/%d (%.1f%%)\n",
		stats.Coverage.Notes.Numerator, stats.Coverage.Notes.Denominator, stats.Coverage.Notes.Ratio*100,
		stats.Coverage.SpecLinks.Numerator, stats.Coverage.SpecLinks.Denominator, stats.Coverage.SpecLinks.Ratio*100,
		stats.Coverage.SCMCloseMetadata.Numerator, stats.Coverage.SCMCloseMetadata.Denominator, stats.Coverage.SCMCloseMetadata.Ratio*100,
	)
	if stats.DataQuality.LegacyEventsIncluded {
		fmt.Fprintf(w, "Data quality: %d legacy events in scope; exact ordering starts at sequence %d\n", stats.DataQuality.LegacyEventCount, stats.DataQuality.ExactOrderingFromSequence)
	}
}

func writePercentiles(w io.Writer, label string, values report.Percentiles) {
	if values.SampleSize == 0 {
		fmt.Fprintf(w, "%s: no samples\n", label)
		return
	}
	fmt.Fprintf(w, "%s: n=%d p50=%.0fs p75=%.0fs p90=%.0fs\n", label, values.SampleSize, values.P50Seconds, values.P75Seconds, values.P90Seconds)
}
