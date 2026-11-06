# Review

Implementation covers afc-185 and afc-186 according to requirements/design.

- Failed/cancelled/incomplete issue runs stop heartbeat, preserve the bounded lease, record cwd/branch recovery context and save private token/metadata files.
- Issue-scoped durable evidence is registered with SHA-256 and revision binding, visible in issue detail, claim and deliberate handoff; JSONL includes metadata.
- Optional close/publish evidence checks require intact final-diff evidence and PASS review status. Retention preserves summaries. Cleanup removes only verified redundant temporary sources of terminal issues; unknown legacy /tmp files remain untouched.
- Existing repository document artifact registration is preserved.

Verified: `gofmt`, `git diff --check`, `go build ./...`, `go test ./...`, focused race tests for evidence and handoff replay, focused API/CLI integration checks. The failed-run regression was observed failing before the fix and passing after. Scratch installed binaries passed a real +18 second second-claimer rejection, explicit handoff/new-claim context and gated close. The final installed-binary rerun passed. Independent implementation review: PASS after correcting issue-mode CLI kind validation and refreshing the close timestamp after evidence verification. The deterministic expiry regression failed before that correction and passed after it.

Evidence directory (outside Git): `/home/abevz/github/dibs/afc-185-186-evidence/` (`build-final.log`, `tests-final.log`, `race-focused.log`, `cli-focused.log`, `store-api-focused.log`, `install-final.log`, `smoke-final.log`, `expiry-red.log`, `expiry-green.log`). Historical regression logs are preserved there as `regression-red.log` and `regression-green.log`.

Limits: filesystem deletion and SQLite commit are not one atomic medium; failed prune may leave a missing file with unpruned metadata and is safe to retry. PASS and diff identity are caller-declared; dibs verifies bytes/binding, not review quality. No global mandatory evidence policy or artifact uploading. Live service installation/restart is deferred; Git delivery is owner-authorized through independent PASS review, green CI and merge. Coordinator issues remain open until merge. Actual PR/merge results belong in coordinator notes and the PR audit trail.


Owner-authorized merge delivery re-ran `gofmt -w .`, `go vet ./...`,
`go build ./...`, `go test ./...`, focused evidence/handoff race tests, and
`golangci-lint run --disable errcheck,staticcheck`: all passed. Scratch binaries
again passed the actual +18 second competing-claim scenario. The previous
installed daemon binary was copied and tested only against the upgraded
scratch database: startup failed with `unknown applied migration
"0012_issue_evidence.sql"`, as required by `VerifyKnownMigrations`.

`TestEvidenceMigrationPreservesLegacyDataAndRejectsOldBinary` starts from the
real pre-0012 migrations with existing project/issue/tag/note/lease/event data.
It compares every prior application table's definition and rows after upgrade,
checks the ledger records 0012 once, repeats the real migration runner, checks
foreign keys and rejects the upgraded DB with the prior migration set. Raw SQL
replay is not the supported upgrade interface; `Migrate` skips ledgered files.

The [upgrade runbook](upgrade-runbook.md) requires both dq queues empty and no
active dq claims, paused dispatch, consistent SQLite-file backup plus JSONL,
stop -> install all three binaries from one reviewed revision -> start ->
doctor/health/migration verification, and restore database plus binaries for
rollback. Shell examples passed `bash -n`; embedded Python parsed successfully.
It explicitly requires a separate owner command and bypasses automatic
post-merge redeploy when advancing the read/merge anchor.

Renewed evidence stays outside Git under the evidence directory's `merge/`:
`vet.log`, `build.log`, `tests.log`, `race.log`, `lint.log`, `migration.log`,
`smoke.log`, `old-binary-rejection.log` and `live-binaries-before.sha256`.
Independent final review is a publication gate; its final result is recorded
with the reviewed revision in coordinator notes and the PR. No runtime review
reports or database snapshots are committed.


Final runbook review identified removable Python assertions and non-fail-fast
install/rollback blocks. Safety gates now raise explicit errors, and each
mutating shell block starts with `set -eu`. Isolated fixtures under
`PYTHONOPTIMIZE=1` verified that queued work, active leases, bad health, a wrong
revision and an unconfirmed migration all fail closed. Failed-stop/still-active
shell fixtures verified that subsequent writes are skipped. Results:
`merge/runbook-checks.log` outside Git.

The maintenance gate also validates each issue row's required identity/status
fields and lease metadata types/coherence, rejecting malformed rows or
lease-less `in_progress` work for reconciliation. Optimized-Python fixtures
cover malformed top-level/row shapes, missing fields, invalid metadata types,
unpaired holder/deadline fields, unknown status and timezone-less expiry.

Maintenance now acquires and holds the daemon's persistent canonical database
ownership lock across installation or database/binary rollback, refusing a
stray daemon even if the systemd unit is inactive. Isolated lock-holder fixtures
verify rejection before writes and exclusion through the critical section.
The lock file is neither truncated nor removed; it is released before start.
