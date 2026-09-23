package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/abevz/dibs/internal/core"
)

func populateDependencies(ctx context.Context, db *sql.DB, issues []core.Issue) ([]core.Issue, error) {
	if len(issues) == 0 {
		return issues, nil
	}
	for idx := range issues {
		issues[idx].Blocked = issues[idx].Status == "blocked"
	}

	ids := make([]string, len(issues))
	idMap := make(map[string]int)
	for i, issue := range issues {
		ids[i] = issue.ID
		idMap[issue.ID] = i
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT source.id, source.short_id, target.id, target.short_id, target.status, d.kind
		FROM dependencies d
		JOIN issues source ON source.id = d.issue_id
		JOIN issues target ON target.id = d.depends_on_issue_id
		WHERE d.issue_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query dependencies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issueID, issueShortID, dependsOnID, dependsOnShortID, dependsOnStatus, kind string
		if err := rows.Scan(&issueID, &issueShortID, &dependsOnID, &dependsOnShortID, &dependsOnStatus, &kind); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		if idx, ok := idMap[issueID]; ok {
			if kind == "blocks" && !isTerminalIssueStatus(dependsOnStatus) {
				issues[idx].Blocked = true
				if !containsShortID(issues[idx].BlockedBy, dependsOnShortID) {
					issues[idx].BlockedBy = append(issues[idx].BlockedBy, dependsOnShortID)
				}
			}
			issues[idx].Dependencies = append(issues[idx].Dependencies, core.Dependency{
				IssueID:          issueID,
				IssueShortID:     issueShortID,
				DependsOnID:      dependsOnID,
				DependsOnShortID: dependsOnShortID,
				Kind:             kind,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dependencies: %w", err)
	}

	// Reverse edges: an issue blocks every non-terminal dependent that depends on
	// it with kind "blocks". This mirrors BlockedBy so the same relationship is
	// visible from both sides. A blocker that is itself terminal no longer blocks
	// anyone, matching the forward rule that only a non-terminal blocker blocks.
	reverseQuery := fmt.Sprintf(`
		SELECT d.depends_on_issue_id, blocked.short_id
		FROM dependencies d
		JOIN issues blocked ON blocked.id = d.issue_id
		WHERE d.depends_on_issue_id IN (%s) AND d.kind = 'blocks'
	`, strings.Join(placeholders, ","))

	reverseRows, err := db.QueryContext(ctx, reverseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query reverse dependencies: %w", err)
	}
	defer reverseRows.Close()

	for reverseRows.Next() {
		var blockerID, blockedShortID string
		if err := reverseRows.Scan(&blockerID, &blockedShortID); err != nil {
			return nil, fmt.Errorf("scan reverse dependency: %w", err)
		}
		idx, ok := idMap[blockerID]
		if !ok || isTerminalIssueStatus(issues[idx].Status) {
			continue
		}
		if !containsShortID(issues[idx].Blocks, blockedShortID) {
			issues[idx].Blocks = append(issues[idx].Blocks, blockedShortID)
		}
	}

	if err := reverseRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reverse dependencies: %w", err)
	}

	return issues, nil
}

func isTerminalIssueStatus(status string) bool {
	return status == "done" || status == "cancelled"
}

func containsShortID(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
