# 016 Adoption Review

Status: approved by owner; packet active. afc-137 implementation awaits owner review.

## Packet authoring (`afc-127`)

- Owner approval: approved by Aleksey Bevz on 2026-09-23 (PR #66 @ da3c8b2).
- Scope: README, requirements, design, task map, and this review stub.
- Requirements/design alignment: updated for the owner's decisions; independent
  review evidence belongs on PR #66.
- Owner decisions: the notes dated 2026-09-23, including PR #66 resolutions,
  are recorded in `design.md`; no product decision remains open in this draft.
- Acceptance evidence: packet files and one-to-one child map are reviewable.

## Implementation ledger

### afc-137 — rename with legacy compatibility (owner review pending)

- Scope: `github.com/abevz/dibs` module/imports; `dibs`, `dibsd`, and
  `dibs-mcp` binaries; old command aliases; canonical `DIBS_*` configuration;
  old live DB/socket selection; new service units and explicit switch docs.
  Project key `afc`, issue IDs, external keys, and installed old service unit
  are unchanged. Release publication and tags remain with `afc-128`.
- Focused evidence: `TestCanonicalEnvironmentWinsOverLegacy`,
  `TestLegacyPathsRemainCanonicalUntilMigration`,
  `TestDaemonUsesExistingLegacyDatabaseWithoutCreatingSecondDB`,
  `TestOperatorTokenCanonicalEnvironmentWins`,
  `TestIssueRunExportsLeaseEnvToChild`, and
  `TestBuildInstallLegacyAliasesPreserveStdout` passed. The daemon test uses
  real embedded migrations and verifies that starting against an existing
  legacy DB does not create a second DB.
- Installed-binary proof: `make build-install` under temp HOME
  `/tmp/dibs-install-u_s1kj48` started a scratch `dibsd`; `dibs` created and
  claimed `smoke-1`, then `afctl` created and claimed `smoke-2`. Alias stdout
  matched, deprecation appeared only on stderr, and the scratch new DB was
  used. A local fake-release fixture verified the checksum installer and its
  three old-name symlinks. No live daemon or installed service was restarted.
- Local gates: `gofmt -w cmd internal`, `git diff --check`, shell syntax checks,
  `make build`, `make test`, and `make vet` passed. Lint passed with
  `GOTOOLCHAIN=go1.26.4 make lint` (`0 issues`); the system Go 1.27 toolchain
  cannot be parsed by the locally installed linter built with Go 1.26.3.
- PR #67 CI passed on implementation HEAD `3c03618`; independent read-only
  review found no material issue on that head. Owner review is pending. The
  owner must perform the manual service switch in `docs/operations.md` after
  merge. There is no automatic DB migration or `dibs migrate-paths` in this
  slice; a later explicitly designed migration can move live SQLite/WAL state.
- Owner review follow-up in PR #67: the Linux switch reuses the existing
  `~/.config/af-coordinator/operator.env` through a new `dibsd.service.d`
  drop-in before `dibsd` starts; no token is copied or printed. The health API
  reports only whether the daemon has a token, and `dibs doctor` warns when a
  new daemon has none while legacy token configuration exists. Older daemons
  omit that health field and are not misreported. Launchd has no equivalent
  per-agent `EnvironmentFile`; its manual token handoff is documented.
- The main-only post-merge hook rebuilds and `try-restart`s only an active
  `dibsd`. An active legacy service gets the manual-switch message; inactive
  units are never started. The mock-systemctl shell regression failed against
  the prior hook and passed after the fix, including build/restart failures.
  Focused Go tests, `make build`, `make test`, `make vet`, gofmt, and
  `GOTOOLCHAIN=go1.26.4 make lint` passed. Follow-up PR CI is pending.
