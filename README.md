# dibs

**A local execution ledger for AI agents working across repositories and git
worktrees.**

`dibs` is a local-first daemon that gives agents one place to decide
what is ready, claim it atomically, renew ownership, hand work off, and leave an
auditable result.

It gives agents a shared execution ledger: what work is ready, who claimed it,
what is blocked, what changed, and how a task was closed. Specs and source code
stay in git; runtime coordination state stays in the local daemon.

Use it when local AI agents need to cooperate safely without all of them
writing directly to a database, editing the same checkout, or duplicating work.

> **Project status: public preview.** The core local workflow is in daily use,
> but the CLI and HTTP API may still change before v1.0. Network clients and
> multi-machine synchronization are not supported yet.

`dibs` is not an agent runtime or a replacement for specs. It is the
execution control plane between planning and execution:

```text
specs / operator intent
          |
          v
dibs: ready -> claim -> heartbeat -> handoff / close
          |
          v
agents / runners -> worktree -> branch / PR / result
```

## What it does

- tracks projects, repositories, worktrees, artifacts, issues, dependencies,
  leases, notes, and events
- exposes a small HTTP+JSON API over a Unix socket
- ships `dibs`, a CLI for agents and humans
- computes a `ready` view from issue status, leases, and blockers
- classifies and routes issues with namespaced tags (`namespace/value`),
  filterable on the `ready`/`list` views — without a separate project
- records an append-only audit trail for claims, notes, updates, and closes
- keeps live runtime data out of git

## The five-minute value

Once a project and repository are registered, the operating loop is small:

```bash
# Human or agent creates work.
dibs issue create --project demo --scope-kind project \
  --title "Document the retry policy"

# Workers ask for work that is open, unblocked, and not leased.
dibs issue ready --project demo

# Exactly one worker receives the lease. The launcher heartbeats and closes
# or hands off around the child command; the token never enters argv.
dibs issue run demo-1 --ttl 900 -- ./do-the-work.sh
```

If two workers try to claim the same issue, one wins and the other receives a
stable `lease_held` error. If a worker disappears, its lease expires and the
work can become ready again — or, to recover it immediately instead of
waiting out the TTL (e.g. a script crashed before it ever persisted its
lease token), `dibs issue operator-release` clears the lease without one.

For a script that's just "do one thing, then close," `dibs issue run`
avoids the lost-token problem structurally instead of just recovering from
it — it claims, execs the command with the lease exported as environment
variables, heartbeats in the background, and closes or hands off
automatically based on the exit code, all inside one process:

```bash
dibs issue run demo-1 --ttl 900 -- ./do-the-work.sh
```

## Where it fits

| Concern | Source of truth |
|---|---|
| Requirements, design, acceptance criteria | SDD files or another planning system |
| Ready/blocked state, leases, attempts, handoffs | `dibs` |
| Code and review | Git worktrees, branches, commits, and pull requests |
| External visibility | Optional tracker integrations; not implemented yet |

This boundary is deliberate. GitHub Issues, GitLab Issues, Markdown specs, or
native coordinator issues can all describe work; the coordinator owns only the
live execution state used by agents.

## Why this exists

`dibs` gives local agents a shared record of task ownership and progress while
they work in separate repositories and worktrees.

The core design choice is simple:

- agents do not write to storage directly
- one daemon owns all writes
- clients talk to the daemon over a local API

## Quick start

### Preview release on Linux and macOS

Install the published `v0.1.0-rc.1` prerelease inside a Git repository:

```sh
curl -fsSL https://github.com/abevz/dibs/releases/download/v0.1.0-rc.1/install.sh | sh /dev/stdin
~/.local/bin/dibs init
```

The installer verifies the downloaded archive and needs no sudo. `dibs init`
starts the local daemon and displays the project key to use when creating your
first task. See [installation and first use](docs/install.md) for the complete
workflow, PATH setup, script inspection, and removal. GitHub's `latest` URL
does not select this prerelease.

If you already use Homebrew, install the same preview with
`brew install abevz/dibs/dibs`.

### Build from source

### Prerequisites

- Go version matching the `go` directive in [go.mod](go.mod)
- `make`
- `git` for clone/worktree workflows

Run the preflight first on a clean laptop or VM:

```bash
make preflight
```

The preflight checks required build tools, the Go version, the install
directory, and the current OS/service-manager situation.

The post-merge hook redeploys an active `dibsd` after merges into `main`.
While the former daemon is active, the [service switch](docs/operations.md#explicit-service-switch)
remains manual. Install the hook with:

```bash
make install-hooks
```

### Build and install

```bash
make build
make build-install
```

This builds `dibsd`, `dibs`, and `dibs-mcp` into `~/.local/bin/`, plus
compatibility command aliases.
Make sure `~/.local/bin` is on `PATH`.

The source build remains available for contributors and development. For a
regular preview installation, use the published release command above.

### Test

```bash
make test
```

CI (`.github/workflows/ci.yml`) runs `vet`, a `gofmt -l` check, `golangci-lint`, `test`, and
`build` on every pull request and on push to `main`; a PR must be green before merging.

### First use and daemon

Inside a Git repository, `dibs init` registers the project and worktree,
adds agent instructions, and starts `dibsd` when needed. See the
[first-use guide](docs/install.md#first-use) for the subsequent create and
claim commands. A dibs-started daemon can also be controlled explicitly:

```bash
dibs daemon start
dibs daemon stop
```

For foreground testing:

```bash
dibsd
```

On Linux with `systemd --user`:

```bash
make install-service
sh contrib/install/systemctl-user.sh enable --now dibsd
```

On macOS with `launchd`:

```bash
make install-launchd
```

The LaunchAgent is staged but not started; follow the explicit switch in
[operations](docs/operations.md#explicit-service-switch).

To inspect a running daemon:

```bash
dibs health
dibs doctor
```

`dibs doctor` is a post-install/runtime diagnostic. It checks daemon
reachability, client/daemon version skew, whether the daemon binary matches
the local git HEAD (run it from inside this checkout to catch a merge that
was never followed by an explicit service restart), backup setup, duplicate
binaries, and client/daemon config mismatch.

### Platform support

| Platform | Status |
|---|---|
| Linux | `dibs init` starts the daemon on first use; systemd user units remain available through `make install-service` and `make restart-service`. |
| macOS | `dibs init` starts the daemon on first use; launchd units are available through `make install-launchd`. |
| Other Unix-like OSes | Untested. The daemon relies on Unix sockets and local filesystem paths. |

### Configure

The daemon reads these environment variables:

| Variable | Default | Description |
|---|---|---|
| `DIBS_SOCKET` | `~/.local/state/dibs/dibsd.sock` | Unix socket path |
| `DIBS_DB` | `~/.local/share/dibs/dibs.db` | SQLite database path |
| `DIBS_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `DIBS_OPERATOR_TOKEN` | (unset) | Required by the daemon for `dibs issue operator-close/operator-reopen/operator-release`. Unset means those commands fail with `forbidden: DIBS_OPERATOR_TOKEN not configured on server`. The client (`dibs`) reads the same variable from its own environment, so the value must match on both sides. Under `systemd --user`, wire it in via an `EnvironmentFile=` drop-in pointing at a `600`-permission file outside the unit — see `contrib/systemd/dibsd.service`. |

The old database remains authoritative when present; a clean install uses the
new paths above. Canonical `DIBS_*` variables win if both names are set.

## Migrating from af-coordinator

The former commands `afctl`, `af-coordinatord`, and `afc-mcp` remain aliases
to `dibs`, `dibsd`, and `dibs-mcp`. An alias prints a deprecation notice only
to stderr; machine-readable stdout is unchanged. Existing `AF_*` environment
variables still work. If the old database at
`~/.local/share/af-coordinator/af-coordinator.db` exists, both command names
continue to use it and the old socket at
`~/.local/state/af-coordinator/af-coordinator.sock`. There is no automatic
data migration or second database creation. Project key `afc`, issue IDs, and
external keys retain their existing values. A future explicit migration tool
can move the live SQLite/WAL state after the service is stopped; this slice
does not provide `dibs migrate-paths`.

The installed old service unit is left untouched. To switch on Linux, install
the new binaries and unit, stop/disable the old service, then enable/start
`dibsd` and run `dibs doctor`. On macOS, install the new LaunchAgent file,
boot out the old agent, bootstrap the new one, and verify `dibs health`.
Do not run both daemons against the same database. Exact commands are in
[operations](docs/operations.md#explicit-service-switch).

Common worktree maintenance commands:

```text
dibs worktree list --repo <repo-id>
dibs worktree unregister --worktree <worktree-id>
dibs worktree prune --repo <repo-id>
```

`unregister` only removes a non-main worktree record when nothing still points
at it. `prune` is the safe cleanup path for stale records whose checkout path
is already gone on disk.

## Goals

- reliable local-first coordination without internet dependency
- atomic claim, release, update, and close operations
- support for many projects and repositories
- first-class worktree and remote awareness
- first-class links from operational work to SDD artifacts
- clear event log and audit trail
- simple recovery and backup model

## Non-goals for v1

- web UI
- distributed cluster mode
- multi-node replication
- GitHub-first source of truth
- embedded scripting or plugin system

## Public repo, private runtime data

This repository is intended to be safe to publish.

The rule is:

- code, docs, schema, migrations, and service definitions may live in git
- real runtime data must stay outside the repository

Expected private runtime locations:

- database: `~/.local/share/dibs/dibs.db`
- socket: `~/.local/state/dibs/dibsd.sock`
- logs/state: `~/.local/state/dibs/`

Do not commit:

- live databases
- local runtime state
- logs
- tokens, secrets, or `.env` files
- exports or snapshots containing real task data unless intentionally sanitized

## Architecture

```text
agents / scripts / tools
        |
        | HTTP+JSON over Unix socket
        v
dibsd
        |
        v
SQLite (WAL)
```

The daemon is the single write authority. Clients never open the database
directly.

## SDD methodology

This project is built spec-first.

For any meaningful feature, the canonical flow is:

```text
requirements.md -> design.md -> tasks.md -> implementation -> review.md
```

For `dibs`, that means:

- SDD artifacts define scope, contracts, and acceptance criteria
- `dibs` runtime state tracks execution, claims, blockers, notes, and handoff
- the coordinator does not replace the spec canon; it complements it

Initial SDD workspace:

```text
docs/specs/001-foundation/
  README.md
  requirements.md
  design.md
  tasks.md
  review.md
```

Rules for v1:

- no implementation starts before `requirements.md`, `design.md`, and `tasks.md` exist
- tiny mechanical fixes may skip a full spec packet
- operational issues should link to the relevant spec/task artifact instead of duplicating design intent in issue text

## Main domain model

- `projects`: logical top-level initiatives
- `repositories`: logical repos inside projects
- `repo_remotes`: tracked fetch/push remotes for each repository
- `worktrees`: concrete checkouts on disk, possibly pointing at different remotes
- `artifacts`: SDD files and related design artifacts tracked by path and kind
- `issues`: tasks, bugs, ops work, coordination units
- `dependencies`: blocking relationships between issues
- `leases`: active claims owned by agents
- `notes`: human/agent notes attached to issues
- `events`: append-only audit trail

Key semantics:

- issues carry human-facing short ids (`<project_key>-<N>`, e.g. `afc-42`)
  allocated by the daemon from a per-project counter
- "claimed" is not a stored status; it is derived from an unexpired lease
- issue statuses: `open`, `in_progress`, `blocked`, `deferred`, `done`,
  `cancelled`
- only `blocks` dependencies affect readiness, and the daemon rejects
  dependency cycles

In practice, the product exposes:

- a `ready` view
- dependency-aware task state
- issue notes / comments
- issue activity timeline
- queryable task listings

## Why projects, repositories, and worktrees are separate

This environment has:

- many projects
- multiple repositories per project
- multiple worktrees per repository
- some worktrees tracking different remotes

So identity cannot be based only on a filesystem path.

An issue may belong to:

- a project
- a repository
- optionally a specific worktree when the task is local to that checkout

## Repository layout

```text
cmd/dibsd/   daemon entrypoint
cmd/dibs/             CLI client
cmd/dibs-mcp/           MCP stdio wrapper over the daemon API
docs/                  design docs
internal/api/          transport layer
internal/client/       Go client for the daemon API
internal/mcp/          MCP protocol wrapper over the daemon API
internal/config/       daemon configuration
internal/core/         domain logic (validation, models, lease semantics)
internal/store/        API-facing store interface
internal/store/sqlite/ SQLite implementation (including lease operations)
migrations/            schema migrations
```

## v1 documentation

- [Architecture v1](docs/architecture-v1.md)
- [Schema v1](docs/schema-v1.md)
- [API v1](docs/api-v1.md)
- [MCP server v1](docs/mcp-server-v1.md)
- [Agent protocol v1](docs/agent-protocol-v1.md)
- [Workflows v1](docs/workflows-v1.md)
- [SDD workflow v1](docs/sdd-workflow-v1.md)
- [Foundation spec packet](docs/specs/001-foundation/README.md)
- [Roadmap](docs/roadmap.md) — direction beyond v1; operational tracking lives in project `afc` inside the coordinator itself

## Known limitations

- The coordinator assumes a single active daemon per machine.
- There is currently no web UI or built-in visualization.
- The project is designed for local-first operations and does not natively synchronize state across multiple machines.
- Privileged operator actions are local-trust operations today; hardening that
  boundary is the next security priority.
- External tracker integrations are planned as optional adapters, not as a new
  source of truth.

## License

dibs is licensed under the [Apache License 2.0](LICENSE).

## How to release

1. Ensure the `review.md` for the active SDD packet is complete and all related
   `afc` issues are closed.
2. Verify locally:
   ```bash
   go test ./...
   make build
   ```
3. Create and push a tag:
   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```
4. The `Release` GitHub Actions workflow builds Linux and macOS archives for
   `dibs`, `dibsd`, and `dibs-mcp`, then uploads
   `checksums.txt` to the GitHub release.
5. Verify the release install path:
   ```bash
   VERSION=vX.Y.Z sh contrib/install/install-release.sh
   dibs health
   ```
