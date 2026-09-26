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

// immediateSubcommands derives group membership from registered routes.
// The top-level dependency alias uses the canonical issue dependency routes.
func immediateSubcommands(group string) []string {
	lookup := group
	if lookup == "dependency" {
		lookup = "issue dependency"
	}
	prefix := ""
	if lookup != "" {
		prefix = lookup + " "
	}
	commands := map[string]bool{}
	for key := range commandRoutes {
		if suffix, ok := strings.CutPrefix(key, prefix); ok {
			commands[strings.Fields(suffix)[0]] = true
		}
	}
	if group == "" {
		for _, alias := range []string{"dependency", "ls", "show"} {
			commands[alias] = true
		}
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func commandPath(group, child string) string {
	if group == "" {
		return child
	}
	if group == "dependency" {
		return "issue dependency " + child
	}
	return group + " " + child
}

// rootHelp and groupHelp share the same route-derived tree and descriptions.
func rootHelp() string {
	var b strings.Builder
	b.WriteString("Commands:\n")
	var appendGroup func(string, int)
	appendGroup = func(group string, depth int) {
		for _, name := range immediateSubcommands(group) {
			key := commandPath(group, name)
			fmt.Fprintf(&b, "%s%-20s %s\n", strings.Repeat("  ", depth), name, commandDescriptions[key])
			if len(immediateSubcommands(key)) > 0 {
				appendGroup(key, depth+1)
			}
		}
	}
	appendGroup("", 1)
	b.WriteString("\nRun dibs <command> --help for details.\n")
	return b.String()
}

// groupHelp renders namespaces before leaf validation.
func groupHelp(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	path := args
	if isHelpArg(args[len(args)-1]) {
		path = args[:len(args)-1]
	}
	if len(path) == 0 {
		return "", false
	}
	group := strings.Join(path, " ")
	names := immediateSubcommands(group)
	if len(names) == 0 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: dibs %s <subcommand>\n\nSubcommands:\n", group)
	for _, name := range names {
		fmt.Fprintf(&b, "  %-20s %s\n", name, commandDescriptions[commandPath(group, name)])
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
	key := ""
	switch args[0] {
	case "ls":
		key = "issue list"
	case "show":
		key = "issue get"
	case "dependency":
		if len(args) >= 2 {
			key = "issue dependency " + args[1]
		}
	default:
		for end := len(args); end > 0; end-- {
			candidate := strings.Join(args[:end], " ")
			if _, ok := commandRoutes[candidate]; ok {
				key = candidate
				break
			}
		}
	}
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
		if key == "issue import" {
			b.WriteString(" <url|owner/repo#n>")
		} else {
			b.WriteString(" <issue-id>")
		}
	}
	for _, raw := range strings.Fields(route.flags) {
		flag := strings.TrimSuffix(raw, "?")
		if flag == "--lease-token" {
			continue // Secrets must never be suggested as argv values.
		}
		info, _ := helpForFlag(key, flag)
		value := ""
		if info.value != "" {
			value = " <" + info.value + ">"
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
	fmt.Fprintf(&b, "\n\n%s\n", commandDescriptions[key])
	var requiredParts []string
	if route.pos == 1 {
		if key == "issue import" {
			requiredParts = append(requiredParts, "<url|owner/repo#n>")
		} else {
			requiredParts = append(requiredParts, "<issue-id>")
		}
	}
	for _, flag := range strings.Fields(requiredCommandFlags[key]) {
		if flag != "--lease-token" {
			requiredParts = append(requiredParts, flag)
		}
	}
	if len(requiredParts) > 0 {
		b.WriteString("\nRequired: " + strings.Join(requiredParts, ", ") + "\n")
	}
	if route.pos == 1 {
		if key == "issue import" {
			b.WriteString("\nArgument:\n  <url|owner/repo#n>  GitHub issue URL or shorthand.\n")
		} else {
			b.WriteString("\nArgument:\n  <issue-id>  Issue ID or short ID (for example, afc-147).\n")
		}
	}
	if route.flags != "" {
		b.WriteString("\nFlags:\n")
		for _, raw := range strings.Fields(route.flags) {
			flag := strings.TrimSuffix(raw, "?")
			if flag == "--lease-token" {
				continue
			}
			info, _ := helpForFlag(key, flag)
			label := flag
			if info.value != "" {
				label += " <" + info.value + ">"
			}
			requirement := "Optional"
			if required[flag] {
				requirement = "Required"
			}
			fmt.Fprintf(&b, "  %-42s %s. %s\n", label, requirement, info.description)
		}
	}
	switch key {
	case "issue link", "issue unlink":
		b.WriteString("\nChoose one of --artifact or --path.\n")
	case "issue dependency add", "issue dependency remove":
		b.WriteString("\nChoose exactly one of --depends-on, --blocked-by, or --blocks.\n")
	case "issue run":
		b.WriteString("\nRequired: -- <command>; arguments after -- belong to the child.\n")
	case "issue claim":
		b.WriteString("\nActor requirement: provide an actor via --holder, --actor, DIBS_ACTOR, parent agent, or USER.\n")
	case "issue cancel", "issue operator-close", "issue operator-reopen", "issue operator-release":
		b.WriteString("\nAuthorization: set DIBS_OPERATOR_TOKEN in the environment.\n")
	}
	if strings.Contains(requiredCommandFlags[key], "--lease-token") {
		b.WriteString("Lease token: use DIBS_LEASE_TOKEN or DIBS_LEASE_TOKEN_FILE; never put a token in argv. Prefer dibs issue run.\n")
		b.WriteString(lifecycleHint + "\n")
	}
	if key == "project add" {
		b.WriteString("\nExample: dibs project add --key myapp --name \"My App\"\n")
		b.WriteString("The key becomes the issue ID prefix, such as myapp-1; the name is for people.\n")
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
