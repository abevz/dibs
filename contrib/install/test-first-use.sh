#!/bin/sh
set -eu

bin_dir=$1
scratch=$(mktemp -d)
export HOME="$scratch/home"
export DIBS_DB="$scratch/data/dibs.db"
export DIBS_SOCKET="$scratch/state/dibsd.sock"
unset AF_COORDINATOR_DB AF_COORDINATOR_SOCKET DIBS_OPERATOR_TOKEN AF_OPERATOR_TOKEN
mkdir -p "$HOME" "$scratch/repo"
managed_pid=
cleanup() {
  if [ -n "$managed_pid" ]; then
    kill "$managed_pid" 2>/dev/null || true
    wait "$managed_pid" 2>/dev/null || true
  fi
  "$bin_dir/dibs" daemon stop >/dev/null 2>&1 || true
  rm -rf "$scratch"
}
trap cleanup EXIT HUP INT TERM

git -C "$scratch/repo" init -b main >/dev/null
cd "$scratch/repo"
"$bin_dir/dibs" init --dry-run >"$scratch/dry-run.log"
test ! -e "$DIBS_DB"
test ! -e AGENTS.md

# A real init must start dibsd with no pre-existing socket or database.
"$bin_dir/dibs" init >"$scratch/init.log"
test -S "$DIBS_SOCKET"
test -s "$DIBS_SOCKET.pid"
"$bin_dir/dibs" init >"$scratch/init-repeat.log"
grep -q 'Instructions unchanged' "$scratch/init-repeat.log"
"$bin_dir/dibs" issue create --project repo --scope-kind project --title 'First-use smoke' >"$scratch/create.log"
"$bin_dir/dibs" issue claim repo-1 --holder first-use-smoke >"$scratch/claim.log"
"$bin_dir/dibs" daemon stop
test ! -S "$DIBS_SOCKET"

# Two simultaneous starters must converge on the same database writer.
"$bin_dir/dibs" daemon start >"$scratch/start-1.log" 2>&1 &
first=$!
"$bin_dir/dibs" daemon start >"$scratch/start-2.log" 2>&1 &
second=$!
wait "$first"
wait "$second"
test -S "$DIBS_SOCKET"
test -s "$DIBS_SOCKET.pid"
"$bin_dir/dibs" daemon stop
test ! -S "$DIBS_SOCKET"
"$bin_dir/dibs" issue get repo-1 >"$scratch/restarted.log"
grep -q 'repo-1' "$scratch/restarted.log"
"$bin_dir/dibs" daemon stop

# A service-managed/foreground instance has no dibs-owned pid file.
"$bin_dir/dibsd" >"$scratch/managed.log" 2>&1 &
managed_pid=$!
attempt=0
until "$bin_dir/dibs" health >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  test "$attempt" -lt 50
  sleep 0.1
done
test ! -e "$DIBS_SOCKET.pid"
if "$bin_dir/dibs" daemon stop >"$scratch/managed-stop.log" 2>&1; then
  echo 'daemon stop unexpectedly signalled a non-dibs-owned daemon' >&2
  exit 1
fi
kill -0 "$managed_pid"
kill "$managed_pid"
wait "$managed_pid"
managed_pid=
