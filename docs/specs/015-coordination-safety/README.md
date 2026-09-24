# 015 Coordination Safety

Status: implementation complete; epic closure evidence and remaining limits are
in `review.md` and `traceability.md`.

Planning issue: `afc-101`. Implementation epic: `afc-102`.

This packet turns the 2026-08-11 coordination audit into an executable safety
program. The audit is preserved in `audit.md`; `requirements.md` and
`design.md` define the target contract; `tasks.md` is the canonical slice map.

## Decision

At the audited 2026-08-11 revision, `af-coordinator` was useful as a local
execution ledger for one agent or carefully sequenced agents. It was not yet a
trustworthy coordination authority for multiple autonomous agents working
concurrently.
The blocking defects were atomic lease fencing, enforcement of the single-writer
model, retry safety after ambiguous outcomes, and production-like failure
tests. The implementation and current maturity assessment are in `review.md`;
SQLite remains the storage engine.

## Delivery order

```text
lease identity and atomic mutation fencing
    -> single-daemon and dependency/ready correctness
    -> six-race concurrency proof
    -> idempotent retry and crash/restart proof
    -> minimum audit, health, and agent decision protocol
```

The order is a dependency graph, not a preference list. Network transport,
external tracker synchronization, bulk operator UX, and cosmetic work stay
behind the safety epic.

## Factory routing

Concurrent factory use remains disabled by the owner decision recorded in
`review.md`; routing tags do not authorize that separate rollout.

Some bounded leaves carry the live tag `exec/auto`. A factory may select them
only through `afctl issue ready --project afc --tag exec/auto`; it must not
ignore `blocks` dependencies or infer readiness from this document. Leaves that
change the lease contract, idempotency schema, or daemon authority boundary are
intentionally not tagged for autonomous execution.

Every implementation leaf must:

- start from the packet-local requirement and design references in `tasks.md`;
- include regression tests in the same change;
- use the real embedded migrations for store tests;
- update `tasks.md`, `traceability.md`, and `review.md` when it is actually
  complete;
- verify the installed daemon/CLI when behavior changes.

## Completion gate

Packet 015 is complete only when all required leaves under epic `afc-102` are
terminal, the six concurrency races and crash matrix pass against real
multi-connection SQLite, and `review.md` records the resulting limitations.
Passing the existing unit suite alone does not close this packet.
