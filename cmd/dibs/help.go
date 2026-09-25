package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-help" || arg == "-h" || arg == "help"
}

// groupHelp renders namespaces before leaf validation. It uses commandRoutes
// so group and leaf help stay in sync as commands are added.
func groupHelp(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	path := args
	if isHelpArg(args[len(args)-1]) {
		path = args[:len(args)-1]
	}
	if len(path) == 0 || len(path) > 2 {
		return "", false
	}
	group := strings.Join(path, " ")
	switch group {
	case "project", "repo", "worktree", "artifact-root", "artifact", "export", "issue", "dependency",
		"issue dependency", "issue note", "issue tag", "issue events":
	default:
		return "", false
	}
	lookup := group
	if group == "dependency" {
		lookup = "issue dependency"
	}
	commands := map[string]bool{}
	for key := range commandRoutes {
		if suffix, ok := strings.CutPrefix(key, lookup+" "); ok {
			commands[strings.Fields(suffix)[0]] = true
		}
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: dibs %s <subcommand>\n\nSubcommands:\n", group)
	for _, name := range names {
		fmt.Fprintf(&b, "  %s\n", name)
	}
	fmt.Fprintf(&b, "\nRun dibs %s <subcommand> --help for flags.\n", group)
	return b.String(), true
}

func printGroupHelp(args []string) bool {
	if help, ok := groupHelp(args); ok {
		fmt.Fprint(os.Stdout, help)
		return true
	}
	return false
}

// leafHelp is generated from the same route and required-flag registry used by
// validation, so newly added flags are visible without a second usage string.
func leafHelp(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	path := []string{args[0]}
	switch args[0] {
	case "project", "repo", "worktree", "artifact-root", "artifact", "export", "issue", "dependency":
		if len(args) < 2 {
			return "", false
		}
		path = append(path, args[1])
		if args[0] == "issue" && (args[1] == "dependency" || args[1] == "note" || args[1] == "tag" || args[1] == "events") {
			if len(args) < 3 {
				return "", false
			}
			path = append(path, args[2])
		}
	case "ls":
		path = []string{"issue", "list"}
	case "show":
		path = []string{"issue", "get"}
	}
	if args[0] == "dependency" {
		path = append([]string{"issue"}, path...)
	}
	key := strings.Join(path, " ")
	route, ok := commandRoutes[key]
	if !ok {
		return "", false
	}
	required := make(map[string]bool)
	for _, flag := range strings.Fields(requiredCommandFlags[key]) {
		required[flag] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: dibs %s", key)
	if route.pos == 1 {
		b.WriteString(" <issue-id>")
	}
	for _, raw := range strings.Fields(route.flags) {
		flag := strings.TrimSuffix(raw, "?")
		if flag == "--lease-token" {
			continue // Secrets must never be suggested as argv values.
		}
		value := " <value>"
		if strings.HasSuffix(raw, "?") {
			value = ""
		}
		if required[flag] {
			fmt.Fprintf(&b, " %s%s", flag, value)
		} else {
			fmt.Fprintf(&b, " [%s%s]", flag, value)
		}
	}
	if key == "issue run" {
		b.WriteString(" -- <command> [args...]")
	}
	b.WriteString("\n\nRequired: ")
	if route.pos == 1 {
		b.WriteString("<issue-id>")
	} else {
		b.WriteString("none")
	}
	for _, flag := range strings.Fields(requiredCommandFlags[key]) {
		if flag != "--lease-token" {
			b.WriteString(", " + flag)
		}
	}
	switch key {
	case "issue link", "issue unlink":
		b.WriteString(", one of --artifact or --path")
	case "issue dependency add", "issue dependency remove":
		b.WriteString(", exactly one of --depends-on, --blocked-by, or --blocks")
	case "issue run":
		b.WriteString(", -- <command>")
	case "issue claim":
		b.WriteString(", actor via --holder, --actor, DIBS_ACTOR, parent agent, or USER")
	}
	b.WriteString("\nOptional flags: ")
	var optional []string
	for _, raw := range strings.Fields(route.flags) {
		flag := strings.TrimSuffix(raw, "?")
		if flag == "--lease-token" {
			continue
		}
		if !required[flag] {
			optional = append(optional, flag)
		}
	}
	if len(optional) == 0 {
		b.WriteString("none")
	} else {
		b.WriteString(strings.Join(optional, ", "))
	}
	b.WriteString("\n")
	if strings.Contains(requiredCommandFlags[key], "--lease-token") {
		b.WriteString("Lease token: use DIBS_LEASE_TOKEN or DIBS_LEASE_TOKEN_FILE; never put a token in argv. Prefer dibs issue run.\n")
		b.WriteString(lifecycleHint + "\n")
	}
	return b.String(), true
}

func printLocalHelp(args []string) bool {
	if !hasHelpFlag(args) {
		return false
	}
	// Child arguments following issue run's separator belong to that process.
	for i, arg := range args {
		if arg == "--" && i > 1 && args[0] == "issue" && args[1] == "run" {
			args = args[:i]
			break
		}
	}
	if !hasHelpFlag(args) {
		return false
	}
	if help, ok := leafHelp(args); ok {
		fmt.Fprint(os.Stdout, help)
		return true
	}
	return false
}
