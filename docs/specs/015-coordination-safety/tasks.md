# 015 Coordination Safety Tasks

Coordinator IDs own live status. This file owns slice boundaries, ordering, and
acceptance. Epic `afc-102` groups implementation; `afc-101` is the planning gate
that must close only after this packet is merged.

## Priority and execution map

| Issue | SDD task | Pri | Stage | Factory | Blocked by |
| --- | --- | ---: | ---: | --- | --- |
| `afc-103` | AFC-SDD-0151 | P1 | 1 | manual | `afc-101` |
| `afc-104` | AFC-SDD-0152 | P1 | 1 | `exec/auto` | `afc-101`, `afc-103` |
| `afc-105` | AFC-SDD-0153 | P1 | 1 | `exec/auto` | `afc-101`, `afc-103` |
| `afc-106` | AFC-SDD-0154 | P1 | 1 | manual | `afc-101`, `afc-103` |
| `afc-107` | AFC-SDD-0155 | P1 | 1 | `exec/auto` | `afc-101` |
| `afc-108` | AFC-SDD-0156 | P1 | 1 | manual | `afc-101` |
| `afc-109` | AFC-SDD-0157 | P1 | 1 | `exec/auto` | `afc-101`, `afc-104` |
| `afc-110` | AFC-SDD-0158 | P1 | 2 | `exec/auto` | `afc-101`, `afc-104`-`afc-109` |
| `afc-111` | AFC-SDD-0159 | P1 | 3 | manual | `afc-101`, `afc-110` |
| `afc-112` | AFC-SDD-0160 | P1 | 3 | `exec/auto` | `afc-101`, `afc-111` |
| `afc-113` | AFC-SDD-0161 | P1 | 3 | `exec/auto` | `afc-101`, `afc-111` |
| `afc-140` | R-09 MCP transport | P1 | 3 | manual | `afc-112`, `afc-113` |
| `afc-114` | AFC-SDD-0162 | P1 | 3 | `exec/auto` | `afc-101`, `afc-110`, `afc-112`, `afc-113` |
| `afc-115` | AFC-SDD-0163 | P2 | 4 | `exec/auto` | `afc-101`, `afc-114` |
| `afc-116` | AFC-SDD-0164 | P2 | 4 | `exec/auto` | `afc-101`, `afc-114` |

`Factory` is routing metadata, not permission to bypass a claim, SDD, worktree,
or dependency. Manual leaves change cross-cutting contracts and require an
operator-driven architecture review before implementation.

### Discovered public-contract blocker: afc-120

`afc-120` repairs the MCP lease-generation contract before the later agent
protocol leaf `afc-116`. The `heartbeat_issue`, `handoff_issue`, and
`close_issue` schemas must require the claim generation; handoff and close must
forward it to the daemon. Both canonical and embedded CLI protocol examples
must show the required generation on heartbeat and release. Regression proof
uses real embedded migrations through a scratch daemon and rejects stale
generations. This mechanical correction is implemented and verified; evidence
is recorded in `review.md`.

`afc-121` repairs MCP propagation of the existing caller-declared invocation
mode on audit-producing lifecycle tools. Its schema, validation, and audit-event
regressions are verified against the contract referenced in `traceability.md`;
implementation evidence is in `review.md`.

`afc-122` hardens CLI argument parsing before the first daemon request. It
rejects unknown flags, missing values, malformed integers, invalid enumerated
values, and extra positionals across command families. Machine callers receive
the typed `validation_failed` envelope on stderr with empty stdout. The
existing `issue run -- <command>` child arguments remain opaque to dibs.
Verification evidence is in `review.md`.

## AFC-SDD-0151 / afc-103 — Add monotonic lease generation

**Problem.** The current token changes per fresh claim, but the public contract
has no monotonic fencing value that downstream publishers can compare. Issue
`version` is not a lease generation because unrelated metadata mutations also
increment it.

**Evidence.** `migrations/0001_schema_v1.sql:115-122` defines leases without a
generation. `internal/store/sqlite/issues.go:517-562` creates token and attempt
IDs and returns issue version only. `docs/api-v1.md:279-290` documents that
contract.

**Why now.** Heartbeat, update, close, and claim fixes must share one ownership
identity. Adding it once avoids incompatible per-operation fencing schemes and
unblocks three P1 leaves.

**Scope.** Add an issue-local generation counter and lease generation through a
real migration; backfill active leases safely; increment only on fresh claim;
expose generation in core/API/client/CLI JSON and `AF_LEASE_GENERATION`; include
it in lifecycle events without exposing the token; define the compatibility
behavior for active pre-upgrade leases.

**Out of scope.** Atomic fixes to heartbeat/update/claim; operation IDs;
external Git/filesystem fencing; network identity.

**Acceptance criteria.** A migration test upgrades a database with and without
an active lease. Fresh claims receive strictly increasing generations per
issue; reattach/heartbeat do not increment. Claim output and event payloads
carry the same generation, tokens never enter events/logs, and old database
state remains readable. `go test ./... -count=1` passes.

**Dependencies.** `afc-101` only. This is **START HERE**.

## AFC-SDD-0152 / afc-104 — Atomic heartbeat and release CAS

**Problem.** Heartbeat reads ownership/expiry and later updates without expiry
in the predicate or checking affected rows. Release finds a token but does not
require the lease to be unexpired. Either can race with replacement and produce
false success or mutate the wrong lifecycle state.

**Evidence.** `HeartbeatLease` in
`internal/store/sqlite/issues.go:565-601`; `ReleaseLease` at `604-660`.

**Why now.** Heartbeat is the liveness signal an unattended worker relies on.
If it lies, the worker can keep publishing after another worker owns the task.

**Scope.** Inject daemon time; use token+generation+expiry conditional writes;
verify exactly one affected row; keep release status/version/event atomic;
return typed ownership loss; add deterministic heartbeat-vs-reclaim and
expired-release regression tests with real migrations.

**Out of scope.** Claim eligibility; metadata update/close; client operation
deduplication; background expiry sweeper.

**Acceptance criteria.** In a forced heartbeat/reclaim interleaving, either
renewal commits and reclaim loses, or reclaim commits and the old heartbeat
returns `lease_expired`; the replacement expiry never changes. Expired release
fails without issue/event changes. Successful release has one status/version
transition and one attempt outcome. Tests do not depend on sleeps.

**Dependencies.** `afc-101`, `afc-103`.

**Status.** Implemented in `afc-104`: heartbeat and release authorize the
current unexpired token+generation with affected-row-checked transactional
writes and daemon-supplied UTC time. Deterministic renew-before-reclaim,
reclaim-before-stale-heartbeat, expired release, and single-transition
regressions are recorded in `review.md`.

## AFC-SDD-0153 / afc-105 — Fence update, handoff, and close

**Problem.** `UpdateIssue` checks issue version and lease before its transaction,
then executes `UPDATE ... WHERE id = ?`. A competing mutation can invalidate
both checks. Handoff and close already use transactions and active-token checks,
but must adopt generation and a uniform affected-row contract.

**Evidence.** `internal/store/sqlite/issues.go:803-975` performs the update
TOCTOU. Handoff is at `663-748`; close is at `1010-1096`.

**Why now.** This is the stale-owner write path: a worker that lost its lease
can overwrite or close work after a later claim unless every mutation is fenced
at commit.

**Scope.** Move update validation into its transaction; apply expected-version
CAS to leased and unleased updates; require token+generation+expiry for leased
updates, handoff, and close; verify affected rows; preserve atomic note/event
ordering; add stale-update and stale-close-after-reclaim tests.

**Out of scope.** Operator overrides; idempotent retries; dependency graph;
external side effects.

**Acceptance criteria.** A stale generation cannot update, handoff, or close
after reclaim. Two updates with one expected version yield one commit and one
typed conflict. Failed operations write no note/event/status fragment.
Existing valid handoff and close behavior remains covered. Tests use controlled
barriers and production migrations.

**Dependencies.** `afc-101`, `afc-103`.

**Status.** Implemented in `afc-105`: update, handoff, and close require the
active lease generation, update commits with expected-version CAS, and failed
handoff writes roll back atomically. Stale-generation and rollback regressions
are recorded in `review.md`.

**Reopened 2026-08-13.** `UpdateIssue` still read ownership through `GetIssue`
before starting its transaction. Because that read intentionally omits expired
leases, the former owner could perform a metadata update after expiry and
before reclaim. The correction must read even expired lease rows inside the
writer transaction, require token+generation+expiry there, verify affected rows
for update-release, handoff, and close, and add store/API plus same-version
concurrency regressions.

**Correction status.** Merged via PR `#62`; exact verification evidence is
recorded in `review.md`.

## AFC-SDD-0154 / afc-106 — Ready-qualified claim and safe reattach

**Problem.** Claim validates status and lease but not unfinished `blocks`
dependencies, so a caller can claim work that the ready view excludes. A caller
that merely repeats the current holder string receives the existing secret
token, although holder is not authenticated identity.

**Evidence.** The ready predicate is in
`internal/store/sqlite/issues.go:319-340`; claim uses only status/type at
`418-435` and holder-only reattach at `440-485`.

**Why now.** The queue is not authoritative if direct claim bypasses it, and
holder-only token recovery collapses attribution into authorization.

**Scope.** Share one eligibility predicate between ready and claim; evaluate it
inside claim transaction; reject unfinished blockers; make holder-only repeat
return `lease_held`; retain reattach only with proof of token+generation or
replace it with idempotent replay once available; cover two-worker claim and
blocked-direct-claim races.

**Out of scope.** Operation ledger implementation; dependency cycle insertion;
remote authentication; scheduling fairness.

**Acceptance criteria.** Two distinct concurrent claimers get exactly one
usable lease. A blocked issue cannot be claimed directly. Repeating a holder
name alone never returns a token. Existing exact ownership proof may renew
without a new generation. Ready may be stale, but claim always makes the final
decision.

**Dependencies.** `afc-101`, `afc-103`.

## AFC-SDD-0155 / afc-107 — Serialize dependency graph mutations

**Problem.** Cycle detection runs before the insert transaction, and traversal
errors are swallowed. Concurrent edges can each validate against an incomplete
graph and commit a cycle.

**Evidence.** `AddDependency` checks at
`internal/store/sqlite/issues.go:1405-1429` and starts the transaction only at
`1433`; `wouldCreateCycle` ignores query/scan failures at `1525-1556`.

**Why now.** Ready correctness depends on a valid graph. A cycle can park
factory work indefinitely and current diagnostics may claim no cycle after a DB
error.

**Scope.** Resolve endpoints, traverse `blocks`, insert edge, and append event
on the serialized writer in one transaction; return traversal errors; define
and test cross-project endpoint policy; add deterministic simultaneous
opposite-edge test and ready-after-blocker-close test.

**Out of scope.** General graph analytics; cached ready state; parent/related
semantics changes; UI.

**Acceptance criteria.** Concurrent `A blocks B` and `B blocks A` produce at
most one committed edge and one `dependency_cycle`. Injected traversal failure
aborts without an edge/event. Closing the final blocker makes the dependant
ready on the next query.

**Dependencies.** `afc-101`.

**Status.** Implemented in `afc-107`: add-dependency resolves endpoints,
enforces the cross-project endpoint policy, traverses `blocks`, inserts the
edge, and appends the event in one serialized-writer transaction; traversal
errors are returned. Same-change regression tests and verification evidence
are recorded in `review.md`.

## AFC-SDD-0156 / afc-108 — One daemon writer and SQLite contract

**Problem.** Startup unlinks any existing socket without probing it, so a second
daemon can orphan the first listener and access the same DB. PRAGMAs are
executed through a pooled `sql.DB`; connection-local settings are not guaranteed
for every later connection. The design promises a dedicated writer and
`synchronous=NORMAL`, but code does not establish them.

**Evidence.** `internal/api/daemon.go:154-173`,
`internal/store/sqlite/sqlite.go:15-34`, and
`docs/architecture-v1.md:44-55`. Store/API test helpers force
`SetMaxOpenConns(1)` (`internal/store/sqlite/issues_test.go:810`,
`internal/api/api_test.go:97`), hiding production pooling behavior.

**Why now.** Atomic functions do not help if two coordination processes or
misconfigured connections violate their serialization assumptions.

**Scope.** Hold an exclusive DB-path process lock; probe before stale socket
removal; configure/verify WAL, foreign keys, busy timeout, and synchronous mode
for every connection; establish the documented serialized writer; set
restrictive runtime modes; add two-daemon and multi-connection tests.

**Out of scope.** Multi-node HA; replacing SQLite; hostile same-UID protection;
network listeners; performance tuning beyond correctness.

**Acceptance criteria.** A second daemon for the same DB exits without unlinking
the live socket. After abrupt first-daemon death, one replacement starts and
recovers. Every test connection reports required PRAGMAs; foreign-key violation
fails on each. Concurrent clients receive typed busy/conflict handling rather
than corrupt/partial state. Architecture docs and actual connection model agree.

**Dependencies.** `afc-101`.

**Reopened 2026-08-13.** Quality review reproduced two daemons serving one
SQLite database when the second path was a symlink of the database file itself;
the original canonical-path test covered only a symlinked parent directory.
The follow-up must cover existing and dangling DB-file symlinks with a real
two-process regression, retain abrupt-death replacement proof, and normalize
existing default runtime-directory modes without chmodding custom parents.

## AFC-SDD-0157 / afc-109 — Stop `issue run` on lease loss

**Problem.** The background heartbeat prints an error and continues running the
child. The child can finish and publish after ownership has moved.

**Evidence.** `cmd/afctl/cmd_issue_run.go:127-146` logs heartbeat failures;
the child continues at `148-165`.

**Why now.** `issue run` is the safest advertised launcher and likely factory
entrypoint. Continuing after confirmed ownership loss defeats lease fencing at
the agent boundary.

**Scope.** Classify heartbeat errors; cancel and gracefully terminate the child
on confirmed lease loss; bound transient retries within known expiry; prevent
close after lost ownership; return a distinct actionable error; add subprocess
tests for loss, transient transport failure, and clean shutdown.

**Out of scope.** Supervising arbitrary distributed jobs; killing external
process trees outside the launched command; operation ledger implementation.

**Acceptance criteria.** A forced replacement causes the child to receive
termination, no close request is sent, and CLI exits non-zero with ownership
lost. A single retryable transport error before the deadline does not kill a
still-owned job. No heartbeat goroutine survives command exit.

**Dependencies.** `afc-101`, `afc-104`.

**Reopened 2026-08-13.** The focused regression fails on the merged
implementation: `exec.CommandContext` sends `SIGTERM` only to the shell leader,
so its active `sleep` child retains the pipes and the shell never runs its TERM
trap before `WaitDelay` kills the leader. The correction must isolate and
terminate the launched process group, with bounded cleanup of descendants.

## AFC-SDD-0158 / afc-110 — Six-race concurrency proof

**Problem.** The existing suite has useful lifecycle tests, but shared helpers
use one SQLite connection and there is no deterministic cross-operation proof
for the six mandatory races.

**Evidence.** `internal/api/api_test.go:97` and
`internal/store/sqlite/issues_test.go:810` set one connection. Existing focused
tests include claim reattach (`issues_test.go:3956+`), handoff rollback
(`2485+`), and close authorization (`1673+`), not the production interleavings.

**Why now.** Component fixes can look green while their composition remains
unsafe. This is the gate from code plausibility to concurrent-agent trust.

**Scope.** Build reusable barriers/failpoints and real multi-connection
fixtures; exercise all six races from `design.md`; assert state, lease,
generation, event count/order, and error code; run repeatedly and under
`-race`; document which serialization point won. At this pre-idempotency stage,
race 5 proves rollback through request cancellation at the transaction seam and
race 6 characterizes the committed-response-loss gap without duplicating the
effect. Process-kill proof remains `afc-114`; stored original-outcome replay
remains `afc-111` through `afc-113`.

**Out of scope.** Load benchmarking; randomized chaos as the only evidence;
multi-host tests; fixes unrelated to a failing required scenario.

**Acceptance criteria.** The six scenarios deterministically execute for at
least 100 repeated schedules without sleeps or flakes. Races 1-4 assert no
dual owner or partial audit state; race 5 leaves the authorized-but-cancelled
transaction wholly absent; race 6 commits one close/note/event sequence and
returns a deterministic typed conflict on replay, explicitly leaving R-09 open.
The afc-107 opposite-edge invariant is also repeated through independent
production-initialized SQLite handles. `go test -race ./... -count=1` and the
repeated `make test-concurrency` target pass.

**Dependencies.** `afc-101`, `afc-104`, `afc-105`, `afc-106`, `afc-107`,
`afc-108`, `afc-109`.

**Status.** Implemented in `afc-110`: `TestCoordinationRaceMatrix` exercises
the six pre-idempotency scenarios for 100 schedules each, and
`TestMultiConnectionDependencyCycleSerialization` repeats the afc-107 graph
invariant across two production-initialized SQLite handles. Final verification
and merge evidence are recorded in `review.md`.

## AFC-SDD-0159 / afc-111 — Durable mutation idempotency ledger

**Problem.** Mutating requests have no operation ID or stored outcome. The
client has a five-second timeout, so a committed response lost in transit is
indistinguishable from an uncommitted request.

**Evidence.** No `request_id`/`operation_id` exists in `internal/core`, API, or
migrations. `internal/client/client.go:47-52` sets the timeout and
`606-646` returns transport failure without reconciliation.

**Why now.** Crash-safe transactions still leave clients unable to decide
whether retry duplicates a create, extends a heartbeat twice, or attempts a
second close.

**Scope.** Specify `operation_id` and `idempotency_conflict`; add the migration
and store abstraction for request fingerprint plus stored outcome; guarantee
same-transaction record/side effect; protect sensitive claim outcomes; define
retention and compatibility for clients that omit IDs; add concurrent duplicate
and mismatched-payload tests using a representative mutation.

**Out of scope.** Adopting every endpoint; automatic client retries; distributed
deduplication across coordinator databases; external tracker keys.

**Acceptance criteria.** Two concurrent identical requests with one operation
ID execute one representative mutation and return the same outcome. Reuse with
a different fingerprint returns typed conflict. A crash before/after commit
leaves neither record/effect or both. Tokens are absent from events/logs and
retention behavior is documented.

**Dependencies.** `afc-101`, `afc-110`.

**Status.** Done. The ledger, its migration (`0009_operation_ledger.sql`), the
`operation_id` contract, and `idempotency_conflict` are implemented, with
**claim** adopted as the representative mutation this task calls for.
Verification: `internal/store/sqlite/operations_test.go` (replay, fail-closed
conflict binding, concurrent duplicate and conflicting operations across
independent connections at 100 schedules each, restart durability, token and
operation-ID secrecy), `internal/api/api_test.go` (HTTP replay, `lease_held`
for a new operation ID, no ledger exposure through issue reads), and
`cmd/afctl/operation_journal_test.go` (client-side key persistence).

Adoption of the remaining endpoints stays where the packet put it: create is
`afc-112`, and heartbeat/release/update/handoff/close are `afc-113`. Those
tasks now inherit a working ledger rather than needing to build one.

## AFC-SDD-0160 / afc-112 — Retry-safe create and claim

**Problem.** Retrying create allocates a new short ID and issue. Claim's
holder-only reattach is an unsafe substitute for request deduplication.

**Evidence.** `CreateIssue` allocates UUID/sequence and inserts on every call at
`internal/store/sqlite/issues.go:18-131`; claim's current repeat path is
`440-485`.

**Why now.** These are the first operations in every agent lifecycle; duplicate
tasks or ambiguous ownership contaminate all downstream evidence.

**Scope.** Require/propagate operation IDs in API/client/CLI for create and
claim; persist exact committed outcomes; return the original short ID or lease
on retry; remove holder-only recovery; expose CLI guidance and JSON; test
timeout-after-commit, concurrent retry, retry after later state change, and
payload mismatch.

**Remaining after `afc-111`.** The claim half is delivered: `afc-111` adopted
claim as its representative mutation, and holder-only recovery was already
removed by AFC-SDD-0154. What is left here is **create**: operation IDs on
`CreateIssue` so a retried create returns the original issue and short ID
instead of allocating a second one.

**Out of scope.** Importer-level natural keys; deduplicating deliberately
distinct tasks with identical titles; lifecycle mutations after claim.

**Acceptance criteria.** Same create operation yields one issue/event/sequence
increment. Same claim operation yields one attempt/generation/event and the
same response even after the original response is lost. A new operation ID
creates a new logical action. No caller can recover a token from holder name.

**Dependencies.** `afc-101`, `afc-111`.

**Implementation status.** The remaining create slice was merged in `afc-112`
via PR #72 (`9ac2eec`). Store,
HTTP, CLI, and production-migration concurrency evidence is in `review.md`.

## AFC-SDD-0161 / afc-113 — Retry-safe lease lifecycle mutations

**Problem.** Heartbeat changes expiry on each retry; successful release,
handoff, or close retried after a lost response sees later state and reports an
error; update can apply a second version bump if reconstructed with fresh
state.

**Evidence.** The mutation request structs and endpoints documented in
`docs/api-v1.md:243-302` have no operation ID; current store methods reason only
from current state.

**Why now.** After atomic fencing, ambiguous outcomes are the remaining common
way an autonomous client can make the wrong lifecycle decision.

**Scope.** Adopt the operation ledger for heartbeat, release, update, handoff,
and close; return original outcomes on exact retry; ensure heartbeat expiry,
note IDs, version, and close metadata replay exactly; add timeout-after-commit
tests per operation and cross-operation operation-ID conflict tests.

**Out of scope.** Operator bulk commands; automatic retry policy; create/claim;
external side-effect deduplication.

**Acceptance criteria.** Each operation can lose its response and be retried
without another expiry extension, version bump, note, event, release, or close.
Same ID with changed payload fails. Different operation IDs continue to enforce
current lease/version/state normally.

**Dependencies.** `afc-101`, `afc-111`.

**Implementation status.** Implemented in the `afc-113` worktree for owner
review. Store, HTTP, and explicit CLI `--operation-id` paths accept the same
ledger identity; the verification ledger is in `review.md`.

## afc-140 — MCP operation ID propagation

`afc-140` carries R-09 operation IDs through the MCP create, claim, heartbeat,
release, update, handoff, and close tools. It exposes caller-provided IDs or
returns a generated ID and verifies replay against the production daemon API.

## AFC-SDD-0162 / afc-114 — Crash, restart, migration, and restore proof

**Problem.** Unit transaction tests do not prove kill/restart behavior, WAL
backup correctness, migration failure policy, or reconciliation after a worker
finishes external work before close.

**Evidence.** `internal/store/sqlite/sqlite.go:37-87` applies transactional
migrations, while `docs/operations.md` documents backup operations. No current
test matrix kills the daemon around each lifecycle commit or restores a live
WAL snapshot.

**Why now.** This is the gate from concurrent correctness to unattended durable
coordination.

**Scope.** Black-box daemon tests with scratch DB/socket and controlled crash
points; cover the eight failure cases in `audit.md`; restart with active and
expired leases; host-reboot equivalent process kill plus reopen; WAL-consistent
backup/restore; migration failure and integrity-check behavior; operation-ID
reconciliation after ambiguous commits.

**Out of scope.** Hardware power-loss certification; replication; automated
off-host backup service; repairing arbitrary corruption in place.

**Acceptance criteria.** Every case has a documented expected state and an
automated assertion after restart. No partial lifecycle/audit/idempotency state
appears. A consistent backup restores all committed events and schema version;
an intentionally invalid backup/migration fails closed with an actionable
diagnostic.

**Dependencies.** `afc-101`, `afc-110`, `afc-112`, `afc-113`.

## AFC-SDD-0163 / afc-115 — Minimum safety telemetry and audit closure

**Problem.** Health is primarily DB ping/revision, heartbeat renewals append no
event, and operators cannot directly see claim conflicts, expirations,
transaction failures, active leases, or mutation latency.

**Evidence.** `internal/api/daemon.go:49-65` implements health; heartbeat at
`internal/store/sqlite/issues.go:565-601` writes no event. Existing stats are
execution analytics, not a daemon safety view.

**Why now.** Once behavior is correct and durable, unattended operation needs a
small signal set to detect degradation without reading SQLite directly.

**Scope.** Add structured operation/result/latency logs; local counters or
derived stats for claim conflict, lease expiry/loss, active leases, DB busy and
transaction failure; health details for singleton lock, migration/schema, and
DB integrity policy; audit generation and a bounded heartbeat summary without
token leakage.

**Out of scope.** Prometheus dependency; tracing backend; dashboards/alerts;
agent ranking; full event sourcing.

**Acceptance criteria.** A local operator can distinguish healthy idle,
contention, expired-owner, DB-busy, and migration/integrity failure states from
documented JSON/log fields. Tests assert stable field names and absence of lease
tokens/secrets. Metrics do not require another durable rollup database.

**Dependencies.** `afc-101`, `afc-114`.

## AFC-SDD-0164 / afc-116 — Agent lease-loss and retry protocol

**Problem.** Current agent docs describe exit codes and heartbeat cadence but
cannot tell a client how to reconcile an ambiguous commit or use lease
generation. A language model may blindly retry or continue after ownership
loss.

**Evidence.** `docs/agent-protocol-v1.md:29-126` documents token/version and
launcher behavior; `246-258` maps errors but has no operation-ID decision tree.

**Why now.** Correct primitives are not usable by agents until the safe action
for each response/timeout is explicit and machine-readable.

**Scope.** Update API, protocol, curl examples, CLI help, and MCP schema for
generation/operation IDs; add a compact decision table for success, conflict,
ownership loss, timeout-before-known-expiry, timeout-after-expiry, and daemon
restart; document heartbeat cadence and external publication fencing; add doc
contract tests where output is generated.

**Out of scope.** New transport; SDKs for other languages; tutorials/UI;
automatic orchestration policy inside the daemon.

**Acceptance criteria.** An agent can determine from one documented table when
to retry with the same operation ID, reread, reclaim, stop the child, handoff,
or ask an operator. Examples never suggest blind retry with a new ID. Public
schemas and installed CLI output agree.

**Dependencies.** `afc-101`, `afc-114`.

## START HERE

Start with `afc-103` / AFC-SDD-0151. It establishes the common ownership
identity consumed by claim, heartbeat, update, handoff, and close and unlocks
three P1 fencing leaves. Starting separately in those functions would create
incompatible predicates and repeated migrations. `afc-107` and `afc-108` are
independent workstreams, but neither replaces the ownership contract needed to
close the highest stale-owner risk.
