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

## afc-147 follow-up

Unknown commands and flags now point to the nearest local `--help` route;
`--h` is reported as an unknown flag. Leaf help gives typed value names and
short explanations for every routed flag. Project registration explains the
issue-prefix key and display name with a concrete example. The route/metadata
coverage test and CLI regression test preserve local, no-daemon help and
`issue run` child arguments. `go test ./cmd/dibs` and `go build ./...` passed
(`/tmp/afc-147-cli-test.log`, `/tmp/afc-147-build.log`).

An installed candidate under a scratch HOME, DB and absent socket passed the
reported invocations without opening the DB (`/tmp/afc-147-installed-check.log`).
Independent review found one inaccurate `--force` explanation; the final
revision fixes it for ordinary and operator updates. Focused help tests passed
again (`/tmp/afc-147-final-focused.log`), and independent read-only review of
that final code revision found no remaining material issue.

## afc-148 follow-up

The help audit found three public rendering paths: a manually maintained root
list, route-derived group lists without explanations, and route-derived leaf
help. The root list omitted several routed issue commands (including `tag`,
`edit`, and `unlink`). Root, group, and leaf help now share a compiled command
description catalog in `cmd/dibs/help_commands.go`; root and group membership
come from the route table, and `dependency`, `ls`, and `show` remain explicit
aliases. Handler-specific usage strings still provide validation error context;
all public `--help` routes render through the shared catalog.
The audit also preserved the root help's explicit `DIBS_OPERATOR_TOKEN`
requirement for cancel and operator commands in their root, group, and leaf
descriptions.

Coverage tests check every routed leaf and each parent group; the CLI test
executes `--help` for every registered route with an absent daemon socket.
`go test ./cmd/dibs`, `go build ./...`, and the installed-candidate scratch HOME/DB
check passed (`/tmp/afc-148-cli-test.log`, `/tmp/afc-148-build.log`,
`/tmp/afc-148-installed-check.log`). Independent review found that the first
`operator-release` summary omitted its transition to `open`; the final catalog
states that effect and a focused help test protects it
(`/tmp/afc-148-final-focused.log`). Independent read-only review of the
corrected code and test change found no remaining material issue.
