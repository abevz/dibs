package main

import "strings"

type flagHelp struct {
	value       string // empty for a switch
	description string
}

// flagHelpText is the shared vocabulary for the flags in commandRoutes.
// Command-specific meanings belong in commandFlagHelpText below.
var flagHelpText = map[string]flagHelp{
	"--agent":             {"claude|codex", "Agent whose project SessionStart hook is being configured."},
	"--absolute-path":     {"path", "Absolute filesystem path to the worktree."},
	"--acceptance":        {"text", "Conditions that must be met to finish the issue."},
	"--actor":             {"name", "Identity recorded as the acting agent or user."},
	"--allow-duplicate":   {"", "Allow another issue with the same external key."},
	"--artifact":          {"id-or-path", "Existing artifact ID or repository-relative path."},
	"--artifact-root":     {"id", "Registered artifact-root ID."},
	"--assignee":          {"name", "Agent or user assigned to the issue."},
	"--author":            {"name", "Author name recorded on the note."},
	"--blocked-by":        {"issue-id", "Issue that must finish before this issue can proceed."},
	"--blocks":            {"issue-id", "Issue that this issue prevents from proceeding."},
	"--body":              {"text", "Text of the issue note."},
	"--branch":            {"name", "Git branch associated with the work."},
	"--canonical-git-dir": {"path", "Absolute path to the repository's canonical Git directory."},
	"--close-resolution":  {"done|cancelled", "Resolution to use when issue run exits successfully."},
	"--columns":           {"names", "Comma-separated columns: id, short, status, type, title, assignee, claimed, blocked_by, deps, tags."},
	"--commit-sha":        {"sha", "Git commit recorded when closing the issue."},
	"--default-branch":    {"name", "Repository default branch (default: main)."},
	"--depends-on":        {"issue-id", "Other issue to connect using --kind."},
	"--description":       {"text", "Longer issue description."},
	"--dry-run":           {"", "Show the init change without writing it."},
	"--ephemeral":         {"", "Mark a worktree as temporary."},
	"--expected-version":  {"number|latest", "Issue version to guard against concurrent updates; latest fetches it first."},
	"--external-key":      {"key", "Identifier from an external tracker or source."},
	"--force":             {"", "Fetch the current issue version before applying the update."},
	"--full":              {"", "Include the issue's full details."},
	"--head-commit":       {"sha", "Current HEAD commit of the worktree."},
	"--holder":            {"name", "Identity that owns the issue lease."},
	"--invocation-mode":   {"interactive|scheduled|unknown", "How this action was started; recorded in the audit trail."},
	"--key":               {"project-key", "Unique lowercase project slug, at most 16 characters; prefixes issue IDs."},
	"--kind":              {"kind", "Artifact kind, for example spec, tasks, code, or doc."},
	"--lease-generation":  {"number", "Generation of the active issue lease."},
	"--lease-token":       {"token", "Read from DIBS_LEASE_TOKEN or DIBS_LEASE_TOKEN_FILE; never pass in argv."},
	"--limit":             {"number", "Maximum results to return (0–1000)."},
	"--logical-name":      {"name", "Repository name used to select it within the project."},
	"--new-path":          {"path", "Absolute new path for the registered canonical Git path (.git, .bare, or a legacy checkout)."},
	"--main":              {"", "Mark this as the repository's main worktree."},
	"--name":              {"display-name", "Human-readable project name; spaces are allowed."},
	"--note":              {"text", "Note recorded with the issue action."},
	"--offset":            {"number", "Number of matching issues to skip (0–1000000)."},
	"--once":              {"", "Print one watch snapshot without opening an interactive board."},
	"--operation-id":      {"id", "Stable ID for safely retrying the same mutation."},
	"--path":              {"path", "Filesystem or repository-relative path, depending on the command."},
	"--pr-url":            {"url", "Pull request URL recorded when closing the issue."},
	"--primary":           {"", "Mark this as the primary artifact root."},
	"--require-complete":  {"", "Close only after the child calls dibs hooks complete; otherwise hand off."},
	"--priority":          {"number", "Issue priority; lower numbers run first (default: 3)."},
	"--project":           {"key-or-id", "Project key or UUID."},
	"--reason":            {"text", "Reason for an operator override; recorded in the audit trail."},
	"--relation":          {"name", "Issue-to-artifact relation (for example, implements)."},
	"--relative-path":     {"path", "Artifact path relative to the repository."},
	"--release":           {"", "Release the active lease as part of the update."},
	"--remote-branch":     {"name", "Upstream branch tracked by the worktree."},
	"--remote-name":       {"name", "Git remote name (for example, origin)."},
	"--remotes":           {"json", "JSON array of repository remote records."},
	"--repo":              {"name-or-id", "Repository logical name or UUID."},
	"--resolution":        {"done|cancelled", "Final issue resolution."},
	"--retry-last":        {"", "Retry the last matching mutation using its operation ID."},
	"--root-path":         {"path", "Artifact-root path relative to the repository."},
	"--scope-kind":        {"project|repository|worktree", "Level of ownership for the new issue."},
	"--session-id":        {"id", "Session identifier associated with the claim."},
	"--since":             {"time-or-duration", "Start of the stats window, for example 24h or RFC3339 time."},
	"--status":            {"status", "Issue status: open, in_progress, blocked, deferred, done, or cancelled; list accepts comma-separated values."},
	"--tag":               {"namespace/value", "Issue tag; repeat the flag to add more on creation."},
	"--title":             {"text", "Short title for the issue or artifact."},
	"--ttl":               {"seconds", "Lease duration in seconds."},
	"--type":              {"task|bug|feature|epic|chore", "Issue type; list accepts comma-separated types."},
	"--until":             {"time", "End of the stats window as RFC3339 time (default: now)."},
	"--worktree":          {"id", "Registered worktree ID."},
}

var commandFlagHelpText = map[string]map[string]flagHelp{
	"init": {
		"--path":           {"path", "AGENTS.md path to create or update (default: ./AGENTS.md)."},
		"--project":        {"key", "Use this project key when Git context is ambiguous."},
		"--repo":           {"name", "Use this repository name when Git context is ambiguous."},
		"--default-branch": {"name", "Confirm the repository default branch when it cannot be inferred."},
	},
	"project add": {
		"--description": {"text", "Optional explanation of what the project tracks."},
	},
	"artifact-root add": {
		"--kind": {"kind", "Artifact-root kind (default: sdd); for example sdd or doc."},
	},
	"issue link": {
		"--path": {"relative-path", "Artifact path relative to the repository; creates it if needed."},
		"--kind": {"kind", "Kind used when --path creates an artifact (default: spec)."},
	},
	"issue unlink": {
		"--path": {"relative-path", "Artifact path relative to the repository."},
	},
	"issue dependency add": {
		"--kind": {"blocks|parent|related|discovered-from", "Relationship used with --depends-on; omission records a generic dependency."},
	},
	"issue dependency remove": {
		"--kind": {"blocks", "Relationship used with --depends-on."},
	},
	"artifact register": {
		"--status": {"text", "Free-form status metadata for the artifact."},
	},
	"issue handoff": {
		"--note": {"text", "Required HANDOFF: note for the next worker."},
	},
	"issue close": {
		"--expected-version": {"number", "Current issue version required to avoid overwriting another update."},
	},
	"issue list": {
		"--tag": {"namespace/value[,..]", "Return issues carrying every listed tag; repeat for more filters."},
	},
	"issue ready": {
		"--tag": {"namespace/value[,..]", "Return ready issues carrying every listed tag; repeat for more filters."},
	},
}

func helpForFlag(command, flag string) (flagHelp, bool) {
	if specific, ok := commandFlagHelpText[command][flag]; ok {
		return specific, true
	}
	info, ok := flagHelpText[flag]
	return info, ok
}

// helpPath returns the most specific known help target for a malformed call.
func helpPath(args []string) string {
	if len(args) == 0 {
		return "dibs --help"
	}
	if args[0] == "projects" {
		return "dibs project --help"
	}
	for end := len(args); end > 0; end-- {
		prefix := args[:end]
		key := strings.Join(prefix, " ")
		if _, ok := commandRoutes[key]; ok || key == "ls" || key == "show" ||
			(len(prefix) == 2 && prefix[0] == "dependency" && (prefix[1] == "add" || prefix[1] == "remove")) {
			return "dibs " + strings.Join(prefix, " ") + " --help"
		}
		if _, ok := groupHelp(prefix); ok {
			return "dibs " + strings.Join(prefix, " ") + " --help"
		}
	}
	return "dibs --help"
}
