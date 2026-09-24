# 015 Coordination Safety Traceability

Status values describe implementation, not specification completeness.

| Requirement | Primary leaves | Audit evidence | Status |
| --- | --- | --- | --- |
| R-01 authoritative restart state | `afc-114` | Current Architecture; Crash/Recovery Analysis | planned |
| R-02 one mutation authority | `afc-108` | Authoritative state; Write ownership; SQLite correctness | verified by corrected PR `#60`, automated two-process recovery, CI, and scratch installed black-box at `41d5517`; cooperative same-UID boundary remains explicit |
| R-03 atomic ready-qualified claim | `afc-106`, `afc-110` | Claim semantics; Race 1; Dependencies | ready-qualified claim implemented by `afc-106`; `afc-110` proves one winner, one typed loser, one generation, and one claim event across independent SQLite handles for 100 schedules |
| R-04 lease identity and fencing | `afc-103`, `afc-104`, `afc-105`, `afc-106` | Claim semantics; Races 2-4 | generation and lease-bound fencing verified through corrected PR `#62`; `afc-110` proves both heartbeat/reclaim and handoff/heartbeat serialization orders plus stale close after reclaim for 100 schedules each |
| R-05 heartbeat and release | `afc-104`, `afc-113` | Lease/TTL; Heartbeat; Race 2 | atomic unexpired lease CAS verified by PR `#53` and the `afc-110` multi-connection matrix; `afc-113` locally proves exact heartbeat expiry and release replay (owner review pending) |
| R-06 update/handoff/close | `afc-105`, `afc-113` | Handoff; Close; Races 3-5 | expiry-window and affected-row correction verified by PR `#62`; `afc-110` proves stale-close rejection, both handoff/heartbeat orders, and cancelled pre-write rollback; `afc-113` locally proves exact update, handoff, and close replay (owner review pending); process-kill proof remains `afc-114` |
| R-07 dependency/ready consistency | `afc-107`, `afc-110` | Dependencies / ready queue | afc-107 serialization and traversal behavior is verified across two production-initialized SQLite handles for 100 opposite-edge schedules by `afc-110` |
| R-08 lease time semantics | `afc-104`, `afc-114` | Lease/TTL; restart failure cases | daemon-time unexpired CAS implemented by `afc-104`; restart and wall-clock robustness proof remains `afc-114` |
| R-09 idempotent mutations | `afc-111`, `afc-112`, `afc-113`, `afc-140` | Idempotency; Race 6 | ledger and claim replay implemented by `afc-111`; create replay merged in `afc-112` PR #72; `afc-113` proves lifecycle replay and merged in PR #73; `afc-140` covers MCP operation ID propagation and raw NDJSON replay against the test daemon |
| R-10 crash/recovery | `afc-114` | Crash/Recovery Analysis; SQLite correctness | `afc-110` proves rollback after request cancellation between authorization and write; real process-kill, restart, WAL, migration, and restore proof remains `afc-114` |
| R-11 protocol and agent decisions | `afc-109`, `afc-116` | Protocol/API; Agent UX | fail-closed `issue run` ownership-loss behavior verified by corrected PR `#58`; broader protocol hardening remains `afc-116` |
| R-12 audit/observability | `afc-115` | Auditability; Observability | planned |
| R-13 verification evidence | each behavior leaf, then `afc-110`, `afc-114` | Six races; eight failure cases | pre-idempotency multi-connection matrix implemented by `afc-110`; black-box crash/restart matrix and final idempotent replay remain `afc-114` after `afc-111` through `afc-113` |

For `afc-121`, invocation-mode behavior is sourced from `docs/specs/002-agent-protocol/requirements.md` through its canonical `docs/agent-protocol-v1.md` and the existing CLI/API contract.
For `afc-122`, the existing CLI JSON error envelope and exit-code contract is sourced from `docs/specs/002-agent-protocol/requirements.md` and `docs/agent-protocol-v1.md`.

## Closure rule

A row changes to `verified` only when its primary leaves are done, packet-local
review evidence names the relevant tests/commits, and no required scenario is
`UNSAFE` or `UNKNOWN`. A passing repository-wide test command without the
required focused tests is insufficient.
