#!/usr/bin/env bash
# check-lease.sh — PreToolUse hook for dibs lease enforcement
#
# Modes (via DIBS_HOOK_MODE env):
#   warn  (default) — print warning, allow mutation
#   block           — exit 2, block mutation
#
# Environment:
#   DIBS_ACTOR     — agent identity (required)
#   DIBS_HOOK_MODE — warn (default) | block
#   DIBS_PATH      — path to dibs (default: dibs in PATH)

set -eo pipefail

ACTOR="${DIBS_ACTOR-${AF_COORDINATOR_ACTOR:-}}"
MODE="${DIBS_HOOK_MODE-${AF_HOOK_MODE:-warn}}"
DIBS="${DIBS_PATH-${AFCTL_PATH:-dibs}}"

if [ -z "$ACTOR" ]; then
  echo "[check-lease] WARNING: DIBS_ACTOR is not set — skipping lease check" >&2
  exit 0
fi

# Quick positive cache: /tmp/dibs-lease-ok-<actor>-<repo>
REPO_HASH=$(git rev-parse --show-toplevel 2>/dev/null | md5sum 2>/dev/null | cut -d' ' -f1 || echo "unknown")
CACHE_KEY="/tmp/dibs-lease-ok-${ACTOR}-${REPO_HASH}"

if [ -f "$CACHE_KEY" ] && [ "$(cat "$CACHE_KEY" 2>/dev/null)" = "1" ]; then
  exit 0
fi

# Query active leases
OUTPUT=$("$DIBS" issue list --json --status in_progress 2>/dev/null || true)

if echo "$OUTPUT" | grep -q '"holder" : "'"$ACTOR"'"' 2>/dev/null; then
  # Cache positive result for 60s
  echo "1" > "$CACHE_KEY"
  (sleep 60 && rm -f "$CACHE_KEY" 2>/dev/null) &
  exit 0
fi

case "$MODE" in
  block)
    echo "[check-lease] BLOCKED: $ACTOR has no active lease on this repo. Run 'dibs issue claim' first." >&2
    exit 2
    ;;
  warn|*)
    echo "[check-lease] WARNING: $ACTOR has no active lease — continuing anyway (set DIBS_HOOK_MODE=block to enforce)" >&2
    exit 0
    ;;
esac
