# 015 Coordination Safety Review

## Status

Specification and backlog slicing complete; implementation in progress.

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

## What has not shipped

Packet 015 remains incomplete. Lease-bound mutation fencing and the
pre-idempotency concurrency matrix are now implemented, while durable
idempotency, black-box crash/restart proof, integrity and backup recovery, and
operational observability remain assigned to later leaves. The historical race
classifications in `audit.md` retain the original audit result; current
implementation evidence and remaining boundaries are recorded in this review
and `traceability.md`.

## Implementation review gate

Packet 015 must not be marked complete until all of the following are recorded
here:

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
