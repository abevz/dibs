# Deferred upgrade for migration 0012

Do not execute this runbook as part of the PR/merge. Live `dibsd`, `dibs`,
and `dibs-mcp` remain on their existing version until a **separate explicit
owner command** authorizes this update. Queue emptiness alone is not approval.
All commands below are operator steps for that later maintenance window.

## 1. Quiesce dq and verify the gate

Pause new agent dispatch/queue producers for dq using their existing operator
controls, then allow running agents to finish and publish their handoffs. Keep
dispatch paused through verification or rollback. This prevents a claim racing
the empty-queue check; do not force-release an active agent to satisfy it.

Both commands must return an empty list:

```sh
dibs issue ready --project dq --tag queue/v2 --json
dibs issue ready --project dq --tag queue/rigid --json
```

Additionally dq must have **no active leases**, including issues not in either
queue. Use the existing CLI, paginate all dq issues, and fail closed on a
malformed response or an unparseable lease deadline. Safety checks use explicit
errors and remain enabled under `PYTHONOPTIMIZE`:

```sh
python3 - <<'PY'
import datetime, json, subprocess
now = datetime.datetime.now(datetime.timezone.utc)
for tag in ('queue/v2', 'queue/rigid'):
    ready = json.loads(subprocess.check_output(
        ['dibs', 'issue', 'ready', '--project', 'dq', '--tag', tag, '--json']))
    if not isinstance(ready, list) or ready:
        raise SystemExit(f'{tag} is not empty or response is malformed')
offset = 0
while True:
    issues = json.loads(subprocess.check_output(
        ['dibs', 'issue', 'list', '--project', 'dq', '--limit', '1000',
         '--offset', str(offset), '--json']))
    if not isinstance(issues, list):
        raise SystemExit('unexpected issue-list response')
    for issue in issues:
        if not isinstance(issue, dict):
            raise SystemExit('issue row is not an object')
        for field in ('id', 'short_id', 'status'):
            if not isinstance(issue.get(field), str) or not issue[field].strip():
                raise SystemExit(f'missing or invalid issue field: {field}')
        if issue['status'] not in ('open', 'in_progress', 'blocked', 'deferred', 'done', 'cancelled'):
            raise SystemExit('unknown issue status')
        holder = issue.get('holder', '')
        deadline = issue.get('lease_expires_at', '')
        if not isinstance(holder, str) or not isinstance(deadline, str):
            raise SystemExit('invalid lease field types')
        if bool(holder.strip()) != bool(deadline.strip()):
            raise SystemExit('incomplete lease metadata')
        if deadline:
            expiry = datetime.datetime.fromisoformat(deadline.replace('Z', '+00:00'))
            if expiry.tzinfo is None:
                raise SystemExit('lease expiry is missing a timezone')
            if expiry > now:
                raise SystemExit(f"active dq lease: {issue['short_id']}")
        elif issue['status'] == 'in_progress':
            raise SystemExit(f"reconcile lease-less in_progress issue: {issue['short_id']}")
    if len(issues) < 1000:
        break
    offset += len(issues)
print('PASS: both queues empty and no active dq leases')
PY
```

Also coordinate maintenance with any other project using the same daemon;
SQLite migration requires exclusive daemon ownership. Save the pre-update
`dibs health --json` and record its database path, socket, version/revision,
and `safety.latest_migration`. Use that actual database path: a legacy install
may use `~/.local/share/af-coordinator/af-coordinator.db`.

## 2. Back up before updating

Use a durable private directory, outside Git and `/tmp`. Set the actual database
path from health, the approved merged SHA, and the directory containing all
three installed commands. Verify the commands resolve to that directory before
proceeding; reconcile duplicate PATH installations rather than mixing versions.

```sh
set -eu
umask 077
backup_dir="$HOME/.local/state/dibs/backups/pre-0012-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$backup_dir/bin"
dibs health --json > "$backup_dir/health-before.json"
db_path="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["db_path"])' "$backup_dir/health-before.json")"
bin_dir="$HOME/.local/bin"  # confirm against command -v for all three below
for executable in dibsd dibs dibs-mcp; do
  test "$(command -v "$executable")" = "$bin_dir/$executable"
  cp -L "$bin_dir/$executable" "$backup_dir/bin/$executable"
done
dibs export jsonl > "$backup_dir/export-before.jsonl"
printf '%s\n' "$db_path" > "$backup_dir/database-path.txt"
python3 - "$db_path" "$backup_dir/database.sqlite" <<'PY'
import pathlib, sqlite3, sys
# A consistent standalone copy of the SQLite database file, including any
# committed WAL pages. Never use plain cp on a running WAL database.
source = sqlite3.connect(pathlib.Path(sys.argv[1]).resolve().as_uri() + '?mode=ro', uri=True)
target = sqlite3.connect(sys.argv[2])
try:
    source.backup(target)
    if target.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
        raise SystemExit('SQLite backup integrity check failed')
finally:
    target.close()
    source.close()
PY
sha256sum "$backup_dir/database.sqlite" "$backup_dir/export-before.jsonl" \
  "$backup_dir/bin/dibsd" "$backup_dir/bin/dibs" "$backup_dir/bin/dibs-mcp" \
  > "$backup_dir/SHA256SUMS"
sha256sum -c "$backup_dir/SHA256SUMS"
```

Do not print or copy operator/lease tokens into these reports. JSONL is a useful
logical export; the consistent SQLite file is the rollback source. If evidence
already exists in durable state, back up that directory separately; JSONL and
SQLite contain evidence metadata, not file bytes. Re-run the gate immediately
before stopping, with dispatch still paused. If it fails, stop this procedure.

## 3. Stop, build/install one version, then start

Use the reviewed merged checkout in `~/github/dibs/main`, already advanced to
the approved merge SHA. **Do not run an ordinary pull/merge there**: the repo's
`post-merge` hook installs and restarts active services. If advancement is
needed, first fetch, then explicitly use
`git -c core.hooksPath=/dev/null merge --ff-only origin/main`; verify the result
matches the separately approved SHA. Do not change the global hook setting.

After the backup and final gate:

```sh
set -eu
cd "$HOME/github/dibs/main"
approved_sha='<owner-approved full merge SHA>'
test "$(git rev-parse HEAD)" = "$approved_sha"
test -z "$(git status --porcelain)"
sh contrib/install/systemctl-user.sh stop dibsd
# Stop and investigate if the unit is not inactive or its daemon remains alive.
test "$(sh contrib/install/systemctl-user.sh show dibsd -p ActiveState --value)" = inactive
make build-install BINDIR="$bin_dir" GIT_VERSION="$(git describe --tags --always)"
# make build-install builds dibsd, dibs and dibs-mcp from this same checkout.
sh contrib/install/systemctl-user.sh start dibsd
dibs doctor
dibs health --json > "$backup_dir/health-after.json"
python3 - "$backup_dir/health-after.json" "$approved_sha" <<'PY'
import json, sys
health = json.load(open(sys.argv[1]))
if health['status'] != 'ok':
    raise SystemExit('daemon health is not ok')
if health['revision'] != sys.argv[2]:
    raise SystemExit('wrong running revision')
if health['safety']['latest_migration'] != '0012_issue_evidence.sql':
    raise SystemExit('migration 0012 not confirmed')
print('PASS: reviewed revision running with migration 0012')
PY
```

`dibs health` queries the running daemon. A successful startup runs
`VerifyKnownMigrations` and applies missing migrations before serving requests;
the health revision and latest migration must both match. Check service logs if
startup fails. Reconnect existing MCP clients (`/mcp` → Reconnect, or a new
session) so resident `dibs-mcp` processes use the installed binary. Recheck dq
and resume dispatch only after doctor/health and the owner accept the update.
Do not use `make restart-service`: it installs before stopping and obscures the
required order.

## 4. Roll back database and all three binaries together

Keep dispatch paused. Execute each shell block with its own `set -eu`; a failed
stop or inactive-state check must prevent every subsequent file operation.
An old binary refuses a database whose ledger contains
0012, reporting `unknown applied migration "0012_issue_evidence.sql"` from
`VerifyKnownMigrations`. Replacing only the binary is not a rollback. Do not
remove the migration row or drop the evidence table in place.

```sh
set -eu
sh contrib/install/systemctl-user.sh stop dibsd
test "$(sh contrib/install/systemctl-user.sh show dibsd -p ActiveState --value)" = inactive
sha256sum -c "$backup_dir/SHA256SUMS"
# Preserve the failed-upgrade DB and sidecars for inspection; never combine
# old backup bytes with a WAL from the upgraded database.
failed_dir="$backup_dir/failed-upgrade-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$failed_dir"
for suffix in '' -wal -shm; do
  if [ -e "$db_path$suffix" ]; then
    mv "$db_path$suffix" "$failed_dir/$(basename "$db_path")$suffix"
  fi
done
cp "$backup_dir/database.sqlite" "$db_path"
chmod 600 "$db_path"
for executable in dibsd dibs dibs-mcp; do
  cp "$backup_dir/bin/$executable" "$bin_dir/$executable"
  chmod 755 "$bin_dir/$executable"
done
sh contrib/install/systemctl-user.sh start dibsd
dibs doctor
dibs health --json > "$backup_dir/health-rollback.json"
```

Compare rollback health/version/revision/database path/latest migration with
`health-before.json`; reconnect MCP clients again. Preserve the failed database,
export, and any new evidence files. Restoring a backup discards subsequent
coordinator writes, which is why agents and producers stay quiescent throughout
the window. Resume only after verification and the owner's decision.

Migration 0012 only creates `issue_evidence` and its index; it changes no prior
tables/rows. The SQL is applied once transactionally through `Migrate` and its
ledger; raw `CREATE TABLE` statements are not intended to be manually replayed.
`TestEvidenceMigrationPreservesLegacyDataAndRejectsOldBinary` verifies legacy
schema/data preservation, repeat startup and rejection by the prior migration
set. These checks do not authorize or perform this live update.
