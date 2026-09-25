# 017 Agent Ergonomics Review

The implementation adds local CLI help from the route registry; route-validation
errors show required and optional arguments while directing lease tokens to environment
or file sources. Project selectors accept a key or UUID.
Repository JSON output adds top-level `id` while retaining `repository` and
`remotes`. The issue list applies `limit`/`offset` in SQLite after filters and
stable ordering. MCP ready results default to 50, cap at 100, and include
`project_key`; read-only project/repository/worktree/general-issue discovery
tools are available. The protocol points to CLI for registration, dependency,
link, and cancel mutations, preserving their existing authorization contracts.

Focused tests passed in `cmd/dibs`, `internal/api`, `internal/client`,
`internal/store/sqlite`, and `internal/mcp` (log:
`/tmp/afc-145-focused-test-2.log`). `go build ./...` passed. A rebuilt CLI and
daemon on an isolated `/tmp` DB/socket verified project registration, repo
registration by project UUID with top-level JSON `id`, issue creation, and
`issue list --limit 1 --offset 1`. That scratch run exposed a UUID-prefixed
short ID for UUID-based issue creation; a subsequent fix and regression test
use the canonical project key. The scratch daemon was stopped.

Independent review found four material gaps and the final revision fixes them:
repository-name worktree filtering now passes the resolved ID to the store;
leaf help displays required alternative targets; CLI pagination bounds match
the API; and both protocol and MCP inventory document the full discovery and
CLI fallback surface. Focused tests were rerun after these corrections
(`/tmp/afc-145-focused-final.log`). A full `go test ./...` run had one
`internal/api` shutdown timeout in `TestDaemonSafetyFieldsAndMutationLogs`;
that test passed when rerun alone (`/tmp/afc-145-safety-rerun.log`).

The first re-review found two help-path gaps. Lifecycle help now directs
agents to environment-backed lease tokens and `issue run`, while claim actor
resolution errors include the command usage and lifecycle hint. Targeted
token/help tests passed (`/tmp/afc-145-token-final.log`) and `go build ./...`
passed after those corrections. Independent read-only review of the completed
implementation found no remaining material issue.

## afc-146 follow-up

The installed CLI returned `unknown command: project --help`, rejected
`-help`, and returned `command is required` for a bare `dibs`. Root and group
help now dispatch locally before leaf validation. Group subcommands are
derived from the route table, and `projects` suggests `project` rather than
silently treating the typo as an alias. A no-daemon CLI test covers the forms
reported by the user and preserves `issue run` child arguments. Focused test
and build evidence: `/tmp/afc-146-cli-test.log`, `/tmp/afc-146-build.log`.

Independent read-only review of the final code and test change found no
remaining material issue.
