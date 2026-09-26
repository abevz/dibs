package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abevz/dibs/internal/build"
	"github.com/abevz/dibs/internal/client"
	"github.com/abevz/dibs/internal/compat"
	"github.com/abevz/dibs/internal/config"
	"github.com/abevz/dibs/internal/core"
	"github.com/abevz/dibs/internal/firstuse"
	"github.com/abevz/dibs/internal/github"
)

var jsonOutput bool
var defaultActor string

func init() {
	defaultActor = config.EnvOrDefault("DIBS_ACTOR", "AF_COORDINATOR_ACTOR", "")
}

func main() {
	compat.WarnLegacyBinary("afctl", "dibs")
	cfg := config.Default()

	// Parse global flags (--json, --actor) from os.Args before command dispatch.
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" {
			jsonOutput = true
		}
	}
	var filtered []string
	for i := 0; i < len(args); i++ {
		// Everything after issue run's separator belongs to the child process.
		if args[i] == "--" {
			filtered = append(filtered, args[i:]...)
			break
		}
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--actor":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "--") {
				fail(argumentError("--actor requires a value"))
			}
			defaultActor = args[i+1]
			i++
		default:
			filtered = append(filtered, args[i])
		}
	}

	if len(filtered) < 1 {
		printUsage(os.Stdout)
		return
	}
	if isHelpArg(filtered[0]) {
		printUsage(os.Stdout)
		return
	}
	if filtered[0] == "--version" {
		if len(filtered) != 1 {
			fail(argumentError("--version takes no arguments"))
		}
		printVersion()
		return
	}
	if printGroupHelp(filtered) {
		return
	}
	if printLocalHelp(filtered) {
		return
	}
	if err := validateCommandArgs(filtered); err != nil {
		if filtered[0] == "projects" {
			fail(argumentError(err.Error() + "\nDid you mean: " + helpPath(filtered) + "?"))
		}
		if help, ok := leafHelp(filtered); ok {
			fail(argumentError(err.Error() + "\nUse: " + helpPath(filtered) + " for flags.\n" + help))
		}
		fail(argumentError(err.Error() + "\nUse: " + helpPath(filtered) + " for available commands and flags."))
	}

	c := client.New(cfg.SocketPath)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if commandNeedsDaemon(filtered) {
		if err := firstuse.EnsureDaemon(ctx, cfg, firstuse.FindDaemon()); err != nil {
			fail(err)
		}
	}

	if shouldCheckDaemonRevision(filtered) {
		if h, err := c.Health(ctx); err == nil {
			if h.Revision != "" && h.Revision != "unknown" && build.Revision != "unknown" && h.Revision != build.Revision {
				fmt.Fprintf(os.Stderr, "dibs revision %s != daemon revision %s; restart dibsd\n", shortRev(build.Revision), shortRev(h.Revision))
			}
		}
	}

	var err error
	switch filtered[0] {
	case "health":
		err = runHealth(ctx, c)
	case "doctor":
		err = runDoctor(ctx, c, cfg, filtered[1:])
	case "protocol":
		runProtocol()
	case "init":
		err = runInitSetup(ctx, c, cfg, filtered[1:])
	case "daemon":
		if filtered[1] == "start" {
			err = firstuse.EnsureDaemon(ctx, cfg, firstuse.FindDaemon())
		} else {
			err = firstuse.StopDaemon(ctx, cfg)
		}
	case "project":
		err = runProject(ctx, c, filtered[1:])
	case "repo":
		err = runRepo(ctx, c, filtered[1:])
	case "worktree":
		err = runWorktree(ctx, c, filtered[1:])
	case "artifact-root":
		err = runArtifactRoot(ctx, c, filtered[1:])
	case "artifact":
		err = runArtifact(ctx, c, filtered[1:])
	case "export":
		err = runExport(ctx, c, filtered[1:])
	case "stats":
		err = runStats(ctx, c, filtered[1:])
	case "watch":
		err = runWatch(ctx, c, filtered[1:])
	case "hooks":
		err = runHooks(ctx, c, filtered[1:])
	case "issue":
		err = runIssue(ctx, c, filtered[1:])
	case "dependency":
		err = runDependency(ctx, c, filtered[1:])
	case "ls":
		err = runLs(ctx, c, filtered[1:])
	case "show":
		err = runShow(ctx, c, filtered[1:])
	case "version":
		printVersion()
	default:
		printUsage(os.Stderr)
		os.Exit(1)
	}
	if err != nil {
		fail(err)
	}
}

func commandNeedsDaemon(args []string) bool {
	switch args[0] {
	case "version", "protocol", "health", "doctor", "daemon", "init", "watch":
		return false
	case "hooks":
		return len(args) > 1 && args[1] == "session-start"
	default:
		return true
	}
}

func shouldCheckDaemonRevision(args []string) bool {
	if len(args) == 0 || args[0] == "init" || args[0] == "protocol" || args[0] == "version" || args[0] == "watch" {
		return false
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return false
		}
	}
	return true
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: dibs [--json] [--actor <name>] <command>

Global flags:
  --json                Output in JSON format (default: human-readable)
  --actor <name>        Set the acting identity (default: DIBS_ACTOR env)

`)
	fmt.Fprint(w, rootHelp())
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func fail(err error) {
	var argErr *cliArgumentError
	if errors.As(err, &argErr) {
		if jsonOutput {
			json.NewEncoder(os.Stderr).Encode(core.APIErrorResponse{Error: core.NewAPIError(core.ErrValidationFailed, argErr.Error())})
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", argErr)
		}
		os.Exit(1)
	}
	var clientErr *client.ClientError
	if errors.As(err, &clientErr) {
		if jsonOutput {
			resp := core.APIErrorResponse{Error: core.APIError{Code: clientErr.Code, Message: clientErr.Message, Details: clientErr.Details}}
			json.NewEncoder(os.Stderr).Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", clientErr)
		}
		os.Exit(mapExitCode(clientErr.Code))
	}
	var githubErr *github.Error
	if errors.As(err, &githubErr) {
		if jsonOutput {
			json.NewEncoder(os.Stderr).Encode(core.APIErrorResponse{Error: core.NewAPIError(githubErr.Code, githubErr.Message())})
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", githubErr)
		}
		os.Exit(1)
	}
	// Транспортная/не-API ошибка
	if jsonOutput {
		json.NewEncoder(os.Stderr).Encode(core.APIErrorResponse{
			Error: core.NewAPIError("internal_error", err.Error()),
		})
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(1)
}

func mapExitCode(code string) int {
	switch code {
	case core.ErrNotFound:
		return 5
	case core.ErrLeaseHeld:
		return 3
	case core.ErrLeaseExpired:
		return 4
	case core.ErrConflict:
		return 2
	case core.ErrIssueNotReady:
		return 7
	case core.ErrDependencyCycle:
		return 6
	default:
		return 1
	}
}

func mapExitCodeErr(err error) int {
	if err == nil {
		return 0
	}
	var clientErr *client.ClientError
	if errors.As(err, &clientErr) {
		return mapExitCode(clientErr.Code)
	}
	return 1
}

func resolveActor(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if defaultActor != "" {
		return defaultActor, nil
	}
	if agent := getParentAgent(); agent != "" {
		return agent, nil
	}
	if sysUser := os.Getenv("USER"); sysUser != "" {
		return sysUser, nil
	}
	return "", fmt.Errorf("actor is required: set --actor flag or DIBS_ACTOR environment variable")
}

func getParentAgent() string {
	pid := os.Getppid()
	for pid > 1 {
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		data, err := os.ReadFile(statPath)
		if err != nil {
			break
		}
		s := string(data)
		idx1 := strings.IndexByte(s, '(')
		idx2 := strings.LastIndexByte(s, ')')
		if idx1 != -1 && idx2 != -1 && idx2 > idx1 {
			comm := s[idx1+1 : idx2]
			// Skip common shells and daemons
			if comm != "bash" && comm != "sh" && comm != "zsh" && comm != "tmux" && comm != "tmux: server" && comm != "su" && comm != "sudo" && comm != "sshd" && comm != "systemd" && comm != "init" {
				return fmt.Sprintf("%s-%d", comm, pid)
			}
			parts := strings.Split(s[idx2+2:], " ")
			if len(parts) >= 2 {
				ppid, _ := strconv.Atoi(parts[1])
				if ppid == 0 || ppid == pid {
					break
				}
				pid = ppid
				continue
			}
		}
		break
	}
	return ""
}

// ─── Health ──────────────────────────────────────────────────────────────────

func runHealth(ctx context.Context, c *client.Client) error {
	health, err := c.Health(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(health)
		return nil
	}
	fmt.Printf("Name:       %s\n", health.Name)
	fmt.Printf("Status:     %s\n", health.Status)
	fmt.Printf("Version:    %s\n", health.Version)
	fmt.Printf("Revision:   %s\n", health.Revision)
	fmt.Printf("DBPath:     %s\n", health.DBPath)
	fmt.Printf("SocketPath: %s\n", health.SocketPath)
	fmt.Printf("Time:       %s\n", health.Time.UTC().Format(time.RFC3339))
	return nil
}

// runLs is a top-level shortcut for `dibs issue list`.
func runLs(ctx context.Context, c *client.Client, args []string) error {
	return runIssueList(ctx, c, args)
}

// runShow is a top-level shortcut for `dibs issue get`.
func runShow(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("Usage: dibs show <issue-id-or-short-id> [--full]")
	}
	return runIssueGet(ctx, c, args)
}

// runDependency is a top-level shortcut for `dibs issue dependency`. It keeps
// a misplaced `dibs dependency ...` invocation inside the issue dependency
// namespace, so usage errors name the canonical `dibs issue dependency` path
// instead of falling through to the generic global usage block.
func runDependency(ctx context.Context, c *client.Client, args []string) error {
	return runIssueDependency(ctx, c, args)
}

// printVersion works without a daemon so bug reports can identify the binary.
func printVersion() {
	if jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		}{Version: build.Version, Revision: build.Revision})
		return
	}
	fmt.Printf("dibs %s (%s)\n", build.Version, build.ShortRevision())
}

func printIssue(i core.Issue) {
	fmt.Printf("ID:           %s\n", i.ID)
	fmt.Printf("Short ID:     %s\n", i.ShortID)
	fmt.Printf("Project ID:   %s\n", i.ProjectID)
	if i.RepositoryID != "" {
		fmt.Printf("Repository ID:%s\n", i.RepositoryID)
	}
	if i.WorktreeID != "" {
		fmt.Printf("Worktree ID:  %s\n", i.WorktreeID)
	}
	fmt.Printf("Scope:        %s\n", i.ScopeKind)
	fmt.Printf("Type:         %s\n", i.IssueType)
	fmt.Printf("Title:        %s\n", i.Title)
	if i.ExternalKey != "" {
		fmt.Printf("External Key: %s\n", i.ExternalKey)
	}
	if i.Description != "" {
		fmt.Printf("Description:  %s\n", i.Description)
	}
	if i.AcceptanceCriteria != "" {
		fmt.Printf("Acceptance:   %s\n", i.AcceptanceCriteria)
	}
	fmt.Printf("Status:       %s\n", i.Status)
	fmt.Printf("Priority:     %d\n", i.Priority)
	if i.Assignee != "" {
		fmt.Printf("Assignee:     %s\n", i.Assignee)
	}
	if i.Holder != "" {
		fmt.Printf("Holder:       %s\n", i.Holder)
	}
	fmt.Printf("Version:      %d\n", i.Version)
	if i.ClaimedAt != "" {
		fmt.Printf("Claimed At:   %s\n", i.ClaimedAt)
	}
	if i.ClosedAt != "" {
		fmt.Printf("Closed At:    %s\n", i.ClosedAt)
	}
	fmt.Printf("Created:      %s\n", i.CreatedAt)
	fmt.Printf("Updated:      %s\n\n", i.UpdatedAt)
}

// ─── Issue Display Helpers ───────────────────────────────────────────────────

// statusSymbol returns a Unicode symbol representing the issue status.
func statusSymbol(status string) string {
	switch status {
	case "open":
		return "○"
	case "in_progress":
		return "●"
	case "done":
		return "✓"
	case "cancelled":
		return "✗"
	case "blocked":
		return "⊘"
	case "deferred":
		return "◌"
	default:
		return "?"
	}
}

// truncate shortens a string to n characters, adding "..." if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}

// printIssueDetailed displays a single issue with basic details.
func printIssueDetailed(i core.Issue, l *core.IssueLease) {
	fmt.Printf("ID:         %s\n", i.ID)
	fmt.Printf("Short ID:   %s\n", i.ShortID)
	fmt.Printf("Status:     %s %s\n", statusSymbol(i.Status), i.Status)
	fmt.Printf("Type:       %s\n", i.IssueType)
	fmt.Printf("Title:      %s\n", i.Title)
	if i.ExternalKey != "" {
		fmt.Printf("External:   %s\n", i.ExternalKey)
	}
	fmt.Printf("Priority:   %d\n", i.Priority)
	if i.Assignee != "" {
		fmt.Printf("Assignee:   %s\n", i.Assignee)
	} else {
		fmt.Printf("Assignee:   (unassigned)\n")
	}
	fmt.Printf("Scope:      %s\n", i.ScopeKind)
	if len(i.Tags) > 0 {
		fmt.Printf("Tags:       %s\n", strings.Join(i.Tags, ", "))
	}
	fmt.Printf("Version:    %d\n", i.Version)
	if l != nil {
		fmt.Printf("Claimed:    %s (expires %s)\n", l.Holder, l.ExpiresAt)
		fmt.Printf("Attempt ID: %s\n", l.AttemptID)
		fmt.Printf("Generation: %d\n", l.LeaseGeneration)
		if l.SessionID != "" {
			fmt.Printf("Session ID: %s\n", l.SessionID)
		}
	} else {
		fmt.Printf("Claimed:    (not claimed)\n")
	}
	fmt.Printf("Created:    %s\n", i.CreatedAt)
	fmt.Printf("Updated:    %s\n", i.UpdatedAt)
}

// printIssueFull displays a single issue with all fields and full history.
func printIssueFull(i core.Issue, l *core.IssueLease, events []core.Event, notes []core.Note, links []core.ArtifactRef) {
	fmt.Printf("ID:            %s\n", i.ID)
	fmt.Printf("Short ID:      %s\n", i.ShortID)
	fmt.Printf("Status:        %s %s\n", statusSymbol(i.Status), i.Status)
	fmt.Printf("Type:          %s\n", i.IssueType)
	fmt.Printf("Title:         %s\n", i.Title)
	if i.ExternalKey != "" {
		fmt.Printf("External Key:  %s\n", i.ExternalKey)
	}
	if i.Description != "" {
		fmt.Printf("Description:   %s\n", i.Description)
	}
	if i.AcceptanceCriteria != "" {
		fmt.Printf("Acceptance:    %s\n", i.AcceptanceCriteria)
	}
	fmt.Printf("Priority:      %d\n", i.Priority)
	if i.Assignee != "" {
		fmt.Printf("Assignee:      %s\n", i.Assignee)
	} else {
		fmt.Printf("Assignee:      (unassigned)\n")
	}
	if len(i.Tags) > 0 {
		fmt.Printf("Tags:          %s\n", strings.Join(i.Tags, ", "))
	}
	fmt.Printf("Project ID:    %s\n", i.ProjectID)
	if i.RepositoryID != "" {
		fmt.Printf("Repository ID: %s\n", i.RepositoryID)
	}
	if i.WorktreeID != "" {
		fmt.Printf("Worktree ID:   %s\n", i.WorktreeID)
	}
	fmt.Printf("Scope:         %s\n", i.ScopeKind)
	fmt.Printf("Version:       %d\n", i.Version)
	if l != nil {
		fmt.Printf("Claimed:       %s (expires %s)\n", l.Holder, l.ExpiresAt)
		fmt.Printf("Attempt ID:    %s\n", l.AttemptID)
		fmt.Printf("Generation:    %d\n", l.LeaseGeneration)
		if l.SessionID != "" {
			fmt.Printf("Session ID:    %s\n", l.SessionID)
		}
	} else {
		fmt.Printf("Claimed:       (not claimed)\n")
	}
	if i.ClaimedAt != "" {
		fmt.Printf("Claimed At:    %s\n", i.ClaimedAt)
	}
	if i.ClosedAt != "" {
		fmt.Printf("Closed At:     %s\n", i.ClosedAt)
	}
	fmt.Printf("Created:       %s\n", i.CreatedAt)
	fmt.Printf("Updated:       %s\n", i.UpdatedAt)

	// A "blocks" edge is rendered from the blocked side as "Blocked By" and from
	// the blocking side as "Blocks"; it is intentionally omitted from this raw
	// list so the direction is never shown ambiguously (e.g. "blocks aion-190"
	// for an edge that actually means "blocked by aion-190").
	nonBlockDeps := make([]core.Dependency, 0, len(i.Dependencies))
	for _, dep := range i.Dependencies {
		if dep.Kind == "blocks" {
			continue
		}
		nonBlockDeps = append(nonBlockDeps, dep)
	}
	if len(nonBlockDeps) > 0 {
		fmt.Printf("\nDependencies:\n")
		for _, dep := range nonBlockDeps {
			label := dep.DependsOnShortID
			if label == "" {
				label = dep.DependsOnID
			}
			fmt.Printf("  - %s %s [%s]\n", dep.Kind, label, dep.DependsOnID)
		}
	}
	if i.Blocked || len(i.BlockedBy) > 0 {
		if len(i.BlockedBy) > 0 {
			fmt.Printf("Blocked:       yes\n")
			fmt.Printf("Blocked By:    %s\n", strings.Join(i.BlockedBy, ", "))
		} else {
			// Blocked with no dependency edge: the issue's own status is "blocked".
			fmt.Printf("Blocked:       yes (status)\n")
		}
	}
	if len(i.Blocks) > 0 {
		fmt.Printf("Blocks:        %s\n", strings.Join(i.Blocks, ", "))
	}

	if len(links) > 0 {
		fmt.Printf("\nLinks:\n")
		for _, link := range links {
			fmt.Printf("  - %s (%s, %s)\n", link.RelativePath, link.Kind, link.Relation)
		}
	}

	if len(events) > 0 {
		fmt.Printf("\nHistory:\n")
		for _, e := range events {
			fmt.Printf("  [%s] %s by %s\n", e.CreatedAt, e.EventType, e.Actor)

			// If it's a note_added event, try to find and print the note body.
			if e.EventType == "note_added" {
				var noteBody string
				for _, n := range notes {
					// Correlate note with event by timestamp and actor
					if n.CreatedAt == e.CreatedAt && n.Author == e.Actor {
						noteBody = n.Body
						break
					}
				}
				if noteBody != "" {
					fmt.Printf("    Note: %s\n", noteBody)
				}
			} else if e.PayloadJSON != "" && e.PayloadJSON != "{}" {
				fmt.Printf("    %s\n", e.PayloadJSON)
			}
		}
	}
}

// issueColumn is one selectable column of the issue table: its --columns key,
// header text, fixed print width, and how to render it for a given issue.
type issueColumn struct {
	key    string
	header string
	width  int
	value  func(core.Issue) string
}

// issueColumnDefs is the full set of columns available to --columns, in the
// default display order.
func issueColumnDefs() []issueColumn {
	return []issueColumn{
		{"id", "ID", 10, func(i core.Issue) string { return truncate(i.ID, 10) }},
		{"short", "SHORT", 10, func(i core.Issue) string { return truncate(i.ShortID, 10) }},
		{"status", "STATUS", 13, func(i core.Issue) string {
			status := statusSymbol(i.Status) + " " + i.Status
			if i.Blocked && i.Status != "blocked" {
				status += " [B]"
			}
			return truncate(status, 13)
		}},
		{"type", "TYPE", 8, func(i core.Issue) string { return truncate(i.IssueType, 8) }},
		{"title", "TITLE", 42, func(i core.Issue) string { return truncate(i.Title, 42) }},
		{"assignee", "ASSIGNEE", 10, func(i core.Issue) string { return truncate(i.Assignee, 10) }},
		{"claimed", "CLAIMED", 10, func(i core.Issue) string { return truncate(i.Holder, 10) }},
		{"blocked_by", "BLOCKED BY", 18, func(i core.Issue) string { return truncate(strings.Join(i.BlockedBy, ","), 18) }},
		{"deps", "DEPS", 34, func(i core.Issue) string { return truncate(formatIssueDependencies(i.Dependencies), 34) }},
		{"tags", "TAGS", 24, func(i core.Issue) string { return truncate(strings.Join(i.Tags, ","), 24) }},
	}
}

// defaultIssueColumns is the column key order used when --columns is not given.
func defaultIssueColumns() []string {
	defs := issueColumnDefs()
	keys := make([]string, len(defs))
	for i, d := range defs {
		keys[i] = d.key
	}
	return keys
}

// parseIssueColumns validates a comma-separated --columns value against the
// known column keys and returns them in the order given (repeats allowed).
func parseIssueColumns(value string) ([]string, error) {
	defs := issueColumnDefs()
	valid := make(map[string]bool, len(defs))
	names := make([]string, len(defs))
	for i, d := range defs {
		valid[d.key] = true
		names[i] = d.key
	}

	var columns []string
	for _, part := range strings.Split(value, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		if !valid[key] {
			return nil, fmt.Errorf("unknown column %q (valid: %s)", key, strings.Join(names, ", "))
		}
		columns = append(columns, key)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("--columns requires at least one column")
	}
	return columns, nil
}

// printIssuesTable displays a list of issues in a fixed-width table format,
// showing only the given columns (in the given order). A nil or empty
// columns list falls back to defaultIssueColumns.
func printIssuesTable(issues []core.Issue, columns []string) {
	if len(columns) == 0 {
		columns = defaultIssueColumns()
	}
	byKey := make(map[string]issueColumn, len(columns))
	for _, d := range issueColumnDefs() {
		byKey[d.key] = d
	}
	selected := make([]issueColumn, 0, len(columns))
	for _, key := range columns {
		if d, ok := byKey[key]; ok {
			selected = append(selected, d)
		}
	}
	if len(selected) == 0 {
		selected = issueColumnDefs()
	}

	var format strings.Builder
	for i := range selected {
		if i > 0 {
			format.WriteByte(' ')
		}
		format.WriteString("%-*s")
	}
	format.WriteByte('\n')
	rowFormat := format.String()

	headerArgs := make([]interface{}, 0, len(selected)*2)
	dashArgs := make([]interface{}, 0, len(selected)*2)
	for _, col := range selected {
		headerArgs = append(headerArgs, col.width, col.header)
		dashArgs = append(dashArgs, col.width, strings.Repeat("-", len(col.header)))
	}
	fmt.Printf(rowFormat, headerArgs...)
	fmt.Printf(rowFormat, dashArgs...)

	for _, i := range issues {
		rowArgs := make([]interface{}, 0, len(selected)*2)
		for _, col := range selected {
			rowArgs = append(rowArgs, col.width, col.value(i))
		}
		fmt.Printf(rowFormat, rowArgs...)
	}
}

func formatIssueDependencies(dependencies []core.Dependency) string {
	labels := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		if dep.Kind == "blocks" {
			continue
		}
		label := dep.DependsOnShortID
		if label == "" {
			label = dep.DependsOnID
		}
		if dep.Kind != "" {
			label = dep.Kind + ":" + label
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ",")
}

func printWorktree(wt core.Worktree) {
	mainStr := ""
	if wt.IsMain {
		mainStr = " (main)"
	}
	ephemeralStr := ""
	if wt.IsEphemeral {
		ephemeralStr = " [ephemeral]"
	}
	fmt.Printf("ID:           %s%s%s\n", wt.ID, mainStr, ephemeralStr)
	fmt.Printf("Repository ID:%s\n", wt.RepositoryID)
	fmt.Printf("Path:         %s\n", wt.AbsolutePath)
	fmt.Printf("Branch:       %s\n", wt.Branch)
	if wt.HeadCommit != "" {
		fmt.Printf("Head:         %s\n", wt.HeadCommit)
	}
	if wt.RemoteName != "" {
		fmt.Printf("Remote:       %s/%s\n", wt.RemoteName, wt.RemoteBranch)
	}
	fmt.Printf("Last Seen:    %s\n", wt.LastSeenAt)
	fmt.Printf("Created:      %s\n", wt.CreatedAt)
	fmt.Printf("Updated:      %s\n\n", wt.UpdatedAt)
}
