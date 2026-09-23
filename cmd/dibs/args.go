package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/abevz/dibs/internal/core"
)

// cliArgumentError is returned before any daemon request for malformed CLI input.
type cliArgumentError string

func (e *cliArgumentError) Error() string { return string(*e) }
func argumentError(message string) error {
	e := cliArgumentError(message)
	return &e
}

type argRoute struct {
	flags string // space-separated flags; a trailing ? means a valueless flag
	pos   int    // exact positional count
}

var commandRoutes = map[string]argRoute{
	"health": {"", 0}, "doctor": {"", 0}, "protocol": {"", 0}, "version": {"", 0},
	"init":        {"--path --dry-run?", 0},
	"project add": {"--key --name --description", 0}, "project list": {"", 0},
	"repo add": {"--project --logical-name --canonical-git-dir --default-branch --remotes", 0}, "repo list": {"--project", 0},
	"worktree register": {"--repo --absolute-path --branch --head-commit --remote-name --remote-branch --main? --ephemeral?", 0},
	"worktree list":     {"--repo", 0}, "worktree prune": {"--repo", 0}, "worktree unregister": {"--worktree", 0},
	"artifact-root add": {"--repo --root-path --kind --primary?", 0}, "artifact-root list": {"--repo", 0},
	"artifact register": {"--repo --relative-path --kind --worktree --artifact-root --title --external-key --status", 0},
	"artifact list":     {"--repo", 0}, "export jsonl": {"", 0},
	"stats":             {"--project --repo --since --until", 0},
	"issue create":      {"--project --scope-kind --title --type --repo --worktree --external-key --description --acceptance --priority --tag --allow-duplicate? --operation-id --retry-last?", 0},
	"issue create-form": {"--allow-duplicate?", 0},
	"issue get":         {"--full?", 1}, "issue list": {"--project --status --type --repo --worktree --assignee --external-key --tag --limit --offset --columns", 0},
	"issue ready":     {"--project --repo --tag --columns", 0},
	"issue claim":     {"--holder --actor --ttl --session-id --invocation-mode --operation-id --retry-last?", 1},
	"issue heartbeat": {"--lease-token --lease-generation --ttl", 1}, "issue release": {"--lease-token --lease-generation", 1},
	"issue handoff":          {"--lease-token --lease-generation --note --invocation-mode", 1},
	"issue run":              {"--actor --ttl --close-resolution --branch --pr-url --commit-sha --note --invocation-mode", 1},
	"issue edit":             {"--title --type --external-key --description --acceptance --priority --assignee --status --expected-version --force? --lease-token --lease-generation --release?", 1},
	"issue update":           {"--title --type --external-key --description --acceptance --priority --assignee --status --expected-version --force? --lease-token --lease-generation --release?", 1},
	"issue close":            {"--resolution --expected-version --lease-token --lease-generation --branch --pr-url --commit-sha --note --invocation-mode", 1},
	"issue operator-close":   {"--resolution --expected-version --force? --reason --branch --pr-url --commit-sha --note", 1},
	"issue operator-reopen":  {"--expected-version --force? --reason", 1},
	"issue operator-release": {"--expected-version --force? --reason", 1},
	"issue cancel":           {"--note", 1},
	"issue link":             {"--artifact --path --repo --kind --relation", 1}, "issue unlink": {"--path --artifact --relation", 1},
	"issue dependency add":    {"--depends-on --kind --blocked-by --blocks", 1},
	"issue dependency remove": {"--depends-on --kind --blocked-by --blocks", 1},
	"issue note add":          {"--author --actor --body --invocation-mode", 1}, "issue note list": {"", 1},
	"issue tag add": {"--tag", 1}, "issue tag remove": {"--tag", 1}, "issue tag list": {"", 1},
	"issue events list": {"", 1},
}

var requiredCommandFlags = map[string]string{
	"project add":            "--key --name",
	"repo add":               "--project --logical-name --canonical-git-dir",
	"worktree register":      "--repo --absolute-path",
	"worktree unregister":    "--worktree",
	"artifact-root add":      "--repo --root-path",
	"artifact register":      "--repo --relative-path --kind",
	"issue create":           "--project --scope-kind --title",
	"issue heartbeat":        "--lease-token --lease-generation",
	"issue release":          "--lease-token --lease-generation",
	"issue handoff":          "--lease-token --lease-generation --note",
	"issue close":            "--resolution --expected-version --lease-token --lease-generation",
	"issue operator-close":   "--resolution --reason",
	"issue operator-reopen":  "--reason",
	"issue operator-release": "--reason",
	"issue note add":         "--body",
	"issue tag add":          "--tag",
	"issue tag remove":       "--tag",
}

// validateCommandArgs checks the whole route before main's revision probe or
// any command-specific parsing. No handler may silently ignore malformed input.
func validateCommandArgs(args []string) error {
	if len(args) == 0 {
		return argumentError("command is required")
	}
	path := []string{args[0]}
	switch args[0] {
	case "project", "repo", "worktree", "artifact-root", "artifact", "export":
		if len(args) < 2 {
			return argumentError(args[0] + " subcommand is required")
		}
		path = append(path, args[1])
	case "issue":
		if len(args) < 2 {
			return argumentError("issue subcommand is required")
		}
		if args[1] == "--help" || args[1] == "-h" || args[1] == "help" {
			return nil
		}
		path = append(path, args[1])
		if args[1] == "dependency" || args[1] == "note" || args[1] == "tag" || args[1] == "events" {
			if len(args) < 3 {
				return argumentError(strings.Join(path, " ") + " subcommand is required")
			}
			if args[2] == "--help" || args[2] == "-h" || args[2] == "help" {
				return nil
			}
			path = append(path, args[2])
		}
	case "dependency":
		if len(args) < 2 {
			return argumentError("dependency subcommand is required")
		}
		path = []string{"issue", "dependency", args[1]}
	case "ls":
		path = []string{"issue", "list"}
	case "show":
		path = []string{"issue", "get"}
	}
	key := strings.Join(path, " ")
	route, ok := commandRoutes[key]
	if !ok {
		return argumentError("unknown command: " + key)
	}
	start := len(path)
	if args[0] == "dependency" {
		start = 2
	}
	if args[0] == "ls" || args[0] == "show" {
		start = 1
	}
	rest := args[start:]
	if (strings.HasPrefix(key, "issue ") || key == "stats") && hasHelpFlag(rest) {
		return nil
	}
	if key == "issue run" {
		separator := -1
		for i, a := range rest {
			if a == "--" {
				separator = i
				break
			}
		}
		if separator < 0 || separator == len(rest)-1 {
			return argumentError("issue run requires -- <command>")
		}
		rest = rest[:separator]
	}
	if route.pos == 1 && key != "issue get" && (len(rest) == 0 || strings.HasPrefix(rest[0], "-")) {
		return argumentError(key + " requires an issue ID before flags")
	}
	allowed := make(map[string]bool)
	seen := make(map[string]bool)
	for _, flag := range strings.Fields(route.flags) {
		bare := strings.TrimSuffix(flag, "?")
		allowed[bare] = !strings.HasSuffix(flag, "?")
	}
	positionals := 0
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			if strings.TrimSpace(a) == "" {
				return argumentError(key + " requires a nonempty positional argument")
			}
			positionals++
			continue
		}
		needsValue, ok := allowed[a]
		if !ok {
			return argumentError("unknown flag: " + a)
		}
		seen[a] = true
		if !needsValue {
			continue
		}
		if i+1 == len(rest) || strings.HasPrefix(rest[i+1], "--") || rest[i+1] == "-h" {
			return argumentError(a + " requires a value")
		}
		v := rest[i+1]
		if strings.TrimSpace(v) == "" {
			return argumentError(a + " requires a value")
		}
		if err := validateFlagValue(key, a, v); err != nil {
			return err
		}
		i++
	}
	if route.pos >= 0 && positionals != route.pos {
		return argumentError(fmt.Sprintf("%s requires %d positional argument(s), got %d", key, route.pos, positionals))
	}
	for _, flag := range strings.Fields(requiredCommandFlags[key]) {
		if !seen[flag] {
			return argumentError(key + " requires " + flag)
		}
	}
	if key == "issue link" || key == "issue unlink" {
		if !seen["--artifact"] && !seen["--path"] {
			return argumentError(key + " requires --artifact or --path")
		}
		if seen["--artifact"] && seen["--path"] {
			return argumentError(key + " accepts only one of --artifact or --path")
		}
	}
	if key == "issue dependency add" || key == "issue dependency remove" {
		forms := 0
		for _, flag := range []string{"--depends-on", "--blocked-by", "--blocks"} {
			if seen[flag] {
				forms++
			}
		}
		if forms != 1 {
			return argumentError(key + " requires exactly one dependency target")
		}
		if seen["--kind"] && (seen["--blocked-by"] || seen["--blocks"]) {
			return argumentError("--kind cannot be combined with --blocked-by or --blocks")
		}
	}
	if key == "issue claim" && seen["--retry-last"] && seen["--operation-id"] {
		return argumentError("--retry-last and --operation-id are mutually exclusive")
	}
	if key == "issue create" && seen["--retry-last"] && seen["--operation-id"] {
		return argumentError("--retry-last and --operation-id are mutually exclusive")
	}
	if (key == "issue edit" || key == "issue update") && seen["--lease-token"] && !seen["--lease-generation"] {
		return argumentError("--lease-generation is required with --lease-token")
	}
	return nil
}

func validateFlagValue(command, flag, value string) error {
	switch flag {
	case "--operation-id":
		if err := core.ValidateOperationID(value); err != nil {
			return argumentError(err.Error())
		}
	case "--priority", "--ttl", "--lease-generation", "--limit", "--offset", "--expected-version":
		if flag == "--expected-version" && value == "latest" && command != "issue close" {
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || (flag != "--priority" && flag != "--offset" && flag != "--limit" && n <= 0) || ((flag == "--offset" || flag == "--limit") && n < 0) {
			return argumentError(flag + " requires a valid integer")
		}
	case "--invocation-mode":
		if !core.ValidInvocationMode(value) {
			return argumentError("invalid --invocation-mode: " + value)
		}
	case "--type":
		if command != "issue list" && strings.Contains(value, ",") {
			return argumentError("invalid --type: " + value)
		}
		for _, part := range strings.Split(value, ",") {
			if !core.ValidIssueType(strings.TrimSpace(part)) {
				return argumentError("invalid --type: " + value)
			}
		}
	case "--scope-kind":
		if value != "project" && value != "repository" && value != "worktree" {
			return argumentError("invalid --scope-kind: " + value)
		}
	case "--resolution", "--close-resolution":
		if value != "done" && value != "cancelled" {
			return argumentError("invalid " + flag + ": " + value)
		}
	case "--status":
		if command == "artifact register" {
			return nil
		} // free-form artifact metadata
		if command != "issue list" && strings.Contains(value, ",") {
			return argumentError("invalid --status: " + value)
		}
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "open" && part != "in_progress" && part != "blocked" && part != "deferred" && part != "done" && part != "cancelled" {
				return argumentError("invalid --status: " + value)
			}
		}
	case "--kind":
		if strings.HasPrefix(command, "issue dependency ") {
			if command == "issue dependency remove" && value != "blocks" {
				return argumentError("invalid --kind: " + value)
			}
			if value != "blocks" && value != "parent" && value != "related" && value != "discovered-from" {
				return argumentError("invalid --kind: " + value)
			}
		} else if (command == "artifact register" || command == "artifact-root add" || command == "issue link") && !core.ValidateArtifactKind(value) {
			return argumentError("invalid --kind: " + value)
		}
	case "--columns":
		if _, err := parseIssueColumns(value); err != nil {
			return argumentError(err.Error())
		}
	}
	return nil
}
