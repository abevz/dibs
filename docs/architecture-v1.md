# Architecture v1

## Objective

Build a local coordination backend for multiple AI agents that need safe
concurrent access to shared task state across many projects, repositories, and
worktrees.

## Design principles

- one daemon is the only writer
- clients use an API, not direct DB access
- task ownership is lease-based
- all updates use optimistic concurrency
- local operation must work without internet
- GitHub or other trackers can be mirrors, not the primary source of truth
- SDD artifacts remain the source of truth for feature scope and design
- execution state must link back to exact spec artifacts instead of duplicating them
- borrow proven task semantics from Beads where they improve UX and operator flow
- do not inherit Beads shared-Dolt write-path assumptions

## Transport

Primary transport:

- HTTP+JSON over Unix socket

Optional later:

- localhost TCP for debugging

Reasoning:

- easy to test with `curl --unix-socket`
- simple for shell wrappers and agent tools
- no need for gRPC complexity in v1

## Storage

Primary storage:

- SQLite in WAL mode

Settings expected for v1:

- `journal_mode=WAL`
- `synchronous=NORMAL`
- `busy_timeout`
- foreign keys on

Connection model:

- the daemon holds an exclusive advisory lock on the canonical database path
- one physical SQLite connection serializes reads and mutations in v1
- connection-local settings are encoded in the DSN and verified at startup
- this matches SQLite's single-writer reality instead of fighting it

Startup and runtime ownership:

- the persistent `<database>.lock` file is mode `0600`; the daemon holds its
  `flock` from before database migration until after listener/database shutdown
- a second daemon for the same canonical database path exits before touching
  its socket, while an existing socket is probed before stale removal
- default runtime directories are created and normalized to mode `0700`; DB,
  WAL, SHM, and lock files are restricted by a `0077` process umask, and the
  DB/lock are also normalized to `0600`; the socket remains `0660`, though its
  default `0700` parent restricts access to the operating user
- parents of custom `DIBS_DB` / `DIBS_SOCKET` paths remain
  operator-managed because changing an arbitrary configured directory could
  affect unrelated or deliberately shared files
- this is a cooperative single-UID correctness boundary, not protection from a
  hostile process that can open the database files directly

Driver decision:

- `modernc.org/sqlite` (pure Go, no cgo)
- fits the stdlib-first rule in `AGENTS.md`, keeps cross-compilation and
  static builds trivial
- `mattn/go-sqlite3` is the fallback if a profiled hot path ever demands cgo
  performance, which is not expected at this scale

Migrations:

- plain SQL files in `migrations/`, applied in filename order
- embedded migration runner in the daemon (stdlib only), tracking applied
  migrations by name in a `_migrations` table

Reasoning:

- robust enough for a single local daemon
- transactional
- simple backup story
- much lower operational cost than a separate SQL server

## Process model

```text
many clients -> one daemon -> store boundary -> one SQLite database
```

Clients:

- `dibs`
- agent wrappers
- future editor integrations

Daemon responsibilities:

- validation
- transactional writes
- lease management
- dependency checks
- event creation
- export endpoints

API handlers depend on `internal/store.CoordinatorStore`, not directly on the
SQLite package. The only implementation is still `internal/store/sqlite`; the
boundary exists to keep transport logic separate from persistence details, not
to make multiple databases a v1 feature.

## SDD integration

This project uses a spec-first workflow.

For repository development, the canonical flow is:

```text
requirements.md -> design.md -> tasks.md -> implementation -> review.md
```

That creates a clean boundary:

- spec artifacts own intent, scope, acceptance criteria, and design
- `dibs` owns execution state, claims, blockers, notes, and events

This is important because the coordinator is itself a task system. Without an
explicit boundary, the project would drift into storing design intent inside
issue descriptions and lose the benefits of SDD.

### Repository-level SDD layout

Recommended v1 convention:

```text
docs/specs/001-foundation/
docs/specs/002-next-track/
...
```

Each packet contains:

- `README.md`
- `requirements.md`
- `design.md`
- `tasks.md`
- `review.md`

### Coordinator support for SDD

The product should model SDD artifacts directly enough to answer:

- which spec packet owns this issue
- which `tasks.md` entry is being executed
- which review artifact closes the loop
- which repository/worktree contains the authoritative file

So the domain model needs explicit artifact registration and issue-to-artifact
links, not just free-form text fields.

## Beads-inspired product semantics

Beads is a useful source of task-system ideas even though its shared-Dolt server
is not the right backend for this workload.

### Concepts to adopt

- tasks as first-class objects
- blocking dependencies
- computed `ready` state instead of only stored status
- short stable ids
- per-issue notes and activity trail
- query-oriented CLI ergonomics

These are product semantics and UX patterns. They are worth keeping.

### Concepts not to adopt

- shared Dolt server as the coordination hot path
- auto-sync or auto-push in the mutation path
- multi-agent mutation through shell commands against a VCS-backed shared store

These are operational choices that became fragile under concurrent-agent load.

### Resulting design stance

The intended model for `dibs` is:

```text
Beads-inspired task semantics
        +
single-writer daemon
        +
SQLite WAL
```

This keeps the useful workflow ideas while replacing the fragile write path.

## Identity model

### Project

Top-level initiative. Can span many repositories.

Examples:

- `aion-forge`
- `utils`
- `platform-iac`

### Repository

Logical repository identity. Not just a local checkout path.

Suggested identity sources:

- canonical git dir
- normalized hosting slug when available
- logical name within the project

### Repo remote

Each repository may have many remotes:

- `origin`
- `upstream`
- fork remotes
- custom mirrors

These must be stored explicitly, not inferred ad hoc from one worktree.

### Worktree

Concrete checkout path on disk.

Tracks:

- absolute path
- branch
- HEAD commit
- selected remote
- selected remote branch
- whether it is the main checkout or an ephemeral worktree

### Artifact

An SDD or design artifact inside a repository.

Examples:

- `docs/specs/001-foundation/requirements.md`
- `docs/specs/001-foundation/design.md`
- `docs/specs/001-foundation/tasks.md`
- `docs/specs/001-foundation/review.md`
- `docs/specs/001-foundation/decisions/ADR-001-*.md`

Artifacts should be tracked by:

- repository
- optional worktree observation source
- artifact kind
- relative path
- optional external key or task id

### Issue scope

Issues should support these scopes:

- project-scoped
- repository-scoped
- worktree-scoped

Default expectation:

- most work is repository-scoped
- some operational tasks are worktree-scoped

### Issue identity

Every issue has two ids:

- `id`: opaque internal primary key
- `short_id`: human-facing stable id, format `<project_key>-<N>`,
  for example `afc-42`

The daemon allocates `short_id` from a per-project counter inside the
issue-create transaction, so ids are short, dense, and race-free. All CLI
and API surfaces accept the short id.

### Issue to artifact linkage

An issue may reference one or more artifacts, for example:

- implements `design.md`
- executes task from `tasks.md`
- blocked by missing `requirements.md`
- verified by `review.md`

This keeps operational coordination aligned with the spec canon.

## Concurrency model

### Claim leases

Claiming an issue must create a lease:

- `lease_token`
- `lease_generation`
- `holder`
- `expires_at`

There is no stored `claimed` status. "Claimed" is a derived property: an
issue is claimed if and only if it has an unexpired lease. This keeps one
source of truth and removes the possibility of a status stuck at claimed
after its lease died.

Every successful claim also receives a daemon-generated, non-secret
`attempt_id`. The live lease holds only the current attempt; the append-only
event stream preserves claim, release, handoff, close, and lazy-expiry outcomes. An
optional non-secret `session_id` is caller correlation data, separate from the
holder identity and lease token.

`lease_generation` is monotonically increasing per issue. It advances exactly
once for each fresh claim, is returned with the secret token, and is copied to
lifecycle events. Heartbeat keeps the existing generation. The generation is
safe to log and lets downstream publishers reject output from an older attempt
without exposing the token.

Holder names are attribution only. A repeated claim against an active lease
always returns `lease_held`, even when the holder string matches, and never
reads or returns the existing token. Safe retry/recovery requires the original
token plus generation or the future operation-id ledger; a lost token is
handled by TTL expiry or the explicit operator-release path.

### Lease expiry

Expiry is lazy: any lease with `expires_at` in the past is treated as
absent by every check (claim, mutation, ready). No background process is
required for correctness.

Lazy reclaim records one `lease_expired` event for the old attempt before the
replacement `issue_claimed` event. An optional background sweeper may use the
same event contract when it cleans up expired rows.

### Heartbeats

Long-running work should periodically extend the lease.

Heartbeats update only `leases.expires_at` and `leases.updated_at`. They
never append events; otherwise a lease renewed every few seconds would
flood the audit log.

If heartbeat stops:

- lease expires
- issue becomes claimable again

### Mutation matrix

Not every operation needs the full proof. Requiring a lease for everything
would make an unclaimed issue uneditable.

| Operation | Requires |
|---|---|
| agent close and edits under an active claim | `lease_token` + `expected_version` |
| operator close or reopen | `actor`, `expected_version`, non-empty `reason`; never a lease token |
| metadata edit of an unclaimed issue (title, priority, description) | `expected_version` only |
| append note, link artifact | nothing (append-only) |

Outside the explicit local operator override, if someone else holds an
unexpired lease, mutation is rejected. Every successful mutation increments
`version` and appends an event in the same transaction.

Generic update only handles metadata and non-terminal routing. Terminal close
uses the agent lease path, while unclaimable epics and deliberate
administrative close/reopen use explicit local operator paths with a reason.

### Optimistic concurrency

Every mutable row has `version`.

Mutating requests include `expected_version` (plus `lease_token` where the
matrix requires it). If the version mismatches, return conflict and require
reread.

### Walkthrough: two agents, one issue

The whole model in one scenario — one lease per issue, one version per
row, one writing daemon:

```text
Agent A                    daemon                       Agent B
   |                          |                            |
   |--- claim afc-42 -------->|                            |
   |<-- lease token, v=2 -----|  open -> in_progress       |
   |                          |<---------- claim afc-42 ---|
   |                          |---- 409 lease_held ------->|
   |                          |        (B picks other      |
   |                          |         ready work)        |
   |--- heartbeat (t+5m) ---->|  expires_at extended,      |
   |                          |  no event written          |
   |--- update, v=2 --------->|  v=3, event appended       |
   |                          |                            |
   X  A crashes, heartbeats   |                            |
      stop                    |  lease expires by itself:  |
                              |  lazy expiry, no cleanup   |
                              |  job needed                |
                              |                            |
                              |<---------- ready? ---------|
                              |---- afc-42 listed -------->|
                              |<---------- claim afc-42 ---|
                              |---- new lease, ok -------->|
                              |                            |
   |  A comes back, tries     |                            |
   |--- update, old token --->|                            |
   |<-- 410 lease_expired ----|  A rereads, moves on       |
```

Key properties this guarantees:

- no work is ever lost to a dead agent: expiry frees the issue without
  any operator action
- no two agents ever hold the same issue: the second claim fails loudly
- a resurrected agent cannot silently overwrite newer work: its stale
  token and stale version are both rejected

## Issue state machine

Recommended states for v1:

- `open`
- `in_progress`
- `blocked`
- `deferred`
- `done`
- `cancelled`

`deferred` means intentionally parked: not cancelled, but excluded from
the ready view until someone unparks it. It exists as a stored status
(not a priority hack) because operator tooling such as `daily-check
board` treats deferred work as a first-class lane.

Basic transitions:

```text
open -> in_progress      (claim)
in_progress -> open      (release / lease expiry sweep)
in_progress -> blocked
blocked -> in_progress
blocked -> open
open -> deferred         (park)
deferred -> open         (unpark)
in_progress -> done
any -> cancelled
done -> open             (reopen)
cancelled -> open        (reopen)
```

Claiming a `deferred` issue is not allowed; unpark it first.

Claim creates the lease and moves `open -> in_progress` in one transaction.
Release deletes the lease and moves `in_progress -> open` unless the issue
was explicitly left `blocked`. Normal agent handoff adds a `HANDOFF:` note and
then releases in the same transaction, preserving `note_added` before
`issue_released`; bare release remains available for recovery.

## Dependency kinds

- `blocks`: the only kind that affects ready computation
- `parent`: hierarchy
- `related`: informational
- `discovered-from`: provenance of follow-up work

The daemon must reject any `blocks` edge that would create a cycle
(reachability check inside the insert transaction). Without this, two
issues blocking each other silently disappear from the ready view forever.

## Ready logic

An issue is ready when:

- state is not `done`, `cancelled`, `deferred`, or `blocked`
- no unfinished issue blocks it through a `blocks` dependency
- it has no unexpired lease

`in_progress` with an expired lease stays in the ready view: the work is
claimable again, which is the whole point of lease expiry.

## Event log

Every important change should append an event:

- issue created
- issue claimed
- issue updated
- note added
- issue closed
- issue operator-closed
- issue reopened
- dependency added or removed
- lease expired (from the sweeper, at most one per lease)

The event log is the audit trail and recovery aid. It is append-only and
ordered by a daemon-assigned monotonic `sequence`, allocated in the same
transaction as the domain mutation. It must survive its subjects: nothing
cascades events away, and issues and projects are never hard-deleted in v1.
Legacy records before the `event_ordering_enabled` marker have a deterministic
display order only; equal timestamps are not causal evidence.

Over time, this should support a user-facing activity timeline comparable to
the good parts of Beads comments/history, but backed by daemon-owned writes.

## CLI model

`dibs` should be a thin client over the same API.

Core commands:

- `dibs issue create`
- `dibs issue get`
- `dibs issue list` / `dibs ls` — query issues; project, type, and status
  filters support comma-separated or repeated values with OR within each
  filter and AND across filters
- `dibs issue ready`
- `dibs issue claim`
- `dibs issue release`
- `dibs issue handoff`
- `dibs issue note`
- `dibs issue events`
- `dibs issue close`
- `dibs issue operator-close`
- `dibs issue operator-reopen`
- `dibs stats` — local, read-only execution-flow report
- `dibs project add`
- `dibs repo add`
- `dibs worktree register`
- `dibs worktree unregister`
- `dibs worktree prune`
- `dibs artifact register`
- `dibs issue link-artifact`

The CLI supports query-style list filters while keeping rendered views and JSON
arrays stable for automation.

`dibs stats` calls `GET /v1/stats` and renders the same versioned report as
JSON or a concise human view. Aggregation lives in `internal/report` behind a
read-only store contract, so it reuses coordinator evidence without opening
SQLite from CLI code, adding a rollup database, or reaching an external
telemetry service. It reports system flow only; it deliberately has no agent
ranking or productivity-score model.

## Integration model

Possible later integrations:

- markdown export
- JSONL export
- GitHub Issues mirror
- bridge from legacy Beads data
- import or discovery of repo-local SDD packets

But none of these should own the write path.

### Known consumer: daily-check

`daily-check board` (in `~/github/utils/daily-check/`) is the first known
external consumer. Today it reads Beads by shelling out to `bd list --json`
/ `bd query` / `bd ready` per configured repo, which breaks entirely
whenever the Dolt server is down.

Migration expectations when repos move to dibs:

- board provider switches from `bd` subprocesses to HTTP over the unix
  socket (`GET /v1/issues`, `GET /v1/issues/ready`) — one daemon call
  instead of N subprocesses, and no dependency on Dolt liveness
- the `Ready` column maps to the daemon-computed ready view instead of
  `bd ready` per repo
- status mapping: all board columns map directly — `Deferred` to the
  `deferred` status, `Open` (including blocked with a `[B]` badge),
  `In Progress`, and `Closed` (`done` + `cancelled`)
- board create actions go through `POST /v1/issues` like any other client

This consumer is a good API litmus test: if daily-check needs to bypass
the API, the API is too weak (a risk already named in the foundation
design).

### Agent integration model

Multiple different coding agents (Claude Code, Codex, CodeWhale, and
whatever comes next) must be able to work with the daemon. Three layers,
in priority order:

1. `dibs` as the universal adapter. Every agent can shell out; almost
   none of them share a richer protocol. Requirements this puts on the
   CLI: every command supports `--json` output, and exit codes distinguish
   "retryable coordination outcome" (conflict, lease held) from hard
   failure, so agent wrappers can react without parsing prose.
2. A canonical agent protocol contract: one short document in this repo
   describing the working loop — check `ready`, claim before touching
   code, heartbeat during long work, leave a handoff note, release or
   close at session end. Per-repo `AGENTS.md` / `CLAUDE.md` files only
   reference it, never restate it; that is what kept the Beads setup
   maintainable across agents.
3. Optional richer adapters later: an MCP server exposing the same API as
   tools, editor integrations, or a Claude Code skill wrapping `dibs`.
   These are conveniences for specific clients and are always plain API
   clients — they never bypass the daemon.

The protocol contract deserves its own spec packet once the issue/lease
APIs exist. The hard problem it must solve is adoption discipline, not
transport: an agent that silently skips claiming is worse than having no
coordinator at all.

Enforcement: agents with hook support (Claude Code and Codex both have
pre-tool-use hooks) should enforce the protocol mechanically — a hook
that checks for an active lease before file edits and warns or blocks.
Hooks call `dibs` like any other client. Agents without hooks fall back
to instructions plus review. The protocol spec packet should ship these
hook snippets alongside the contract text, so enforcing the rules costs
one config line per agent.

## Actor identity

In v1, `actor` and `holder` are client-asserted strings (agent name,
username, tool id). The trust boundary is the unix socket itself: mode
`0660`, owned by the operating user. Anything that can connect is trusted
to tell the truth about who it is.

Future hardening option: read peer credentials via `SO_PEERCRED` and record
them alongside the asserted actor. Not required for v1.

## Operational model

Expected runtime assets:

- DB: `~/.local/share/dibs/dibs.db`
- socket: `~/.local/state/dibs/dibsd.sock`
- logs/state: `~/.local/state/dibs/`

Environment overrides (mainly for tests and CI; unix socket paths are
limited to ~108 bytes, so tests need short paths):

- `DIBS_SOCKET`
- `DIBS_DB`
- `DIBS_LOG_LEVEL`

Suggested service model:

- `systemd --user` service for daemon
- optional timer for exports / snapshots

### Backup

Never copy a live WAL database with `cp`. Supported approaches:

- `VACUUM INTO '<backup path>'` from the daemon (preferred: single
  consistent file, no external tooling)
- `sqlite3 <db> ".backup <path>"` when the daemon is stopped

A `systemd --user` timer should produce periodic backups into
`~/.local/share/dibs/backups/`, plus optional JSONL exports for
greppable history.

## v1 implementation constraints

- no direct DB access from agents
- no shared mutable files as source of truth
- no server-side dependence on internet
- no automatic remote sync in the hot path
