# 015 Coordination Safety Design

## 1. Boundary and trust model

The target remains a local single-node service:

```text
cooperative agents and afctl clients
             |
      HTTP+JSON / Unix socket
             |
       one af-coordinatord
             |
    serialized mutation boundary
             |
         SQLite WAL
```

SQLite is the canonical execution-state store. Git, SDD files, worker memory,
and CLI output can refer to that state but do not replace it. The daemon owns
all supported mutations. A lock tied to the database path plus safe Unix-socket
startup prevents two daemon processes from serving the same database.

This is a correctness boundary for cooperative local clients, not a security
sandbox for hostile same-UID code. Database, WAL, shared-memory, lock, socket,
and parent directories use restrictive modes, but a process with equivalent OS
credentials can still tamper with files. A factory that needs hostile-worker
isolation must give workers socket access without database filesystem access,
using separate users or a sandbox.

## 2. Real and target issue states

The persisted status enum remains:

```text
open <-> in_progress
  |          |
  +-> blocked <-+
  +-> deferred
  +-> done
  +-> cancelled

done/cancelled --operator-reopen--> open
```

`claimed`, `ready`, `expired`, and `handed_off` are not persisted issue
statuses:

- claimed means an `open` or `in_progress` issue has an active lease;
- ready is a query predicate;
- expired is a lease outcome recorded when observed/replaced;
- handoff is an atomic note plus release, returning the issue to `open` unless
  it is deliberately `blocked`.

Database CHECK constraints continue to restrict the status vocabulary. Service
methods remain responsible for authorization and legal transitions; tests must
prove the database cannot be driven into a state the API cannot interpret.

## 3. Lease record and generation

Each issue keeps an issue-local, monotonically increasing lease generation. A
successful fresh claim increments it once and stores the resulting value on the
lease row. A reattach or heartbeat never increments it.

The public claim result contains:

| Field | Purpose | Secret |
| --- | --- | --- |
| `lease_token` | capability authorizing this lease | yes |
| `lease_generation` | monotonic external fencing value | no |
| `attempt_id` | correlation across audit events | no |
| `expires_at` | daemon wall-clock deadline | no |
| `version` | optimistic issue revision after claim | no |

The token protects coordinator mutations. The generation lets an external
consumer reject late output when that consumer supports fencing. The
coordinator cannot retroactively fence a Git push or filesystem write performed
outside its boundary; factory integrations must propagate and check the
generation before publishing externally visible results.

Holder names remain attribution, not authentication. Therefore a caller that
only repeats a holder string must never receive an existing secret token. Safe
claim retry uses an operation ID. An explicit reattach, if retained, must prove
the current token and generation and is semantically a heartbeat.

## 4. Transactional claim

Claim runs on the serialized writer and performs one transaction:

1. resolve and read the issue;
2. reject epics and statuses outside the executable set;
3. evaluate unfinished `blocks` dependencies in the same snapshot;
4. inspect the current lease using daemon time;
5. if an active lease exists, return `lease_held` unless this is an exact
   idempotent replay;
6. if a persisted lease expired, append `lease_expired` and remove it;
7. increment the issue-local lease generation;
8. insert the new lease and attempt;
9. update status to `in_progress`, bump issue version, and set `claimed_at`;
10. append `issue_claimed` and the idempotency outcome;
11. commit.

Two racing claims may both begin, but only one may serialize the generation and
lease insertion. The loser returns a typed conflict/held result; it never
returns a usable token.

Ready-listing and claim share one eligibility definition. The list is advisory
and may be stale immediately; claim is the authoritative decision.

## 5. Fenced mutation predicates

Ownership is checked where the write occurs, not in a pre-transaction read.
The store uses conditional updates/deletes or an equivalent transaction-local
read/write sequence whose affected-row count is verified.

The common lease predicate is:

```text
issue_id = requested issue
AND lease_token = presented secret
AND lease_generation = presented generation
AND expires_at > daemon_now
```

`update` and `close` additionally require `issues.version = expected_version`.
Failure to affect exactly the intended row is mapped to a typed
`lease_expired`, `version_conflict`, or state error after a transaction-local
diagnostic read. No method reports success from `Exec` alone.

Operation-specific rules:

- heartbeat conditionally updates expiry for the current lease and returns the
  stored new deadline;
- release conditionally removes the current unexpired lease, changes issue
  status/version, and records the attempt outcome atomically;
- update performs version/lease CAS and its event in one transaction;
- handoff keeps note insertion, lease removal, issue transition, and ordered
  events in one transaction;
- close keeps version/lease validation, terminal transition, optional note,
  lease removal, resolution metadata, and event in one transaction.

If heartbeat and handoff race, either heartbeat serializes first and handoff may
then finish the still-current attempt, or handoff serializes first and heartbeat
fails. Neither ordering creates two owners.

## 6. Time and expiry

Clients provide TTL duration, never an absolute deadline. The daemon validates
the duration, samples its own UTC wall clock, and persists RFC 3339 UTC text.
Client clock skew is irrelevant.

Absolute expiration survives daemon restart. Expiry remains lazy: ready queries
ignore expired rows and the next claim transaction records `lease_expired`
before replacement. A background sweeper is optional only for timely metrics;
it must use the same transactional fencing and is not required for correctness.

A backward host-clock jump conservatively delays expiry, reducing liveness but
not granting a second owner. A forward jump can expire an attempt early; all
later writes from it fail the lease predicate. Generation prevents a reclaimed
attempt from being mistaken for the prior one.

## 7. Dependency serialization

`blocks` cycle detection and insertion use the same serialized writer
transaction. Traversal errors abort. Endpoint policy is checked inside that
transaction. The graph has no cached `ready` column: dependants become ready as
soon as their last blocker is terminal. Concurrent edge additions therefore
observe a serial order and cannot each validate against a graph that excludes
the other.

Non-blocking `parent`, `related`, and `discovered-from` edges do not affect
readiness, but their inserts/removals and audit events remain atomic.

## 8. One daemon and SQLite connections

Startup acquires an exclusive process lock derived from the canonical database
path before opening the listener or migrating. It holds the lock until clean
shutdown. Socket cleanup probes an existing socket and refuses to unlink a live
daemon; an unreachable stale socket is removed only while the database lock is
held.

Canonicalization resolves both parent-directory aliases and the final database
file symlink, including a dangling final symlink whose target will be created by
SQLite. Otherwise two path-derived lock files could protect the same database.
Default daemon-owned runtime directories are normalized to `0700` on upgrade;
custom configured parents remain operator-managed.

The store exposes a serialized write path. Every actual SQLite connection has
foreign keys enabled and a busy timeout; WAL mode and `synchronous=NORMAL` are
verified at startup. The implementation may use one write connection plus a
small read pool or initially one total connection, but it must not rely on
connection-local PRAGMAs having been executed on an arbitrary pooled
connection. Tests use the same connection initialization as production and
also exercise multiple clients/connections where the behavior requires it.

Startup checks migration state before serving. Migration files remain
transactional and append-only. Unknown or failed migrations stop startup.

## 9. Idempotency and ambiguous outcomes

Every mutation accepts an opaque client-generated `operation_id`. The daemon
records, in the mutation transaction:

- operation ID and operation kind;
- actor and target identity;
- a canonical request fingerprint;
- completion state and serialized public outcome;
- creation and retention timestamps.

The operation ID is unique within a documented namespace. A same-ID,
same-fingerprint retry returns the stored outcome, including the original
success after close/handoff has changed current state. A same-ID,
different-fingerprint request returns `idempotency_conflict`. Concurrent first
uses of the same ID serialize through a unique constraint.

Claim outcomes may contain a lease token and are sensitive. They remain in the
same protected database, are never emitted to events or logs, and follow an
explicit retention policy. Heartbeat replay returns the originally committed
expiry rather than extending the lease twice. New logical attempts always use a
new operation ID.

Until an endpoint adopts this contract, its documentation must say that an
ambiguous timeout requires a read/reconciliation step and that blind retry is
unsafe.

## 10. Crash and restart model

SQLite transaction commit is the mutation boundary:

- daemon death before commit leaves no mutation or audit fragment;
- daemon death after commit leaves the full state, event, and idempotency
  outcome;
- a retry after an ambiguous response resolves through the operation ledger;
- active leases reload from SQLite after restart and retain their deadlines;
- expired leases are reclaimable and fenced from later mutation;
- a worker that finished external work but died before close must reconcile its
  operation/evidence and may safely retry close with the same operation ID.

Backup and restore must include a SQLite-consistent snapshot, schema migration
ledger, and verification (`integrity_check`, expected revision/schema, and a
read-only coordinator query). Copying only the main DB file while WAL contains
uncheckpointed commits is not an accepted backup procedure.

## 11. Protocol hardening

HTTP supplies framing and handles partial Unix-socket reads/writes. The `/v1/`
path is the compatibility boundary. The daemon additionally sets bounded read,
write, idle, and header timeouts; caps request bodies; consistently rejects
unknown fields and trailing JSON; and returns one structured error envelope.

Mutation responses include the operation ID and enough ownership data for an
agent to decide its next action. Error documentation separates:

- safe retry with the same operation ID;
- reread/reconcile then decide;
- ownership permanently lost, stop work;
- validation/operator action required.

`afctl issue run` treats `lease_expired`, token/generation mismatch, or
confirmed replacement as ownership loss: it cancels the child, attempts a
bounded graceful termination of the isolated Unix process group, never closes,
and returns a distinct failure. Group-wide `SIGTERM` lets both a shell leader
and its current child observe cancellation; bounded `SIGKILL` cleanup prevents
a descendant that ignores termination from surviving the cancelled run.
Transient transport failures may be retried with the same heartbeat operation
ID only within the remaining known lease window; once ownership cannot be
proved, the child is stopped.

## 12. Audit and operations

The current append-only events remain an audit log; full event sourcing is not
introduced. Lease tokens are never recorded. The minimum additional evidence
is generation on lifecycle events, heartbeat count/last-heartbeat summary,
explicit expiry/reclaim, stale-mutation rejection counters/logs, and operation
IDs where safe.

Structured logs and local health/stats expose at least:

- mutation latency and result code by operation;
- claim conflicts;
- active and expired leases;
- heartbeat/lease-loss failures;
- SQLite busy/transaction/migration/integrity failures;
- daemon revision, singleton-lock state, and DB health.

No Prometheus dependency is required. Stable JSON health/stats plus structured
logs are sufficient for the local unattended-daemon maturity gate.

## 13. Deterministic proof matrix

The final harness uses controlled barriers, injected clocks, and crash points;
it does not rely on sleeps to make races likely.

| Scenario | Required result |
| --- | --- |
| Two workers claim one ready issue | exactly one valid token/generation; loser typed conflict |
| Old heartbeat vs expiry/reclaim | renewal wins before replacement, or replacement wins and heartbeat fails; no resurrection |
| Old close after reclaim | stale generation fails; new owner and state unchanged |
| Handoff vs heartbeat | serial outcome; no partial note/release and no dual owner |
| Daemon death between check and write | whole transaction committed or absent |
| Timeout after commit then retry | same operation ID returns original outcome; no duplicate event/effect |

Every leaf adds focused regressions. The matrix leaf proves interactions after
the component fixes land.

The table states the final packet outcome, while the dependency DAG proves it
progressively. `afc-110` owns the pre-idempotency multi-connection matrix:
races 1-4 must satisfy their final safety result, race 5 injects request
cancellation after authorization but before the write to prove rollback at the
transaction seam, and race 6 must characterize the current lost-response
ambiguity without duplicating state or audit effects. Real daemon termination
at the same boundary remains `afc-114`; original-outcome replay for the same
operation ID becomes testable only after `afc-111` through `afc-113`. The later
leaves replace those two bounded proofs with the final process-kill and
idempotent-replay outcomes rather than making `afc-110` depend on tasks it
unblocks.
