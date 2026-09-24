# 015 Coordination Safety Review

## Status

Packet 015 implementation complete; epic `afc-102` closure evidence follows.

## Epic closure / afc-102 — 2026-09-24

Coordinator status and the merge DAG were checked for every required leaf:
`afc-103` through `afc-116`, plus discovered contract leaves `afc-120`,
`afc-121`, `afc-122`, and `afc-140`, are `done`, and each recorded commit SHA
is an ancestor of `main` at PR #77 merge `8dfc05a`. The six-race matrix in
`TestCoordinationRaceMatrix` uses separate production-initialized SQLite
connections and controlled schedules; the eight failure cases are mapped to
process-kill/restart and restore tests in the afc-114 section below. The
2026-08-11 `audit.md` remains the immutable baseline, not the current verdict.
For the packet's tested local, cooperative topology, all six required race
outcomes now have a safe result and all eight failure cases have explicit
evidence; none remains `UNSAFE` or `UNKNOWN` under that test model.

| Leaf(s) | Final PR | Recorded commit |
| --- | --- | --- |
| `afc-103` | #47 | `78a6489` |
| `afc-104`, `afc-105` | #62 | `6620f0c` |
| `afc-106` | #49 | `2a83437` |
| `afc-107` | #54 | `bb3fef9` |
| `afc-108` | #60 | `41d5517` |
| `afc-109` | #58 | `fcc768e` |
| `afc-110` | #64 | `01d7052` |
| `afc-111` | #65 | `60b3164` |
| `afc-112` | #72 | `9ac2eec` |
| `afc-113` | #73 | `463c763` |
| `afc-114` | #75 | `0e81bcb` |
| `afc-115` | #76 | `c10884d` |
| `afc-116` | #77 | `8dfc05a` |
| `afc-120`, `afc-121`, `afc-122`, `afc-140` | #69, #70, #71, #74 | `a6f8298`, `9871fad`, `9f13ed2`, `c339996` |

`afc-104` also shipped its original heartbeat/release slice in #53, and
`afc-106` its original ready-qualified claim in #48; the table names the
later correction PR recorded on each issue.

| Requirement | Completion evidence |
| --- | --- |
| R-01 authoritative restart state | PR #75: `TestRestartRetainsActiveLeaseAndFencesExpiredWorker` and `TestCrashAfterCommitReplaysOriginalOutcome` reopen the same file-backed SQLite state after daemon death. |
| R-02 one mutation authority | PR #60: singleton lock, per-connection SQLite settings, restrictive runtime modes, and two-process recovery proof; same-UID cooperation remains the trust boundary. |
| R-03 ready-qualified atomic claim | PRs #49/#64: blocked direct claim returns `issue_not_ready`; `TestCoordinationRaceMatrix` proves one winner and one claim event across two SQLite handles. |
| R-04 lease identity and fencing | PRs #47/#62/#64: generation, token, expiry, and affected-row fencing; race matrix proves stale close and heartbeat cannot act after reclaim. |
| R-05 heartbeat and release | PRs #53/#73: unexpired lease CAS and exact operation-ID replay for heartbeat/release, including original historical expiry. |
| R-06 update, handoff, close | PRs #62/#73/#75: atomic lease/version checks, exact replay, and before/after-commit daemon-kill tests with no partial state. |
| R-07 dependency and ready view | PRs #54/#64: serialized edge/cycle checks over separate SQLite handles; ready view follows blocker close without a mutable cache. |
| R-08 lease time | PRs #53/#75: daemon-time expiry and restart proof for active and expired leases, with stale generation fenced. |
| R-09 idempotent mutations | PRs #65/#72/#73/#74: durable operation ledger, create/claim/lifecycle replay, changed-payload conflict, and MCP operation-ID propagation. |
| R-10 crash and recovery | PR #75: `TestCrashBeforeCommitHasNoPartialState`, `TestCrashAfterCommitReplaysOriginalOutcome`, `TestLiveWALBackupRestoresEventsAndMigrationLedger`, startup integrity/migration failures, and the exact restore runbook check. |
| R-11 protocol decisions | PRs #58/#77: `issue run` stops the child on lease loss, proves fresh liveness after historical heartbeat replay, and fences short TTLs; canonical/embedded protocol, API, and MCP decision contracts agree. |
| R-12 audit and observability | PR #76: `TestSafetyAuditSurvivesSecondConnectionWithoutSecrets` and `TestDaemonSafetyFieldsAndMutationLogs` prove durable rejection counts, bounded renewal summaries, stable health/stats/log fields, and no token leakage. |
| R-13 verification | PRs #64/#75: six deterministic multi-connection races and eight crash/restart cases, plus per-leaf regressions, race-enabled suites, and scratch installed-binary checks recorded below. |

The updated maturity assessment is: local concurrent cooperative agents and
restart-safe coordination (audit levels 3–4) are supported by these tests;
the minimum local unattended-daemon signals (level 5) are present, but this is
not factory rollout approval. After PR #77 merged, `dibs doctor` reported all
six checks OK, including daemon revision equal to `main` HEAD. Limits
remain explicit: the same-UID filesystem boundary does not isolate hostile
processes; Git/file/external effects require caller reconciliation and
downstream generation fencing where available; abrupt host power loss was
approximated by process kill and WAL reopen, not hardware certification.
Operation-ledger and rejected-attempt rows have documented retention windows
but no automatic pruning in this packet. The packet does not enable a remote
or multi-host coordination service.

Owner decision (Aleksey Bevz, 2026-09-24): concurrent factory use stays
**DISABLED** at epic closure. The current 90-day plan allocates zero Aion
hours; aion-forge still uses legacy `afctl`/`AF_*` names (`aion-924` open);
and factory-level concurrency has not been proven by coordinator safety tests.
Enabling it requires a separate owner-approved rollout after `aion-924`, with
a bounded concurrency trial and rollback. No rollout issue is created here.

The sections below record evidence at each leaf's implementation time; their
historical "pending" wording does not override the closure table above.

## AFC-SDD-0164 / afc-116 — agent lease-loss and retry protocol

The canonical `docs/agent-protocol-v1.md` and byte-identical embedded
`dibs protocol` now give one action table for success, contention/conflict,
ownership loss, timeout before/after the known lease deadline, and daemon
restart. `docs/api-v1.md` adds the same API decisions and a private-file curl
example; MCP tool descriptions explain the operation-ID contract. The CLI is
the primary shell-capable agent path, with MCP retained for shell-less clients.
External publication requires fresh ownership proof and downstream generation
fencing where available. This follows R-11 and the earlier
`docs/specs/002-agent-protocol/requirements.md` contract.

Owner replay decision: same-ID heartbeat replay returns its original,
historical expiry even after replacement. `issue run` therefore keeps one
operation ID across an ambiguous retry, then requires a new-ID heartbeat
before treating the lease as live. It terminates the child if fresh proof
fails. The heartbeat cadence stays below even a short TTL, each request is
bounded by the last known deadline, and an invalid claim deadline prevents
the child from starting. No `replayed` response marker was added: the caller
already knows whether it reused an ID, so the optional marker is left for a
follow-up.
Owner token decision: agents never pass lease tokens in argv. `issue run`
passes the token to its child in the environment; manual lifecycle commands
accept `DIBS_LEASE_TOKEN` or a private `DIBS_LEASE_TOKEN_FILE`, with the
legacy `AF_LEASE_TOKEN` alias when the canonical variable is unset.

`TestIssueRunRetriesTransientHeartbeatFailure` proves exact-ID retry followed
by new-ID liveness proof; `TestIssueRunTreatsReplayedHeartbeatExpiryAsHistorical`
proves the child stops when that new request loses the lease, even after a
successful historical replay. `TestIssueRunShortTTLStopsBeforeExpiredChildContinues`
guards a two-second TTL against the old five-second cadence.
`TestIssueRunRejectsUnknownClaimDeadlineBeforeStartingChild` checks the
fail-closed path for a malformed claim deadline.
`TestIssueHeartbeatReadsPrivateTokenFileWithoutArgv`
executes a built CLI against the mock daemon and checks the token reached the
request without appearing in argv or output. `TestLifecycleTokenSourcesAvoidArgv`,
`TestEmbeddedProtocolPublishesRetryAndTokenRules`, and
`TestHeartbeatSchemaExplainsHistoricalReplay` cover source precedence,
embedded help, and MCP schema. `make build`, `make test` (race), `make vet`,
and `GOTOOLCHAIN=go1.26.4 make lint` passed; logs are
`/tmp/afc-116-final-{build,test,vet,lint}.log`. An installed-binary smoke under a
temporary HOME, DB, and socket used `make build-install BINDIR=<temp>/bin`,
started scratch `dibsd`, ran a three-second child under a two-second TTL and
confirmed `issue run` renewed and closed it, then created, claimed,
heartbeated with `DIBS_LEASE_TOKEN_FILE`, and closed another issue through
installed `dibs`. The token did not enter argv. PR CI and independent
final-content review gate the merge; the owner's service and database were
untouched.

## AFC-SDD-0163 / afc-115 — safety telemetry and audit closure

Heartbeat renewals now increment a bounded per-lease count and last-heartbeat
timestamp in the same transaction as the lease CAS. Release, handoff, close,
operator release/close, and expiry/reclaim events carry that summary and the
lease generation; exact operation-ID replay does not increment it again.
Successful claims remain in issue events. Rejected claims and stale lifecycle
mutations leave issue events unchanged and upsert a durable counter in a
separate short transaction after the rejection rolls back. Each row is keyed
by issue, kind, holder, and reason, with first/last times, last presented and
current generations, and last declared invocation mode. No lease token or
operation ID is stored. Lifecycle holder is resolved from the claim event for
the presented generation, including after reclaim. Counter failure is logged and does not replace the
original API error. Operator close/reopen/release stale-version rejections use
the same durable counter path. Rows for issues closed over 30 days ago are eligible for
future pruning; this slice has no automatic reaper.

`/v1/health` reports the held singleton lock, startup migration/integrity
verification policy, latest migration, active/expired leases, durable claim
conflicts and stale rejections, and process-local mutation counters. `/v1/stats`
adds a read-only `safety` snapshot including at most ten top stale-holder rows.
Mutation logs on stderr contain operation, result code, HTTP status, and
latency in milliseconds. `db_busy` and generic transaction failures have
distinct local result codes; the public `internal_error` envelope is unchanged.
The result-code classifier is shared across issue and non-issue mutation
handlers, including project/repository/artifact/worktree writes.
Startup migration/integrity failures continue to fail closed with diagnostic
logs rather than serving a degraded daemon.

Verification: `TestSafetyAuditSurvivesSecondConnectionWithoutSecrets` uses
real embedded migrations and a second file-backed SQLite handle to prove
durable counts, replay-once heartbeat summary, no rejected issue event, and no
secret in the snapshot. `TestExpiryEventCarriesHeartbeatSummary` checks an
expiry/reclaim event. `TestStaleLifecycleRejectionsDoNotAppendEvents` checks
release, handoff, update, and close with replaced ownership. `TestDaemonSafetyFieldsAndMutationLogs` exercises the
real Unix-socket daemon, health/stats JSON, claim conflict, and token-free
logs. `TestOperatorVersionRejectionsHaveDurableCountsWithoutEvents` covers
operator close/reopen/release conflicts without issue-event changes.
`TestBusyMutationGetsStableTelemetryCode` holds a separate SQLite write
lock and checks issue and project DB-busy result headers, logs, and counters. `make build`,
`make test` (race), `make vet`, and `GOTOOLCHAIN=go1.26.4 make lint` passed;
logs are `/tmp/afc-115-{build,test,vet,lint}.log`. With temporary HOME, DB,
socket, and `make build-install BINDIR=<temp>/bin`, installed `dibsd` served
health `ok` with one active lease and migration `0011_rejection_counts.sql`;
installed `afctl` read the same issue. The owner's service and database were
untouched. PR CI and independent final-content review gate the merge.

## AFC-SDD-0162 / afc-114 — crash, restart, migration, and restore proof

The subprocess harness starts the production `api.RunDaemon` over a scratch
Unix socket with `sqlite.Open` and the embedded migrations. A store wrapper
only pauses test requests at the existing transaction proof points or after a
committed store call; the parent sends SIGKILL and starts a fresh process on
the same file-backed SQLite DB. It checks issue, lease, event, and operation
ledger state after restart. No production crash switch or live DB access was
added. Polling waits for an explicit crash-point marker, not for a likely
timing race. A separate short-lived worker subprocess claims an issue and
exits, or writes an external-work marker and exits, for the two worker-death
cases.

| Audit failure | Expected state and automated assertion |
| --- | --- |
| 1. Worker dies immediately after claim | `TestRestartRetainsActiveLeaseAndFencesExpiredWorker`: same active attempt, generation, and absolute expiry after kill/restart; current token can renew. |
| 2. Worker dies after external work before close | `TestWorkerDeathBeforeCloseRequiresReconciliation`: external marker survives, coordinator remains nonterminal with one lease and no close event; explicit reconciled close replays once. External side effects still require caller reconciliation. |
| 3. Daemon dies during claim | `TestCrashBeforeCommitHasNoPartialState` rolls back lease/status/events/ledger; `TestCrashAfterCommitReplaysOriginalOutcome` returns the committed token, generation, and attempt with one claim event. |
| 4. Daemon dies during heartbeat | The same before/after tests prove original expiry or one committed new expiry, one ledger outcome, and no second renewal on retry. |
| 5. Daemon dies during handoff | The before/after tests prove no partial note/release and one original handoff outcome on replay. |
| 6. Restart with active leases | The active lease test checks persisted ownership and renewal on the new daemon process. |
| 7. Host reboot equivalent | SIGKILL plus new process/SQLite handle reopens WAL state; `TestLiveWALBackupRestoresEventsAndMigrationLedger` snapshots an active WAL with `VACUUM INTO`, restores on another path, and checks integrity, migration count, committed events, and a read-only issue query. This is not hardware power-loss certification. |
| 8. Client retry after timeout | After-commit response loss is forced for create, claim, heartbeat, handoff, and close; exact operation-ID replay returns the original result with one ledger row and one effect. |

`TestStartupRejectsUnknownMigrationAndCorruptDatabase` proves the daemon
refuses to serve an unknown applied migration or invalid SQLite file with a
diagnostic. Startup now runs `PRAGMA integrity_check` and rejects unknown
migration-ledger entries before applying embedded migrations.
`TestInvalidMigrationRollsBackWithoutLedgerEntry` proves an invalid SQL
migration leaves neither its table nor ledger entry. The restore runbook
keeps the old DB/WAL/SHM together and validates the backup read-only before
moving the active files. A shell check of the exact runbook block showed that
missing and corrupt backup paths leave the active DB byte-for-byte intact,
while a valid backup replaces it and retains the old DB in a holding directory.
The established multi-connection race matrix remains in `afc-110`; this
slice adds real process termination and reopen evidence.

`make build`, `make test` (race), `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed, with logs at
`/tmp/afc-114-{build,test,vet,lint}.log`. A separate temporary HOME, DB,
socket, and `make build-install BINDIR=<temp>/bin` checked installed
`dibsd`/`dibs`: after SIGKILL and restart, `smoke-1` remained readable; an
unknown applied migration then prevented startup with an explicit diagnostic.
The owner's daemon and live DB were untouched. Independent read-only review
found no material defects; PR CI remains pending. This core issue remains open
for owner review and will not be
merged by the implementer.

## afc-140 — MCP operation ID propagation

The MCP create, claim, heartbeat, release, update, handoff, and close tools
accept an optional caller-provided `operation_id`, validate it against the
core contract, and forward it unchanged to the daemon. If omitted, MCP
generates a UUID and returns it in success or tool-error structured content.
The result retains its existing fields; an exact retry returns the daemon's
original committed result, and a changed request preserves the typed
`idempotency_conflict`. MCP adds no automatic retry policy. A caller that can
lose the entire response must persist its own ID before the first request;
claim IDs are private because replay can return the lease token.

`TestMCPOperationIDWireReplayAgainstTestDaemon` sends raw NDJSON to
`Server.Run` against a separate daemon and SQLite initialized with the real
embedded migrations. It covers exact replay of all seven operations, changed
create/claim/heartbeat conflicts, one create event, three fresh claim events,
one close event, and no operation IDs in event payloads. Schema and
generated-ID/error tests cover optional tool arguments, forwarding,
validation, and typed error content. Existing invocation-mode tests pass with
the same result shape.

`make build`, `make test` (race), `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed; logs are
`/tmp/afc-140-{build,test,vet,lint}.log`. `make build-install` targeted a
temporary bin directory; installed `dibsd`, `dibs`, and `dibs-mcp` used a
scratch HOME, DB, and socket to create `smoke-1` and replay the same MCP
create ID with an identical result. No live daemon or DB was touched.
Cross-reference: R-09 in this packet and the transport contract in
`docs/mcp-server-v1.md`, with the retry rule also in
`docs/agent-protocol-v1.md` and its embedded CLI copy; no new idempotency
semantics were introduced. The protocol-copy check and targeted
`go test ./cmd/dibs ./internal/mcp -count=1` passed after that doc update.
Pending independent review and PR CI before merge.

## AFC-SDD-0161 / afc-113 — retry-safe lifecycle mutations

Heartbeat, release, update, handoff, and ordinary close now accept an optional
caller-owned `operation_id` through core, HTTP, client, CLI, and SQLite. A
transaction checks the ledger before current lease/version/status, then records
the original public outcome in the same commit as the mutation. Exact replay
does not renew expiry twice, change the issue version twice, or duplicate a
release event, HANDOFF note, close note, or close event. A changed payload,
target, or operation kind returns `idempotency_conflict` (409). Update captures
its complete public issue inside the mutation transaction, before any later
update can change the returned result.

The API and explicit CLI `--operation-id` accept caller-persisted IDs. Omitted
IDs keep the existing behavior per `docs/api-v1.md`; callers without an ID must
reconcile an ambiguous outcome before attempting a new logical action.
Independent review found that update's `latest`/omitted version would change
the fingerprint on retry. The CLI now requires an explicit numeric
`--expected-version` whenever `--operation-id` is supplied, and argument
tests prove `latest`, `--force`, and omission fail before daemon contact.
Automatic retry policy, `issue run` retry decisions, operator overrides, MCP
argument propagation (`afc-140`), and crash/restore proof (`afc-114`) remain
outside this slice. Fingerprints include the presented lease token through a
hash, generation, expected version, actor, normalized invocation mode, and all
other request fields; operation IDs and lease tokens are absent from events.

`TestHeartbeatOperationReplaysOriginalExpiry`,
`TestReleaseOperationReplaysAfterReplacement`,
`TestHandoffOperationReplaysNoteAndRelease`,
`TestCloseOperationReplaysTerminalOutcome`, and
`TestUpdateOperationReplaysOriginalIssue` use production embedded migrations
and independent file-backed SQLite handles. They simulate a lost response by
discarding the first result, change current state, retry the original request,
and check the exact outcome and one set of effects. Each also checks a changed
payload and a new operation ID against stale state.
`TestLifecycleOperationKindConflictAndConcurrentHeartbeat` races two
independent handles with the same ID but different daemon times; both receive
one stored expiry and there is one ledger row. The HTTP table test exercises
all five endpoints for original response, replay, and typed 409 conflict.
CLI argument tests cover each new flag.

`make test` (race), `make build`, `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed; logs are under
`/tmp/afc-113-{test,build,vet,lint}-final.log` after the review correction.
A temporary `HOME`, `DIBS_DB`, and
`DIBS_SOCKET` with `make build-install BINDIR=<temp>/bin` launched a scratch
`dibsd`; installed `dibs` created and claimed `smoke-1`, then repeated the
same heartbeat and close IDs. Output: `scratch install and daemon: ok;
heartbeat replay: True close replay: True issue: smoke-1`. The owner's daemon
and live database were not changed. This PR awaits owner review and merge.

## AFC-SDD-0160 / afc-112 — retry-safe create

The claim half was delivered by `afc-111`. Create now accepts an optional
client `operation_id` through core, HTTP, client, and CLI. The CLI generates
and fsyncs a new ID before sending, journals by project and canonical request
fingerprint, and exposes `--operation-id` / `--retry-last`; `create-form` also
journals its create ID. The operation ledger stores the original public issue
in the same transaction as its issue row, one `issue_created` event, tags, and
project sequence increment. Replay checks the ledger before live references,
so later issue changes cannot change the old outcome.
The fingerprint binds project, actor, every issue field and tag set; default
type/priority and tag order are normalized. A changed payload or operation
kind fails `idempotency_conflict`. Requests without an ID retain the legacy
behavior, following the existing claim compatibility contract in
`docs/api-v1.md`.
Repository and worktree references are fingerprinted as supplied: switching
between a name and UUID on retry fails closed rather than guessing that the
requests are equivalent.

`TestCreateOperationReplayAfterLaterStateChange` proves the original issue and
short ID survive a later claim; `TestCreateOperationPayloadMismatch` varies
all material create fields; `TestConcurrentCreateOperations` runs 30 identical
and 30 conflicting races through independent `Open` handles with embedded
migrations; `TestCreateOperationReplayAfterReopen` reopens the database;
`TestCreateOperationKindAndWeakIDFailClosed` and
`TestCreateOperationFailedTransactionHasNoLedgerOrSequenceEffect` cover kind
binding, validation, and rollback. The HTTP test proves 201 replay, 409
`idempotency_conflict`, 400 weak-ID rejection, and one issue/event/ledger row.
All database fixtures use the real embedded migration set.

Scratch binary check (separate temporary HOME, DB, Unix socket, and `dibsd`):
`go build -o <tmp>/dibsd ./cmd/dibsd`,
`go build -o <tmp>/dibs ./cmd/dibs`, then `dibs project add --key smoke
--name Smoke`, `dibs --json issue create --project smoke --scope-kind project
--title 'lost response'` with its output discarded, and the same create with
`--retry-last`. Output: `scratch_create smoke-1 replay_same=True
conflict=idempotency_conflict issues=1`. No installed binary or live daemon
was changed.

`make build`, `make test` (race), `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed. The first full test run identified
that the embedded CLI protocol copy lagged the canonical document; the files
were synchronized and the full checks passed on the final implementation.
Independent review found that an API `internal_error` could follow an uncertain
transaction commit, yet the CLI treated every typed API error as a definite
rejection and erased the retry journal. The CLI now restores a displaced ID
only for documented create rejections; an internal or unknown server error
preserves the current ID and prints retry guidance. The subprocess regression
test `TestCreateCLIPreservesJournalAfterServerInternalError` sends a raw API 500
through the actual CLI path and confirms the operation ID remains on disk.
R-09 remains open for lifecycle mutations in `afc-113`; crash/restore proof
remains `afc-114`. This PR awaits owner review and has not been merged.

## afc-122 — CLI argument parsing

The CLI now validates the complete command route before its daemon revision
probe or command handler. Unknown flags, missing values, malformed integer
fields, invalid enums, and extra positionals fail with exit 1. With `--json`,
they emit one `validation_failed` API envelope on stderr and leave stdout
empty. The `issue run --` separator keeps child argv, including child `--json`,
outside the CLI parser. The contract comes from packet 002 and
`docs/agent-protocol-v1.md` (see `traceability.md`).

`TestCommandArgumentsFailClosed` covers every command family and the key
lifecycle parsers; `TestDocumentedCommandArgumentsRemainAccepted` preserves
valid invocation shapes; `TestMalformedJSONCommandNeverContactsDaemon` runs
the built binary with a nonexistent socket and checks the raw stdout/stderr
envelope. Before the fix, the installed binary accepted `--ttl 90bad`,
attempted a claim request, and returned `internal_error`; it also wrote an
operation journal. The temporary journal created by that regression probe was
removed. The corrected command rejects the argument before request creation.

`make build`, `make test` (race), `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed on the worktree. The focused CLI suite
also passed with `go test ./cmd/dibs -count=1`.
Independent review found two argument paths that still reached a daemon probe:
a misplaced issue ID and the mutually exclusive claim retry flags. Both now
fail in the preflight parser; raw CLI tests assert the typed envelope, empty
stdout, and absence of a claim operation journal in an isolated home.
The follow-up review also found empty issue IDs and empty alternative targets;
preflight now rejects those values before a journal or daemon request.

## afc-121 — MCP invocation-mode audit propagation

Contract source: `docs/specs/002-agent-protocol/requirements.md` via `docs/agent-protocol-v1.md` and the existing CLI/API behavior (see `traceability.md`).

MCP `claim_issue`, `add_note`, `handoff_issue`, `close_issue`, and
`operator_close_issue` now expose the optional enum in `tools/list`, validate it
before mutation, and pass the normalized value to the existing client/API
paths. `TestInvocationModeToolSchemas` checks every affected schema.
`TestInvocationModeMCPAuditEvents` uses a scratch daemon and real SQLite with
embedded migrations to assert declared and omitted values in claim, note,
handoff, close, and operator-close audit events. For each tool, an invalid
value produces an MCP tool error and no new event. The focused test failed
before the fix because the schemas omitted the field and claim rejected it as
an unknown argument; it passes after the fix.

`go test ./internal/mcp -count=1`, `make build`, `make test` (race), `make vet`,
and `GOTOOLCHAIN=go1.26.4 make lint` passed. `make build-install` updated
installed binaries without restarting the owner's daemon. Installed
`dibs-mcp` called `claim_issue` twice against a temporary `dibsd` DB/socket;
`dibs issue events list` reported `issue_claimed` modes `interactive` and
`scheduled` respectively. PR `#70` CI `test` passed on implementation HEAD
`f05ddc1`; owner-authorized merge awaits final independent review.

## afc-120 — MCP lease-generation contract correction

The MCP `tools/list` schemas now require `lease_generation` for heartbeat,
handoff, and close. Handoff already forwarded it; close now validates and
forwards the claimed value rather than sending zero. Canonical and embedded
`dibs protocol` examples include the mandatory generation on heartbeat and
bare release. This is a public-contract repair under R-04/R-11, not a change to
lease semantics.

`TestLifecycleToolSchemasRequireLeaseGeneration` checks the published required
fields and type. `TestLifecycleToolsFenceGenerationAgainstDaemon` exercises
missing, stale, and valid generations for handoff and close through the MCP
client, scratch daemon, and real SQLite with embedded migrations. Both failed
before the fix (`handoff_issue`/`close_issue` schema absent; close rejected
`lease_generation` as unknown) and pass after it. `go test ./internal/mcp
-count=1`, `make build`, `make test` (race), `make vet`, and
`GOTOOLCHAIN=go1.26.4 make lint` passed.

`make build-install` updated the installed binaries without restarting the
owner's daemon. A separate temporary DB/socket and installed `dibsd`, `afctl`,
and `afc-mcp` created and claimed an issue, listed the three schemas, rejected
a stale close generation, accepted the current one, and read final status
`done`; the alias deprecation notice appeared only on stderr. PR `#69` CI
`test` passed on implementation HEAD `024b211`; owner-authorized merge awaits
the final independent review.

## AFC-SDD-0152 / afc-104 implementation review

The heartbeat/release lease-CAS slice is merged via PR `#53` (source
`f5caa6d`, merge `f9079cc`), whose GitHub CI passed:

- heartbeat conditionally updates by issue, token, generation, and
  `expires_at > daemon_now`, checks exactly one affected row, and cannot renew
  an expired or replaced lease;
- release reads and deletes the same unexpired token+generation inside one
  transaction, then performs one issue status/version transition and appends
  one attempt-linked release event before commit;
- daemon-supplied UTC time defines the authorization boundary consistently;
- API, client, CLI, MCP, core, and store contracts all carry the generation.

Production-migration store regressions include
`TestHeartbeatRenewalWinsBeforeReclaim`,
`TestStaleHeartbeatFailsAfterReclaim`,
`TestHeartbeatLeaseExpiredFailsWithoutMutation`,
`TestReleaseLeaseExpiredFailsWithoutStateChanges`, and
`TestReleaseLeaseSingleStatusVersionTransition`. The 2026-08-13 repository
quality follow-up also passed the full normal and race suites after the later
`afc-108`/`afc-109` corrections.

## AFC-SDD-0153 / afc-105 implementation review

The leased-mutation fencing slice is merged via PR `#55` (source `ed61ca5`,
merge `81ac038`), whose GitHub CI passed:

- update rejects a mismatched token or generation for leased work and applies
  expected-version in the transactional `UPDATE` predicate;
- handoff and close require the current unexpired token+generation;
- rejected stale operations leave replacement ownership, issue state, notes,
  and lifecycle events unchanged;
- handoff note, release, and events remain one rollback-safe transaction.

Production-migration store regressions include
`TestStaleGenerationUpdateFailsAfterReclaim`,
`TestStaleGenerationHandoffFailsAfterReclaim`,
`TestStaleGenerationCloseFailsAfterReclaim`,
`TestUpdateIssueVersionConflict`, and
`TestHandoffLeaseRollsBackWhenNoteOrReleaseWriteFails`. The 2026-08-13
repository quality follow-up also passed the full normal and race suites after
the later `afc-108`/`afc-109` corrections.

### 2026-08-13 afc-105 correction

The review then checked the implementation against R-04/R-06 rather than only
the merged regressions. `UpdateIssue` still called `GetIssue` before its
transaction; because `GetIssue` exposes only unexpired leases, an expired owner
could update metadata before reclaim without any lease check. The new
`TestExpiredLeaseUpdateFailsWithoutMutation` reproduced that stale write before
the correction.

The reopened implementation now reads issue version/state and the raw lease row
inside the immediate writer transaction. Any existing lease row requires the
matching token and generation and `expires_at > daemon_now`; the update-release,
handoff, and close writes also repeat the complete lease predicate and verify
affected rows. Store and API regressions cover the expiry window, while
`TestConcurrentUpdatesWithSameVersionCommitOnce` proves one commit and one
typed conflict. `TestUpdateIssueRejectsExpiredLease` verifies the public API
returns HTTP 410 with `lease_expired` and leaves issue state unchanged.

Local verification in the sibling worktree:

- the focused expired-lease regression failed before the correction and passed
  after it;
- all focused update/handoff/close store and API regressions — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass.

The correction is merged via PR `#62` (source `6620f0c`, merge `43574f5`);
GitHub CI passed in 2m24s. A temporary installation under `/tmp` ran the merged
revision against a scratch DB/socket: after a one-second lease expired,
`afctl issue update` returned typed `lease_expired`, and reread preserved the
original title and version. The production daemon and installed binaries were
not changed or restarted.

## AFC-SDD-0157 / afc-109 quality follow-up

The 2026-08-13 full-suite review reproduced a deterministic failure in
`TestIssueRunStopsChildOnLeaseLoss`: cancellation signalled only the shell
leader, its active `sleep` child retained the subprocess pipes, and the shell's
TERM trap never recorded graceful cancellation before `WaitDelay` killed the
leader. The reopened correction isolates every `issue run` workload in its own
Unix process group, sends group-wide `SIGTERM`, and performs bounded group-wide
`SIGKILL` cleanup after a cancelled run. The correction is merged via PR `#58`
(source `fcc768e`, merge `b6fb9b0`).

Local verification in the sibling worktree:

- the focused regression failed before the correction because the TERM marker
  was absent, then passed with a TERM marker and proof that a descendant which
  ignored TERM no longer existed when `issue run` returned;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass.

GitHub CI passed in 2m33s before merge. Production daemon and installed binaries
were intentionally not changed or restarted during this follow-up.

## AFC-SDD-0151 / afc-103 implementation review

The lease-generation slice is merged via PR `#47` (source `78a6489`, merge
`48c157d`):

- migration `0008_lease_generation.sql` adds an issue-local counter and the
  matching active-lease generation, backfilling active legacy leases to `1`;
- fresh claims increment generation once, while heartbeat preserves it;
- claim responses, secret-safe issue reads, CLI text/JSON, `issue run` through
  `AF_LEASE_GENERATION`, and lease lifecycle events expose the same non-secret
  generation without recording the lease token;
- migration, monotonicity, legacy-read, API, client, CLI environment, expiry,
  release, and operator-release regressions use the embedded production
  migrations.

Verification in the sibling worktree:

- `git diff --check` — pass;
- `make build` — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass.

Installed verification confirmed generation `1` for a migrated active legacy
lease and monotonic generations `1`, then `2`, across release and fresh claim.

The generation is not yet required on heartbeat/update/handoff/close requests;
those atomic fencing predicates remain owned by `afc-104` and `afc-105`.

## AFC-SDD-0154 / afc-106 implementation review

The ready-qualified claim slice is merged via PR `#48` (source `d876146`, merge
`a76b099`), with its typed `issue_not_ready` correction in PR `#49` (source
`2a83437`, merge `4f94a0a`):

- ready listing and claim use one executable-state predicate for status, issue
  type, and unfinished `blocks` dependencies;
- claim re-evaluates that predicate inside its transaction and returns typed
  `issue_not_ready` without changing issue, lease, version, or event state when a
  blocker remains unfinished;
- an active lease always returns `lease_held`; claim never reads or returns its
  token based on the public holder string, and a rejected same-holder retry
  cannot renew expiry or append a `lease_reattached` event;
- the existing concurrent distinct-holder regression still produces one fresh
  claim, one usable token, and one `issue_claimed` event.

Verification in the sibling worktree:

- `git diff --check` — pass;
- `make build` — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass.

Installed black-box verification confirmed a blocked direct claim returns
`issue_not_ready` with CLI exit code `7` and no claim side effects.

Durable retry of an ambiguously completed claim remains intentionally pending
the operation-id ledger in `afc-111`; until then, clients must reconcile rather
than attempting holder-only token recovery.

## AFC-SDD-0156 / afc-108 implementation review

The single-daemon writer and SQLite connection-contract slice is merged via PR
`#50` (source `a055aa7`, merge `28b0f80`):

- startup takes a non-blocking `flock` on `<canonical-db-path>.lock` before
  opening or migrating SQLite and holds it until listener and database shutdown;
- socket cleanup probes an existing Unix listener, refuses to unlink a live
  daemon, and removes only a confirmed unreachable socket while DB ownership is
  held;
- production SQLite uses one physical connection with immediate transactions;
  WAL, foreign keys, a 5000 ms busy timeout, and `synchronous=NORMAL` are encoded
  in the DSN and verified at startup;
- the process umask, newly created runtime directories, database, and lock file
  use restrictive modes; the group-accessible Unix socket remains `0660` per the
  documented cooperative local trust model;
- focused tests cover canonical-path alias exclusion, lock release/reacquire,
  live/stale socket behavior, settings and foreign-key enforcement on three
  independently opened handles, and eight concurrent claimers producing one
  winner.

Verification in the sibling worktree:

- `git diff --check` — pass;
- `make build` — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass;
- black-box two-daemon test — the second process for the same DB exited without
  disturbing the first listener; after `SIGKILL`, a replacement acquired the
  released lock, removed the stale socket, and served `/v1/health`;
- black-box modes — database and lock `0600`, Unix socket `0660`.

The same black-box recovery scenario passed through the installed binaries at
revision `28b0f80`; `afctl doctor` confirmed client, daemon, and local `HEAD`
revision parity, and the live `afc-108` claim survived the service restart.

This lock prevents cooperative daemon duplication. It deliberately does not
prevent hostile same-UID code from opening SQLite directly. Full eight-case
crash injection, integrity policy, and backup/restore proof remain `afc-114`.

### 2026-08-13 quality follow-up

The original directory-symlink test did not cover a symlink at the database
file itself. A scratch runtime reproduced two live daemon processes, two lock
files, and one SQLite database. The reopened `afc-108` change now:

- resolves existing and dangling final DB-file symlinks before deriving the
  persistent lock path;
- adds an automated two-process test proving the alias daemon exits, the first
  socket remains reachable, and a replacement starts after abrupt death;
- normalizes existing default runtime directories to `0700`, while leaving
  custom configured parents operator-managed.

Verification after rebasing onto the corrected `afc-109` remote main:

- focused daemon singleton and permission regressions — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass;
- `go vet ./...` and `make build` — pass.

The correction is merged via PR `#60` (source `41d5517`, merge `46f1623`);
GitHub CI passed in 2m22s. A temporary `make build-install` target under `/tmp`
proved revision parity without replacing user binaries. In that scratch
runtime, the second daemon through a DB-file symlink exited as already owned;
after `SIGKILL`, a replacement served health on the stale socket using the alias
path; only canonical `real.db.lock` existed and DB/WAL/SHM modes were `0600`.

The production daemon and installed binaries were intentionally left untouched
until the operator separately permits an install/restart.

## AFC-SDD-0155 / afc-107 implementation review

The dependency-serialization slice is implemented in this change:

- `AddDependency` now begins its transaction first and resolves both endpoints,
  verifies the cross-project endpoint policy, traverses `blocks` edges, inserts
  the edge, and appends `dependency_added` against that single serialized-writer
  transaction. The previous pre-transaction cycle check could validate
  concurrent opposite edges against an incomplete graph and commit a cycle;
- `wouldCreateCycle` now takes a queryer satisfied by both `*sql.DB` and
  `*sql.Tx`, returns `(bool, error)`, and propagates query/scan errors instead
  of reporting "no cycle";
- the defined cross-project endpoint policy rejects any dependency edge whose
  endpoints belong to different projects with a typed `validation_failed`, and
  the API handler maps it to HTTP 400;
- regression tests cover simultaneous opposite edges (at most one committed
  edge and one `dependency_cycle`), an injected traversal failure that aborts
  without an edge/event, cross-project rejection, and ready-after-blocker-close
  through `ListReadyIssues`.

Verification in the sibling worktree:

- `git diff --check` — pass;
- `make build` — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass.

General graph analytics, cached ready state, parent/related/discovered-from
semantics changes, and UI remain out of scope per AFC-SDD-0155.

## AFC-SDD-0158 / afc-110 implementation review

Implementation commit `f9590f3` is delivered through PR `#64`.

The pre-idempotency concurrency matrix is implemented with two independent
SQLite handles opened through the production `Open` path and the real embedded
migrations. Private context-scoped proof hooks hold an immediate transaction at
the named claim, heartbeat, handoff, or update boundary; callers outside the
SQLite package cannot activate them, and normal runtime execution remains a
no-op.

`TestCoordinationRaceMatrix` repeats each scenario for 100 schedules without
`time.Sleep`:

- concurrent claims produce one token/generation/event and one typed
  `lease_held` loser;
- heartbeat/reclaim proves both commit orders: renewal prevents replacement,
  or replacement commits and the old heartbeat returns `lease_expired` without
  changing the new deadline;
- a close queued behind reclaim returns `lease_expired` and cannot write its
  note, close event, or terminal state;
- heartbeat/handoff proves both commit orders with exactly one handoff note,
  one release event, no lease, and no partial audit sequence;
- context cancellation after update authorization but before its write leaves
  issue, version, lease, and events unchanged;
- a close whose successful response is treated as lost commits exactly one
  note/event/state transition; replay returns `version_conflict` and does not
  duplicate the effect.

`TestMultiConnectionDependencyCycleSerialization` additionally repeats 100
opposite-edge schedules through separate handles: exactly one edge/event
commits, the loser receives `dependency_cycle`, and exactly one issue is
blocked.

The last two boundaries are intentionally progressive evidence. Context
cancellation is not a daemon process kill; black-box termination remains
`afc-114`. The committed-close replay is non-duplicating but cannot return the
original success until `afc-111` through `afc-113` add operation IDs and stored
outcomes. This review therefore does not claim R-09 idempotency or the R-10
crash/restart matrix.

Focused verification in the sibling worktree:

- `go test ./internal/store/sqlite -run '^TestCoordinationRaceMatrix$' -count=1 -v` — pass in 4.00s;
- `go test -race ./internal/store/sqlite -run '^TestCoordinationRaceMatrix$' -count=1` — pass in 81.307s;
- `make test-concurrency` — pass in 5.819s;
- `git diff --check` — pass;
- `make build` — pass;
- `go vet ./...` — pass;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass; the SQLite package, including the
  repeated matrix, completed in 88.501s.

## Planning outcome

- Preserved the 2026-08-11 evidence-based technical audit in `audit.md`.
- Defined the concurrency, fencing, durability, idempotency, and operational
  requirements in `requirements.md`.
- Chose a SQLite-preserving single-daemon design in `design.md`.
- Created implementation epic `afc-102` and bounded leaves `afc-103` through
  `afc-116` with priority, dependency, factory-routing, scope, out-of-scope, and
  acceptance criteria.
- Gated every implementation leaf on planning issue `afc-101`, so no factory
  worker can start from an unmerged packet.
- Reconciled stale packet 011 and rewrote/gated the pre-existing live backlog
  rather than deleting product ideas.

## Earlier implementation state

At this point in the implementation history, lease-bound mutation fencing and
the pre-idempotency concurrency matrix had shipped; durable idempotency,
black-box crash/restart proof, integrity and backup recovery, and operational
observability were assigned to later leaves. Those leaves are now merged, as
recorded in the epic closure table above. The historical race classifications
in `audit.md` retain the original audit result.

## Implementation review gate

The closure table above and the leaf evidence below satisfy these packet
completion checks:

- commits and PRs for every required leaf;
- focused regression tests added in each behavior change;
- deterministic results for all six concurrency races;
- black-box results for all eight crash/recovery cases;
- installed daemon/CLI revision parity and scratch-runtime verification;
- updated maturity assessment and any remaining trust boundary;
- explicit operator decision on whether concurrent factory use is enabled.

## Planning verification

Verified in sibling worktree
`/home/abevz/github/af-coordinator/afc-101-coordination-safety`:

- `git diff --check` — pass;
- `make build` — pass at revision
  `68f4bc67e6ca7067d13bc1c75c7ec3f4204df613`;
- `go test ./... -count=1` — pass;
- `go test -race ./... -count=1` — pass;
- `afctl issue list --project afc --status open,in_progress,deferred --json`
  — confirms the epic, all 14 leaf IDs, parent edges, blocking DAG, priorities,
  and `exec/auto` tags;
- `afctl issue ready --project afc --json` — `[]` while `afc-101` is claimed;
- `afctl issue ready --project afc --tag exec/auto --json` — `[]` while the
  packet is unmerged.

The raw `go build ./...` command in this external `.bare` linked worktree
reported Go VCS-stamping status failure. The repository's canonical `make
build` target deliberately uses `-buildvcs=false` and passed; no source build
or test failed.

After merge and closure of `afc-101`, the expected first manual ready leaf is
`afc-103`; independent `exec/auto` routing may expose only `afc-107` until the
lease-contract blockers close. That post-close view is verified during issue
handoff, not predicted as current live state.

## AFC-SDD-0159 / afc-111 — durable mutation idempotency ledger

**Shipped.** An `operations` ledger (`migrations/0009_operation_ledger.sql`)
recording operation ID, kind, target, actor, canonical request fingerprint,
completion state, serialized public outcome, and retention timestamps. Claim
accepts an optional client-generated `operation_id` and is the representative
mutation this task requires; a new `idempotency_conflict` code covers reuse
with a different request.

**Verified.** Replay of a committed claim returns the original `lease_token`,
`lease_generation`, `attempt_id`, `expires_at`, and `version`, executes no
second mutation, and leaves the fencing generation unchanged. Replay still
works after the lease is released and after a daemon restart. Reuse of an
operation ID with a different target, TTL, holder, session, or operation kind
fails closed with no mutation. Two concurrent identical operations across
independent production-initialized SQLite handles yield one lease, one claim
event, one ledger row, and one token, over 100 schedules; two concurrent
conflicting operations yield exactly one success and one typed conflict over
100 schedules. Lease tokens and operation IDs appear in no event, issue read,
or listing. An end-to-end run against a scratch daemon and CLI reproduced the
original operational failure and recovered from it across a daemon restart
without `operator-release`.

**Security boundary held.** The ledger authorizes replay on possession of the
`operation_id` plus request equivalence — never on `holder`, `actor`, or
`session_id`, which remain attribution. A claim carrying identical holder and
session but a different (or absent) operation ID still receives `lease_held`
and never sees the active token, so the same-holder reattach removed by
AFC-SDD-0154 is not reintroduced. `operator-release` is unchanged and remains
the separate audited break-glass path.

**A client-side bug found and fixed during end-to-end validation.** `afctl`
journals each claim's operation ID before sending, so a lost response does not
lose the key. The first implementation overwrote that journal on every attempt,
so a later claim that the daemon *rejected* destroyed the recovery key of an
earlier claim that had actually committed. The journal now returns the
displaced key and restores it whenever the daemon answers with a typed error,
since a rejected request never committed. Regression tests cover it.

**Open.** Retention is recorded per row (`retain_until`, 30 days) and
documented, but no automatic reaper deletes expired rows yet. Endpoint
adoption beyond claim stays with `afc-112` (create) and `afc-113` (lifecycle
mutations), and the black-box crash/restart matrix remains `afc-114`.
