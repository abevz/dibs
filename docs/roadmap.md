# Roadmap

Direction for dibs toward v1 and beyond. The operational source of truth for
this work is the coordinator itself (project `afc`); this document records
the intent and the reasoning so the issues stay short.

## Current direction: installation and first use

The [delivery plan saved on 2026-09-25](specs/016-adoption/implementation-plan.md)
records the next sequence: Linux/macOS installation (`afc-128`), first-use setup
(`afc-129`), everyday agent integration and visibility, then attachments and a
GitHub adapter. Jira is a future adapter/plugin possibility; the owner has no
Jira instance, and no implementation is committed. Swarm is an optional later
extension and does not gate launch.

Next: [packet 018](specs/018-github-import/README.md) (proposed) adds a thin
GitHub slice for `v0.1.0-rc.4`: import an issue by URL and publish the closed
result as one comment. The public launch post waits for it; attachments and
the full tracker workflow stay in the 016 plan.

Packet 015 is complete; its [closure evidence](specs/015-coordination-safety/review.md)
remains the safety baseline. The sections below preserve earlier roadmap
decisions. Read the saved plan and its source-precedence/resume checklist before
starting new work; live issue states still belong to the coordinator.

## Completed target: coordination safety (`afc-102`)

The project is useful as a local execution ledger, but the 2026-08-11 audit
found inconsistent fencing, daemon/SQLite serialization, and retry semantics.
Packet `docs/specs/015-coordination-safety/` addressed that baseline and was
completed on 2026-09-24. The delivery order below records that completed track.

Delivery order:

```text
lease generation + atomic ownership predicates
    -> daemon/dependency serialization + fail-closed runner
    -> deterministic six-race proof
    -> idempotent retry + crash/restart/restore proof
    -> minimum safety telemetry + agent decision protocol
```

SQLite remains the canonical store. The target is one trustworthy local daemon,
not a replicated service.

### Historical backlog assessment (2026-08-11)

Live status, claims, and closure remain in project `afc`. Packet 015 records the
problem/evidence/scope/acceptance for `afc-103` through `afc-116`; this table
records only product ordering.

| Work | Decision | Reason |
| --- | --- | --- |
| `afc-103`-`afc-109` | **Stage 1 / P1** | Establish the ownership, ready/dependency, daemon writer, and launcher correctness boundary. |
| `afc-110` | **Stage 2 / P1 gate** | Prove all six required races on production-like multi-connection SQLite. |
| `afc-111`-`afc-114` | **Stage 3 / P1 gate** | Resolve ambiguous mutation outcomes and prove crash/restart/WAL/migration/restore behavior. |
| `afc-115`-`afc-116` | **Stage 4 / P2** | Make the hardened service operable and unambiguous for unattended agents. |
| `afc-70`, `afc-89`, `afc-90`, `afc-97` | **Keep after safety** | Valid later ergonomics/integration/audit features, rewritten and dependency-gated. |
| `afc-98`, `afc-99` | **Deferred decision gates** | Remote transport/proxy work needs completed local safety plus a new accepted threat-model packet. |

### Decision gate: remote worker access

Do not expose the coordinator off-host until `afc-102` is complete and maturity
levels 3-5 are re-assessed. If remote access is still needed, create a new SDD
packet that chooses one transport/proxy topology and defines server-derived
identity, least privilege, revocation, request limits, timeouts, audit, and
idempotency. The Unix socket remains the default and no listener is enabled
implicitly.

### Later gate: external tracker adapters

The durable boundary remains:

> `dibs` owns execution state. External trackers are optional
> planning and reporting surfaces.

After safety, specify a provider-neutral external-reference/mapping contract and
one narrow GitHub adapter. Import explicitly selected work; publish terminal
evidence; do not implement bidirectional shared authority.

## Completed target: execution telemetry and analytics hardening (`afc-47`)

Packet `docs/specs/008-execution-telemetry-and-analytics/` captures gaps found
from the first ten days of live multi-project use. It first makes causal event
order and close authorization trustworthy, then adds lease-attempt outcomes,
atomic HANDOFF, and a local project statistics report.

`afc-49` delivered causal event order, `afc-50` delivered close/reopen
authorization, `afc-51` delivered lease-attempt outcomes, `afc-52` delivered
atomic HANDOFF, and `afc-53` delivered the local project statistics report.
The track is complete.

Delivery order is:

```text
event sequence + close authorization
    -> lease attempts + atomic HANDOFF
    -> project statistics
```

The coordinator remains the issue/lease/audit control plane. It does not absorb
Temporal or Aion Forge workflow execution state, and the first report does not
add Prometheus, remote analytics, or a mutable rollup database.

## Completed target: public readiness (`afc-39`-`afc-44`)

Packet `docs/specs/006-public-readiness/` completed the public README refresh,
API endpoint map, safe worktree cleanup commands, install preflight, macOS
daemon install path, Linux service helper, and API-facing store boundary
cleanup. SQLite remains the only storage backend; the boundary keeps transport
code separate from persistence details without adding multi-database support.

## Completed target: release and backup readiness (`afc-45`-`afc-46`)

Packet `docs/specs/007-release-and-backup-readiness/` added tagged GitHub
release packaging, checksum manifests, release install docs, tag-driven version
injection, and macOS launchd backup parity with `dibs doctor` coverage.

## Completed target: Aion Forge integration (epic `afc-24`)

dibs now has the local tracker / control-surface primitives needed
by the [Aion Forge](https://github.com/abevz/aion-forge) agent factory:

```text
issue (dibs) -> Temporal workflow -> isolated runner -> branch/PR -> checks -> merge -> issue closed
```

Division of responsibility stays strict:

- dibs is the single write authority over issue state
  (status, leases, notes, audit trail)
- Temporal owns execution truth (retries, workflow progress, runner state)
- the coordinator stores references to execution (workflow IDs, PR URLs),
  never execution state itself

Delivered work:

| Issue | Type | What |
|---|---|---|
| `afc-30` | bug | Dependency response identity semantics: stop mixing UUID and short_id in dependency payloads; return explicit fields and cover the contract with regression tests |
| `afc-36` | bug | Scope repository resolution by project: remove ambiguous unscoped logical-name resolution before broader multi-project use |
| `afc-25` | feature | Events watch API: `GET /v1/events?since=` with long-poll, so consumers react to new ready issues without tight polling |
| `afc-26` | feature | External issue references on issues: start with mirrored issue keys / workflow IDs while keeping coordinator issue status authoritative |
| `afc-27` | feature | Structured resolution: PR/commit references on close |
| `afc-29` | feature | JSONL export (`internal/export`), backup + interim bridge |
| `afc-28` | feature | MCP server wrapper over the daemon API for Claude-based agents |

Packet `docs/specs/005-aion-forge-integration-readiness/` is complete. The
coordinator has no active roadmap target until the next direction is written
as a new SDD packet and corresponding `afc` issues.

The unclaimable-epic closure gap is resolved by explicit local operator
close/reopen commands with a reason and audit event; ordinary agent close
continues to require its active lease token.

## Issue classification (done)

`issue_type` shipped in migration `0002`: `task` (default), `bug`,
`feature`, `epic`, `chore`.

Design decisions, recorded here so they are not re-litigated:

- a single type column instead of free-form labels; labels wait until a
  concrete need appears
- `epic` is a container, not a unit of work: it cannot be claimed and is
  excluded from the ready view; children attach via the existing `parent`
  dependency kind
- type is the routing key for future agent pipelines
  (bug -> bugfix flow, feature -> SDD flow, chore -> lightweight flow)

## Non-goals (unchanged from v1)

- web UI
- distributed cluster mode / multi-node replication
- GitHub as source of truth
- embedded scripting

## Working agreement

New roadmap items start as issues in project `afc` (`dibs issue create
--project afc --type ...`). This file is updated only when direction
changes, not per issue.
