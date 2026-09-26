# af-coordinator repo instructions

## Purpose

This repository builds a local-first coordination daemon for multi-project,
multi-repository, multi-worktree AI execution.

## Non-negotiable rules

- Never add AI attribution trailers such as `Co-authored-by`.
- Keep SDD as the source of truth for scope and design.
- Keep the coordinator as the source of truth for execution state.
- Do not invent feature behavior in code that is not grounded in the active spec.
- Do not access SQLite directly from helper scripts or agents for mutations.
- Keep real runtime data out of git.
- **All implementation work MUST go through a git worktree, never write directly to the `main/` checkout.** See CodeWhale agent conventions below.

## SDD workflow

Canonical flow for meaningful work:

```text
requirements.md -> design.md -> tasks.md -> implementation -> review.md
```

Repository location:

```text
docs/specs/NNN-feature/
```

Active packet selection:

- treat the active SDD packet as the lowest-numbered `docs/specs/NNN-*` packet
  that is not explicitly declared complete by its packet-local artifacts
- `docs/specs/005-aion-forge-integration-readiness/` is completed
- `docs/specs/006-public-readiness/` is completed
- `docs/specs/007-release-and-backup-readiness/` is completed
- if no packet remains active, do not invent one in code or issue text; create
  a new packet first when the next meaningful track starts

Packet shape:

- minimum v1 packet:
  - `README.md`
  - `requirements.md`
  - `design.md`
  - `tasks.md`
  - `review.md`
- supporting artifacts are first-class when present:
  - `decisions/`
  - `traceability.md`
  - `glossary.md`
  - `schemas/`

Rules:

- do not start meaningful implementation before `requirements.md`, `design.md`,
  and `tasks.md` exist
- tiny mechanical fixes may skip a full spec packet
- when implementing a task, reference the concrete artifact path in commit and
  handoff language when useful
- treat `tasks.md` as the canonical task-slicing artifact for the packet
- do not mark a `tasks.md` entry done just because supporting code or documents
  exist; mark it done only when the described slice is actually complete and
  verified
- do not assume task status is inferred automatically from commits or file
  presence; update `tasks.md` and `review.md` deliberately as part of finishing
  the slice
- use `review.md` to capture what shipped, what was verified, what remains open,
  and whether implementation still matches requirements and design

## Architecture assumptions for v1

- simple service layout
- stdlib first
- manual dependency wiring
- SQLite WAL backend
- HTTP+JSON over Unix socket

Do not introduce frameworks, DI containers, or plugin systems unless the spec
explicitly calls for them.

Enum values in the public contract (issue statuses, event types) are
snake_case by design (`in_progress`); do not "fix" them to kebab-case.
Rationale: `docs/specs/001-foundation/design.md`, Naming conventions.

## Directory intent

- `cmd/dibsd/` daemon entrypoint only
- `cmd/dibs/` CLI entrypoint only
- `internal/api/` HTTP transport and handlers
- `internal/core/` domain logic and contracts
- `internal/store/sqlite/` SQLite-specific persistence
- `docs/specs/` SDD canon
- `migrations/` schema migrations

Keep `main.go` files thin. Push behavior down into `internal/`.

## CodeWhale agent conventions

This project uses a `.bare` git worktree model. All implementation work
MUST go through a git worktree, never write directly to the `main/` checkout.

CodeWhale upstream supports first-class sub-agent worktree isolation on
`agent_open` / sub-agent spawn arguments through `worktree`,
`worktree_branch`, `worktree_base`, and `worktree_path`. For this repository:

- when opening a sub-agent, use the `agent_open` / spawn-argument form, not an
  abstract `agent(...)` shorthand
- set `worktree: true`
- always set `worktree_path` explicitly
- prefer setting `worktree_branch` explicitly too, so the child branch name is
  deterministic and sibling worktree cleanup is easier
- use an absolute sibling path at the repo root; relative paths still resolve
  under CodeWhale's default `.codewhale-worktrees/` root
- do not rely on CodeWhale defaults for this repository; without an explicit
  absolute `worktree_path`, CodeWhale will create the child checkout under
  `.codewhale-worktrees/...`
- do not pass both `cwd` and `worktree`; for isolated parallel edits in this
  repo, use `worktree`, not a hand-picked `cwd`
- `.codewhale/hooks.toml` enforces these spawn requirements for both the current
  `agent` tool name and the documented `agent_open` name; a rejected spawn must
  be retried with the required arguments, not bypassed by editing `main/`
- the same hook policy blocks direct file-write and shell tools when the active
  CodeWhale workspace is `main/`; use read-only tools there and perform all
  mutations and verification commands in the sibling worktree
- in CodeWhale 0.8.66 this enforcement applies to the interactive TUI only;
  headless `codewhale exec` does not attach the hook executor, so never run a
  mutating headless task from `main/` -- launch it from the sibling worktree

Preferred shape:

```text
agent_open / sub-agent spawn args:
  {
    "worktree": true,
    "worktree_branch": "docs/example-branch",
    "worktree_path": "/home/abevz/github/af-coordinator/docs-example-branch"
  }
```

Wrong:

```text
agent_open / sub-agent spawn args:
  {
    "worktree": true
  }

This falls back to CodeWhale's default `.codewhale-worktrees/...` layout.
```

Every feature commit MUST land via a worktree branch that is merged into
main. Do not write or commit directly from the main checkout.

## Git topology

This repository uses a separate git dir:

```text
/home/abevz/github/af-coordinator/.bare
```

The current working tree is the main checkout.

Rules:

- prefer `git worktree` for parallel tracks instead of ad hoc directory copies
- do not create nested git repositories inside the working tree
- treat `.bare` as repository internals; do not edit it manually
- when additional worktrees are created, keep them tied to the same `.bare`
  repository

## Public repo boundary

This repository is expected to be publishable.

Do commit:

- code
- docs
- migrations
- sanitized examples
- service/unit files without embedded secrets

Do not commit:

- live SQLite databases
- runtime sockets or state files
- logs
- local overrides with secrets
- task exports containing real unsanitized operator data

## Build and verification

Preferred checks:

```text
gofmt -w .
go build ./...
go test ./...
```

Before finishing implementation work:

- run formatting
- build the repo
- run tests relevant to the touched code

### Verification budget

Verify in proportion to the change. The goal is evidence, not repeated
rebuilds.

- While iterating, run focused tests for the touched packages
  (`go test ./internal/<pkg>/...`) with the normal Go cache. Do not add
  `-count=1` or run `./...` after every edit.
- Once before opening or updating a PR, run `gofmt`, `go build ./...`, and
  `go test ./...`. Add `-count=1` only when a cached pass would mislead, for
  example when tests read environment, files, or time. Record the commands
  in the PR.
- Cross-compile (`GOOS=darwin GOARCH=arm64 go test -exec /bin/true <pkgs>`)
  only when the change touches platform-specific code: build tags,
  `_linux.go`/`_darwin.go` files, syscall constants, or OS limits such as
  socket path length. CI covers Linux; the release workflow installs natively
  on all four platforms when release paths change.
- If a slow or flaky test fails, rerun it once. If it fails again and is
  unrelated to the change, record it in the PR and file a coordinator issue
  instead of looping or blocking the task.
- Tests must not depend on the developer's environment. Clear inherited
  variables a test reads with `t.Setenv` (for example `DIBS_OPERATOR_TOKEN`,
  `AF_OPERATOR_TOKEN`, and `DIBS_*` paths); do not work around it by
  scrubbing the shell.
- Do not `git pull` in the `main/` checkout from task work. Its post-merge
  hook runs `make build-install` and restarts an active `dibsd`. Start task
  worktrees from `origin/main` after `git fetch` instead.

## Testing policy

Running tests is not the same as having tests. Rules:

- New behavior in `internal/` ships with tests in the same change: store
  functions and API handlers at minimum. "`go test ./...` passes" is not
  verification when the new code has no tests.
- Every bug fix includes a regression test that fails before the fix and
  passes after.
- Test fixtures must honor production data contracts. Timestamps are
  RFC 3339 UTC text (`2026-07-03T19:35:00Z`); never seed fixtures with
  `datetime('now')`. A fixture that mirrors a bug hides that bug — this is
  exactly how the lease-expiry comparison bug (AFC-SDD-0013) survived a
  99-test suite.
- Store tests run against in-memory SQLite created from the real embedded
  migrations, never hand-written DDL.
- Prefer table-driven tests with `t.Run` subtests.
- `cmd/` entrypoints stay thin and untested; anything worth testing lives
  below them in `internal/`.

## Scope control

- Do not replace spec artifacts with long issue descriptions or ad hoc notes.
- Do not add network-backed dependencies for core local operation.
- Do not widen from bootstrap/service skeleton into full feature delivery unless
  the user asks for that explicitly.

## Self-coordination (this repo eats its own dog food)

This repository is registered in its own coordinator as project **`afc`**.
The daemon you are modifying is the daemon that tracks your work — so:

- Before starting work, check `dibs issue ready --project afc`. If a
  coordinator issue exists for the spec task you are about to implement,
  claim it first and close it (with `--note`) when done. The issue links
  to the AFC-SDD task; the spec stays the contract, the issue tracks
  execution.
- If you implement a spec task that has no coordinator issue, that is
  fine — spec-first is the canon here. But if the issue exists and you
  ignore it, another agent may claim it and duplicate your work.
- The daemon must stay running while you work: your own claims live in
  the database you are changing. Never test destructive changes against
  the live daemon — use a scratch DB and socket via
  `DIBS_DB`/`DIBS_SOCKET` overrides.
- After changing daemon or CLI behavior: verify installed binaries under a
  scratch HOME and DB. The live service switch is an explicit operator step
  in `docs/operations.md`; do not restart it from a task worktree.
  Two of the same binary live in PATH history (`~/go/bin` vs
  `~/.local/bin`) — `make build-install` targets the right one.

<!-- BEGIN AF-COORDINATOR INTEGRATION v:1 -->
This repo is coordinated by [dibs](https://github.com/abevz/dibs).

- **Canonical protocol**: run `dibs protocol`; source: `https://github.com/abevz/dibs/blob/main/docs/agent-protocol-v1.md`
- **Identity**: `dibs` automatically infers your agent name and process PID from the process tree. You may optionally override this by exporting `DIBS_ACTOR=<agent-name>`.
- **Session cycle**: `ready → claim → heartbeat → handoff/close`
- **Lease tokens**: Agents never pass `--lease-token` on an executed command line, including shell expansion. Use `dibs issue run` for the lifecycle. For unavoidable manual steps, supply `DIBS_LEASE_TOKEN` in the environment or `DIBS_LEASE_TOKEN_FILE` pointing to a private mode-0600 file, and omit the flag.
- **Worktree hygiene**: implement in a sibling worktree when the coordinated checkout is a read/merge anchor; after removing a merged task worktree, use `dibs worktree prune --repo <repo-id>` (or `dibs worktree unregister --worktree <id>` for one known safe record).
- **Never** edit files without an active claim.
- **Never** touch the coordinator database.
- **Never** restate specs in issue descriptions — link them.
- **Never** close an issue without a note (`--note`) — the audit trail is for whoever comes after you.
<!-- END AF-COORDINATOR INTEGRATION -->
