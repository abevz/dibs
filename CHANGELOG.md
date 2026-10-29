# Changelog

## Unreleased

## v0.1.0-rc.6 — 2026-10-29

### Added

- `dibs stats --by actor` and `--top N`: normalized claim-owner outcomes, costly
  issues and a documented claim-without-progress indicator (afc-175).
- `dibs watch --interval <duration>` (default `5m`, minimum `2s`): the longest
  wait between full board refreshes when no coordinator event arrives
  (afc-182).

### Fixed

- `dibs watch` no longer makes the daemon re-read the whole database every
  ~2 s. The board long-polls the event cursor and reloads only on an event, at
  the earliest active lease expiry, on `r`, or after `--interval`. On a 15 MB
  database an open all-projects board dropped from ~8 MB/s reads and ~25%
  daemon CPU to ~100 B/s and under 1%. Board output is unchanged; the fix is in
  the CLI, so it works against an older running daemon (afc-182).
- Auto-started daemon logs are bounded in the state directory instead of growing
  beside the socket; verified stop cleans owned pid/legacy startup-log artifacts
  (afc-160).
- `dibs repo relocate` explains how to recover when the old path is already
  gone: temporarily link it back, with the exact `ln -s` command (afc-155).
- `dibs watch --once` output is locked down by a golden test: unpadded rows and
  one final newline (afc-157).

## v0.1.0-rc.5 — 2026-10-02

### Breaking changes — afc-142

**Migrate consumers before upgrading.**

- Installs expose only `dibs`, `dibsd`, and `dibs-mcp`. The `afctl`,
  `af-coordinatord`, and `afc-mcp` aliases and their deprecation warnings are
  removed. Upgrade installers remove only their own compatibility symlinks;
  independent executables or symlinks to other targets remain untouched.
- Legacy environment fallback is removed. `issue run` exports only canonical
  lifecycle metadata; update child scripts and private EnvironmentFile keys
  before upgrading, keeping token values private.

| Legacy name | Canonical replacement |
| --- | --- |
| `AF_COORDINATOR_DB` | `DIBS_DB` |
| `AF_COORDINATOR_SOCKET` | `DIBS_SOCKET` |
| `AF_COORDINATOR_LOG_LEVEL` | `DIBS_LOG_LEVEL` |
| `AF_COORDINATOR_ACTOR` | `DIBS_ACTOR` |
| `AF_OPERATOR_TOKEN` | `DIBS_OPERATOR_TOKEN` |
| `AF_LEASE_TOKEN` | `DIBS_LEASE_TOKEN` (prefer private `DIBS_LEASE_TOKEN_FILE`) |
| `AF_LEASE_GENERATION` | `DIBS_LEASE_GENERATION` |
| `AF_ATTEMPT_ID` | `DIBS_ATTEMPT_ID` |
| `AF_ISSUE_ID` | `DIBS_ISSUE_ID` |
| `AF_EXPECTED_VERSION` | `DIBS_EXPECTED_VERSION` |
| Installer `AF_COORDINATOR_REPO` | `DIBS_REPO` |

Consumer-context exports formerly named `AF_COORDINATOR_PROJECT` and
`AF_COORDINATOR_REPOSITORY` must also use `DIBS_PROJECT` and `DIBS_REPOSITORY`.

- The old daemon systemd unit template is retired. Existing legacy data and
  socket paths remain supported to preserve database identity. Backup unit
  names remain unchanged. No installer switches or restarts the live service.

The owner approved removal before v0.1.0 final on 2026-10-01, superseding the
original plan to remove aliases after v0.1.0. Known Aion Forge source already
uses canonical DIBS interfaces; the companion daily-check change migrates its
runner and create-form command to `dibs` and its actor to `DIBS_ACTOR`.

### Changed — afc-176

- `dibs init` migrates legacy integration blocks to canonical DIBS markers,
  removes stale generated blocks, preserves surrounding repository guidance,
  and remains idempotent. Dry-run migration is isolated from checkout state.

### Added — afc-177

- `dibs watch` shows a cross-project summary and a STALE lane for issues without
  a current lease. Keyboard navigation opens project boards and issue details
  read-only; `--once` keeps a scriptable snapshot.

### Added — afc-178

- `dibs update --check` compares installed/latest release versions, shows the
  intervening CHANGELOG with breaking changes first, and returns exit 0
  (current), 10 (update available), or 1 (failed). Prerelease installations
  follow prereleases; stable builds can opt in with `--prerelease`.
- `dibs update` verifies existing release checksums and atomically publishes
  all three canonical binaries in their existing installation directory,
  retaining previous binaries for offline `--rollback`. Breaking releases
  require confirmation or `--yes`; restarting dibsd separately requires
  confirmation or `--restart`. Neither operation touches coordinator storage.
- The daemon refreshes a local release cache at most daily; ordinary terminal
  commands show a cached notice after output, or in the watch footer, at most
  once daily per version. JSON, hooks, MCP and issue-run children suppress it;
  `DIBS_NO_UPDATE_NOTIFIER=1` opts out. Doctor reports cached updates alongside
  its existing CLI/daemon version mismatch check. No background auto-upgrade.

### Added and documented since rc.4

- `dibs stats` includes per-project status breakdowns (afc-174).
- README links a recorded GitHub issue import/publish demo using the published
  rc.4 binaries; current install commands now point to rc.5.
- Agent/operations guidance records the rc.4 round trips and MCP validation,
  reconciles completed packet status, and removes stale repository paths.
