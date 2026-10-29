# Operations

## Building

```bash
go build -o ~/.local/bin/dibsd ./cmd/dibsd
go build -o ~/.local/bin/dibs ./cmd/dibs
```

Or use the Makefile:

```bash
make build
```

## Systemd user service

### Install

For a fresh installation with no former daemon service, install and start
`dibsd`:

```bash
make install-service
sh contrib/install/systemctl-user.sh enable --now dibsd
```

For an existing installation, use the [explicit service switch](#explicit-service-switch)
below so the former daemon stops before `dibsd` starts.

### Check status

```bash
sh contrib/install/systemctl-user.sh status dibsd
```

### View logs

```bash
journalctl --user -u dibsd -f
```

### Start/stop/restart

```bash
sh contrib/install/systemctl-user.sh start dibsd
sh contrib/install/systemctl-user.sh stop dibsd
make restart-service
```

`contrib/install/systemctl-user.sh` fills `XDG_RUNTIME_DIR` and
`DBUS_SESSION_BUS_ADDRESS` from `/run/user/$(id -u)/bus` when they are missing,
which keeps service targets working from non-interactive agent environments.

On `main`, the `post-merge` hook rebuilds and `try-restart`s `dibsd` only when
that unit is already active. While only the former daemon is active, it leaves
the explicit switch below to the operator. It never starts an inactive unit.
`dibs doctor` detects a daemon running an older revision.

## macOS LaunchAgent

Build the binaries and stage the daemon as a user LaunchAgent:

```bash
make install-launchd
```

The Makefile renders `contrib/launchd/com.abevz.dibsd.plist.in` into
`~/Library/LaunchAgents/com.abevz.dibsd.plist` without starting it.

To uninstall the new unit:

```bash
make uninstall-launchd
```

Daemon logs are written to:

```text
~/Library/Logs/dibsd.log
~/Library/Logs/dibsd.err.log
```

## Explicit service switch

The former daemon service remains installed and running until the operator
stops it. The two services must never run together against the same database.
On Linux, after this change is merged and reviewed:

```bash
make build-install
make install-service
if [ -f "$HOME/.config/af-coordinator/operator.env" ]; then
  mkdir -p "$HOME/.config/systemd/user/dibsd.service.d"
  cat > "$HOME/.config/systemd/user/dibsd.service.d/operator-token.conf" <<'EOF'
[Service]
EnvironmentFile=%h/.config/af-coordinator/operator.env
EOF
  sh contrib/install/systemctl-user.sh daemon-reload
fi
sh contrib/install/systemctl-user.sh disable --now af-coordinatord
sh contrib/install/systemctl-user.sh enable --now dibsd
dibs doctor
```

On macOS, `make install-launchd` installs but does not bootstrap the new
agent. Launchd has no per-agent `EnvironmentFile` equivalent. If the old agent
has a custom operator-token environment setting, configure the new agent
through the same secure operator-managed mechanism before bootstrap; do not
put the token in these commands. Then stop the old agent and bootstrap the new
one:

```bash
make install-launchd
launchctl bootout gui/$(id -u) "$HOME/Library/LaunchAgents/com.abevz.af-coordinatord.plist"
launchctl bootstrap gui/$(id -u) "$HOME/Library/LaunchAgents/com.abevz.dibsd.plist"
dibs doctor
```

The old unit files are not removed. The existing live database and socket
remain at their old paths until a separate, explicit migration. Do not copy
only the SQLite main file while WAL has uncheckpointed writes. There is no
`dibs migrate-paths` command in this slice.

## Manual daemon start

```bash
dibsd
```

Clean-install socket: `~/.local/state/dibs/dibsd.sock`
Clean-install database: `~/.local/share/dibs/dibs.db`
Existing legacy database and socket paths remain selected automatically.

## Auto-started daemon logs

`dibs init` and `dibs daemon start` detach `dibsd` when no matching daemon
is reachable. Its structured startup and mutation diagnostics go to
`$XDG_STATE_HOME/dibs/logs/dibsd-<socket-sha256>.log`, or
`~/.local/state/dibs/logs/dibsd-<socket-sha256>.log` when `XDG_STATE_HOME`
is unset. `<socket-sha256>` is the SHA-256 of the cleaned configured socket
path, so custom sockets keep separate logs. Startup errors print the exact
path and the last diagnostic line.

Each instance retains an active file and one previous `.log.1` file, each
at most **1 MiB** (2 MiB total); oversized records retain their final 1 MiB.
The log directory is private (0700), files are 0600, and a zero-length
`.log.lock` coordinates concurrent starters. Raw detached stdout/stderr are
discarded; structured daemon diagnostics are retained. No new
`<socket>.startup.log` is created in the socket/runtime directory.

After verifying the daemon's database and pid, `dibs daemon stop` removes
its owned `<socket>.pid` and legacy `<socket>.startup.log`. It preserves the
bounded state logs for diagnosis and refuses cleanup for an unverified
socket or a manager-owned daemon. Foreground `dibsd` still logs to stderr;
systemd uses the journal and launchd uses the paths documented above.

## Execution statistics

The daemon derives a local read-only report from its coordinator records; it
does not need Prometheus, a rollup database, or network access:

```bash
dibs stats --project afc --since 7d
dibs --json stats --project afc --since 24h
```

Use RFC 3339 or a positive Go duration for `--since`; `--until` accepts RFC
3339 and defaults to now. JSON includes the report version, window,
denominators, percentile sample sizes, and the legacy event-ordering cutoff.

### Agent and costly-issue views

```sh
dibs stats --by actor
dibs stats --top 10
dibs stats --project afc --since 7d --by actor --top 5 --json
```

Agent outcomes belong to the normalized claim owner, even for expiry/operator
releases. Use stable `DIBS_ACTOR`/holder labels and a separate claim `session_id`
for task/PID details; normalization and raw aliases are documented in
[schema-v1](schema-v1.md#derived-statistics). These are flow-window counts:
DONE/CLAIMS is not a cohort success rate. Cost ranks claims then completed
attempt time; ongoing elapsed time is excluded. `claim without progress` means
at least three exactly ordered plain releases without note/commit/PR evidence
inside their attempts, not a judgment about an agent. Legacy pairs stay
unclassified. JSON definitions are in [api-v1](api-v1.md#statistics).

## Interacting via curl

Since the daemon listens on a Unix socket, use `curl --unix-socket`:

```bash
# Health check
curl --unix-socket ~/.local/state/dibs/dibsd.sock http://localhost/v1/health

# Safety snapshot, including active/expired leases and top stale holders
curl --unix-socket ~/.local/state/dibs/dibsd.sock http://localhost/v1/stats

# Mutation logs on stderr include operation, result_code, status, latency_ms.
# Health safety.mutation_counters is process-local; stats.safety rejection
# counts are durable. A db_busy result keeps the existing internal_error API
# envelope and adds the X-Dibs-Result-Code: db_busy response header.

# Create a project
curl --unix-socket ~/.local/state/dibs/dibsd.sock \
  -X POST http://localhost/v1/projects \
  -H 'Content-Type: application/json' \
  -d '{"name":"Test","key":"test"}'
```

## Backup

The daemon uses SQLite in WAL mode. Online backup uses `VACUUM INTO`:
Use the database path reported by `dibs --json health`; the example below is for a
clean install. An existing legacy database stays at its original path.

### Manual backup

```bash
sqlite3 ~/.local/share/dibs/dibs.db \
  "VACUUM INTO '/path/to/backup/dibs-$(date +%Y%m%d).db'"
```

This creates a consistent, compacted copy of the database while the daemon is running.
Copying the main `.db` file alone while a live WAL exists is not a backup.
`dibsd` checks SQLite integrity and rejects unknown applied migrations before
serving requests. On either startup failure, keep the original DB/WAL/SHM
together and restore a verified backup or use a compatible binary; it does
not repair corrupt state in place.

### Automatic backup

`make install-backup` installs the native scheduler for the current OS:

- Linux: systemd user timer.
- macOS: launchd LaunchAgent.

The job runs `VACUUM INTO` daily at 03:17, checks the integrity of the backup,
and keeps the last 14 backups in `~/backups/dibs`.

#### Linux systemd timer

An automated backup script and systemd timer are provided in `contrib/systemd/`.
To install and enable them:

```bash
make install-backup
sh contrib/install/systemctl-user.sh enable --now af-coordinator-backup.timer
```

Logs are available with:

```bash
journalctl --user -u af-coordinator-backup.service
```

#### macOS launchd backup

Install the backup LaunchAgent:

```bash
make install-backup
launchctl print gui/$(id -u)/com.abevz.af-coordinator-backup
```

Run a backup immediately when needed:

```bash
launchctl kickstart -k gui/$(id -u)/com.abevz.af-coordinator-backup
```

Logs are written to:

```text
~/Library/Logs/af-coordinator-backup.log
~/Library/Logs/af-coordinator-backup.err.log
```

To uninstall:

```bash
make uninstall-backup
```

### Restore

Before stopping the daemon, record the active DB path from `dibs --json
health`. Keep the original DB and any WAL/SHM files together until the
restored daemon has been verified.

1. Stop the daemon:
   ```bash
   sh contrib/install/systemctl-user.sh stop dibsd
   ```
2. Verify the backup and replace the database:
   ```bash
   restore_verified() {
     backup=/path/to/backup/dibs-20260703.db
     db="$HOME/.local/share/dibs/dibs.db"
     [ -f "$backup" ] || { echo "backup missing: $backup" >&2; return 1; }
     integrity=$(sqlite3 -readonly "$backup" 'PRAGMA integrity_check') || return 1
     [ "$integrity" = ok ] || { echo "backup integrity failed: $integrity" >&2; return 1; }
     migrations=$(sqlite3 -readonly "$backup" 'SELECT count(*) FROM _migrations') || return 1
     [ "$migrations" -gt 0 ] 2>/dev/null || { echo 'backup migration ledger missing' >&2; return 1; }
     staged=$(mktemp "$(dirname "$db")/.dibs-restore.XXXXXX") || return 1
     cp "$backup" "$staged" && chmod 600 "$staged" || return 1
     hold=$(mktemp -d "$(dirname "$db")/pre-restore.XXXXXX") || return 1
     for suffix in '' -wal -shm; do
       [ ! -e "$db$suffix" ] || mv "$db$suffix" "$hold/$(basename "$db")$suffix" || return 1
     done
     mv "$staged" "$db" || return 1
     printf 'Previous DB/WAL/SHM retained in %s\n' "$hold"
   }
   restore_verified
   ```
   Set `db` to the path reported by `dibs --json
   health` before stopping; use the existing legacy path if that is active.
   Keep the old DB/WAL/SHM together in `hold` until verification succeeds.
   Do not restore into a second path or leave an old WAL beside the restored
   file.
3. Start the daemon:
   ```bash
   sh contrib/install/systemctl-user.sh start dibsd
   ```
4. Run `dibs doctor` and a read-only `dibs issue get <known-id>` to verify
   daemon revision, schema startup, and restored coordinator data.

## CLI usage

```bash
# List projects
dibs project list

# Create an issue
dibs issue create --project test --scope-kind project --title "My issue"

# List issues; project, type, and status accept CSV values
dibs ls --project afc --type epic,chore --status open,in_progress
dibs ls --project afc,aion --type epic,chore --status open,in_progress

# Dependency-aware table columns
dibs ls --project aion --status open

# Narrow terminal: pick only the columns you need, in any order
dibs ls --project aion --columns short,status,title

# Show the complete filter contract without contacting the daemon
dibs ls --help

# Claim, work, heartbeat, and close/handoff in one launcher
dibs issue run <issue-id> --ttl 900 -- ./do-the-work.sh

# Manual recovery only: set DIBS_LEASE_TOKEN_FILE to a private 0600 token
# file and pass the non-secret generation from the claim response.
dibs issue heartbeat <issue-id> --lease-generation <generation> --ttl 900
dibs issue release <issue-id> --lease-generation <generation>

# Ready view
dibs issue ready --project test

# Notes
dibs issue note add <issue-id> --author me --body "Working on this"
dibs issue note list <issue-id>
```

The human-readable issue list keeps task context together and puts dependency
state at the end of each row:

```text
ID SHORT STATUS TYPE TITLE ASSIGNEE CLAIMED BLOCKED BY DEPS TAGS
```

- `STATUS` includes `[B]` when an unfinished `blocks` dependency makes the
  issue non-ready.
- `BLOCKED BY` lists active blocker short IDs. A `done` or `cancelled`
  dependency is not shown as an active blocker.
- `DEPS` lists non-blocking relationships such as `parent:`, `related:`, and
  `discovered-from:`. Blocking relationships are shown in `BLOCKED BY` to
  avoid duplicating the same information. To list all child tasks belonging to a parent issue (e.g. `aion-500`), filter table output with `grep "parent:aion-500"`.
- `TAGS` lists the issue's namespaced tags, comma-joined.
- The full default row is wide; pass `--columns <key[,key...]>` (valid keys:
  `id`, `short`, `status`, `type`, `title`, `assignee`, `claimed`,
  `blocked_by`, `deps`, `tags`) to `dibs ls`/`issue list`/`issue ready` to
  select a narrower subset, in any order. Omitting the flag keeps this
  default column set.

With `--json`, the issue object exposes the same derived state as optional
`blocked` and `blocked_by` fields, while `dependencies` retains the complete
relationship list. To query children of `aion-500` from JSON output:
`dibs issue list --json | jq -r '.[] | select(.dependencies[]? | .kind == "parent" and (.depends_on_short_id == "aion-500" or .depends_on_id == "aion-500"))'`

## Live watch

`dibs watch --project <key>` opens a read-only terminal board with ready issues,
active lease holders and remaining time, blocked issues, and recent activity.
Without `--project`, the board starts with a summary for every project;
projects with no open, blocked, or in-progress issues sort last. The counts
use the same status definitions as `dibs stats` (ready is a subset of open).
Last event is the timestamp of the latest retained issue event by sequence,
not the maximum wall-clock time. Unknown history displays `unknown`.
Use ↑/↓ (or j/k) and Enter to open a project then an issue. The project list
labels ready, active, blocked and stale rows; details show description,
dependencies and the last five notes with scrolling. Esc/Backspace returns.
STALE includes in-progress issues without an active lease and expired leases.
Age is labelled since expiry when the API exposes it, otherwise since update
(the daemon hides expired lease metadata). Deferred/terminal issues are excluded.
Clock updates move an expiring lease to STALE without coordinator writes.
`--once` prints all summary and lane rows with one final newline.
Press `r` to refresh or `q` to quit. Between full snapshots the board
long-polls the event cursor (one indexed read every few seconds) and reloads
only when a coordinator event arrives, just after the earliest active lease
expires, or after `--interval` (default `5m`, minimum `2s`) for changes that
emit no event, such as a newly registered project. An idle board therefore
does not re-read the database. It marks
the last complete view stale if the daemon becomes unavailable. Use
`dibs watch --project <key> --once` for a text snapshot or add `--json` for a
machine-readable snapshot. Watch reads the daemon API and does not claim work.
For an active lease started by `dibs issue run`, Active leases shows the PID of
the supervising `dibs` process and its hostname. A new manual `dibs issue
claim` shows a caller ancestor PID and hostname when available. That caller
can exit while the lease remains active. The values come from the claimant's
session ID and are **self-reported**, not verified by the daemon; another local
client can report the same format. An explicit `--session-id` overrides the
automatic manual-claim value. `PID ?` means process metadata is unavailable,
as with older claims or other clients. `--json` exposes `lease_pid` and
`lease_host` when present. PIDs can be reused, and lease ownership still
requires the token and generation.
The installed `dibs` and running `dibsd` must both include the watch endpoint;
restart an older daemon explicitly after upgrading it.

## Moving a registered repository

Keep the old path reachable while updating dibs (a temporary symlink is
sufficient). Move the whole repository parent so its registered worktrees keep
the same relative paths, then run:

```sh
dibs repo relocate --repo <repository-id> --new-path /absolute/new/path/main/.git
dibs repo list --project <project-key>
dibs worktree list --repo <repository-id>
git -C /absolute/new/path/main worktree repair /absolute/new/path/main
```

`--new-path` names the new location of the **registered canonical path**. For
registrations created by `dibs init`, that is normally the Git common directory
(`.git` or `.bare`); older registrations may name the main checkout instead.
For an older checkout-path registration, use `--new-path /absolute/new/path/main`.
For a repository with linked worktrees, pass **every moved worktree path** to
`git worktree repair` as additional arguments. Git stores absolute pointers in
linked worktrees' `.git` files; these must be repaired before removing the old
path. Verify `git -C <each-new-worktree> rev-parse --git-common-dir` after
removing the temporary symlink. The daemon verifies the old and new Git object
store and each mapped worktree before changing SQLite. If a worktree was
deleted or lives outside the moved parent, resolve its registration explicitly
and retry. The command prints an operation ID; supply it with
`--operation-id` to retry an uncertain request without making a second change.
Then check `dibs doctor`.

## Agent guidance sync

`dibs protocol` is the canonical detailed agent workflow. `dibs init`
updates only the managed coordinator block in one target `AGENTS.md` (the
current directory by default, or `--path`), preserving all surrounding
repository instructions. It is not a global fan-out command.

After a protocol-summary template update, check each registered checkout first:

```bash
dibs init --dry-run
dibs init
```

The generated block points agents back to `dibs protocol` and the canonical
`docs/agent-protocol-v1.md`; it intentionally does not duplicate the full
workflow in every repository.

## Data locations

| Resource | Path |
|----------|------|
| Database | `~/.local/share/dibs/dibs.db` |
| Socket | `~/.local/state/dibs/dibsd.sock` |
| Logs | `journalctl --user -u dibsd` |

## Configuration

Environment variables override defaults:

- `DIBS_DB` — database path
- `DIBS_SOCKET` — socket path

## Legacy alias removal (afc-142)

Before installing the canonical-only release, migrate consumer commands from
`afctl` / `afc-mcp` / `af-coordinatord` to `dibs` / `dibs-mcp` / `dibsd` and
configuration to `DIBS_*` names (see CHANGELOG.md for the exact mapping).
`issue run` child scripts must use the DIBS lifecycle environment variables.
Update any operator-token EnvironmentFile key to `DIBS_OPERATOR_TOKEN` while
preserving its private value and mode 0600; do not print tokens. A later
explicit service switch loads that environment. Existing database/socket
paths are still discovered; alias removal does not migrate runtime data.

Clean installs create only canonical binaries. Source and release installers
remove old aliases only if they are symlinks targeting the corresponding
canonical binary; unrelated files and symlink targets are preserved.
The legacy daemon unit template is no longer shipped. Retire an installed
`af-coordinatord.service` only after confirming it is inactive and disabled
and the `dibsd` service uses the same canonical database. Remove the obsolete
`~/github/af-coordinator` symlink only after repository relocation and consumer
path migration are verified. These are operator steps, not automatic service
or path mutations by the installer. Backup unit names are retained.

## Release updates and rollback

Run `dibs update --check` for a read-only release comparison and intervening
CHANGELOG (breaking changes first). Exit 0 means current, 10 means an update is
available, and 1 means the check failed. Stable builds stay on stable releases;
prerelease builds include prereleases, or pass `--prerelease` explicitly.

Migrate consumers before accepting a breaking release. `dibs update` verifies
`dibs_OS_ARCH.tar.gz` using `checksums.txt`, then publishes `dibs`, `dibsd`, and
`dibs-mcp` together in the existing installation directory. It retains the
previous binaries. `--yes` accepts breaking changes only; daemon restart needs
its own confirmation or `--restart`. With no restart, the current daemon keeps
running its old version and `dibs doctor` reports CLI/daemon mismatch. Doctor
also reports a newer release from the local cache, without network.

If the new binaries or an explicitly requested restart fail:

```sh
dibs update --rollback           # offline binary rollback; daemon still running
dibs update --rollback --restart # also explicitly restart the managed daemon
```

The shared `.dibs-update/current` pointer switches all three public binary
paths atomically. Each verified generation records its rollback predecessor;
a failed later update cannot discard that target. Rollback restores binaries,
not database contents or schema. Neither command opens coordinator storage or
changes database/socket paths or service configuration. For Homebrew, use its
upgrade/rollback procedures. A source build reporting `dev` needs a published
release before using the updater.

Release checks run only in explicit `dibs update` commands or the daemon's
bounded background refresh, at most once per 24 hours including failed/offline
attempts. There is no background auto-upgrade. Cache and the daily notice
ledger live in `$XDG_STATE_HOME/dibs/update.json` (default
`~/.local/state/dibs/update.json`), separate from the database. Terminal commands
use cached results only; a notice follows normal output, or appears in the
interactive watch footer (`--once` uses stderr). JSON/hooks/MCP/issue-run
children suppress notices. To opt out,
set `DIBS_NO_UPDATE_NOTIFIER=1` in the CLI environment (and daemon environment to
disable its background refresh too).
