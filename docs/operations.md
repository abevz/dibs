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
Press `r` to refresh or `q` to quit. It refreshes every two seconds and marks
the last complete view stale if the daemon becomes unavailable. Use
`dibs watch --project <key> --once` for a text snapshot or add `--json` for a
machine-readable snapshot. Watch reads the daemon API and does not claim work.
The installed `dibs` and running `dibsd` must both include the watch endpoint;
restart an older daemon explicitly after upgrading it.

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
