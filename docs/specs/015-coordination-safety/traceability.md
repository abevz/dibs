# 015 Coordination Safety Traceability

All requirements are verified at epic closure after PR #77. Historical leaf
evidence and remaining trust boundaries are recorded in `review.md`.

| Requirement | Primary leaves | Audit evidence | Status |
| --- | --- | --- | --- |
| R-01 authoritative restart state | `afc-114` | Current Architecture; Crash/Recovery Analysis | **verified** — PR #75; active/expired lease restart and committed-outcome replay tests reopen file-backed SQLite |
| R-02 one mutation authority | `afc-108` | Authoritative state; Write ownership; SQLite correctness | **verified** — PR #60; singleton/two-process recovery, per-connection PRAGMAs, modes, installed runtime; same-UID limit explicit |
| R-03 atomic ready-qualified claim | `afc-106`, `afc-110` | Claim semantics; Race 1; Dependencies | **verified** — PRs #49/#64; blocked direct claim fails; race matrix gives one valid owner/event across independent handles |
| R-04 lease identity and fencing | `afc-103`–`afc-106`, `afc-110` | Claim semantics; Races 2–4 | **verified** — PRs #47/#62/#64; generation/token/expiry CAS and stale-owner race schedules |
| R-05 heartbeat and release | `afc-104`, `afc-113` | Lease/TTL; Heartbeat; Race 2 | **verified** — PRs #53/#73; unexpired CAS and original-outcome heartbeat/release replay |
| R-06 update/handoff/close | `afc-105`, `afc-113`, `afc-114` | Handoff; Close; Races 3–5 | **verified** — PRs #62/#73/#75; atomic fencing, exact replay, and daemon-kill before/after-commit proof |
| R-07 dependency/ready consistency | `afc-107`, `afc-110` | Dependencies / ready queue | **verified** — PRs #54/#64; serialized edge/cycle traversal and computed ready view over independent handles |
| R-08 lease time semantics | `afc-104`, `afc-114` | Lease/TTL; restart failure cases | **verified** — PRs #53/#75; daemon-time CAS, persisted expiry, stale generation fenced after restart |
| R-09 idempotent mutations | `afc-111`–`afc-113`, `afc-140` | Idempotency; Race 6 | **verified** — PRs #65/#72/#73/#74; ledger, create/claim/lifecycle replay, MCP ID forwarding, conflict and response-loss regressions |
| R-10 crash/recovery | `afc-114` | Crash/Recovery Analysis; SQLite correctness | **verified** — PR #75; process kill, WAL backup/restore, migration/integrity failure, restore-runbook proof |
| R-11 protocol and agent decisions | `afc-109`, `afc-116` | Protocol/API; Agent UX | **verified** — PRs #58/#77; child termination, historical replay/new-ID proof, short-TTL deadline, token-safe CLI, protocol/schema tests |
| R-12 audit/observability | `afc-115` | Auditability; Observability | **verified** — PR #76; durable rejected-attempt counts, renewal summaries, second-connection proof, health/stats/log fields, token exclusion |
| R-13 verification evidence | each behavior leaf, `afc-110`, `afc-114` | Six races; eight failure cases | **verified** — PRs #64/#75; deterministic multi-connection matrix, process-kill/restart/WAL matrix, per-leaf regressions, CI and scratch binaries |

For `afc-121`, invocation-mode behavior is sourced from `docs/specs/002-agent-protocol/requirements.md` through its canonical `docs/agent-protocol-v1.md` and the existing CLI/API contract.
For `afc-122`, the existing CLI JSON error envelope and exit-code contract is sourced from `docs/specs/002-agent-protocol/requirements.md` and `docs/agent-protocol-v1.md`.

## Closure rule

A row changes to `verified` only when its primary leaves are done, packet-local
review evidence names the relevant tests/commits, and no required scenario is
`UNSAFE` or `UNKNOWN`. A passing repository-wide test command without the
required focused tests is insufficient.
