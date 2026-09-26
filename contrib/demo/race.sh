#!/usr/bin/env bash
# Run the README demo: two simulated agents race for one ready queue while
# `dibs watch` shows the board. All state is temporary and isolated from any
# existing dibs installation. Requires bash, git, jq, tmux, and Go (unless
# DIBS_BIN_DIR points at prebuilt dibs/dibsd binaries).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

DEMO_DIR=$(mktemp -d "${TMPDIR:-/tmp}/dibs-demo.XXXXXX")
export DEMO_DIR
cleanup() {
	tmux -L dibs-demo kill-server 2>/dev/null || true
	command -v dibs >/dev/null && dibs daemon stop >/dev/null 2>&1 || true
	rm -rf "$DEMO_DIR"
	if [ -n "${DIBS_SOCKET:-}" ]; then rm -f "$DIBS_SOCKET" "$DIBS_SOCKET".*; fi
	echo "dibs demo cleaned up"
}
trap cleanup EXIT

# Build before HOME is replaced so Go keeps using the normal module cache.
if [ -n "${DIBS_BIN_DIR:-}" ]; then
	bin=$DIBS_BIN_DIR
else
	bin="$DEMO_DIR/bin"
	(cd "$root" && go build -o "$bin/dibs" ./cmd/dibs && go build -o "$bin/dibsd" ./cmd/dibsd)
fi
export PATH="$bin:$PATH"

# Unix socket paths are limited to ~104 bytes, so keep this one short.
export DIBS_SOCKET="${XDG_RUNTIME_DIR:-/tmp}/dibs-demo-$$.sock"
export DIBS_DB="$DEMO_DIR/dibs.db"
export HOME="$DEMO_DIR/home"
export DIBS_ACTOR=owner
mkdir -p "$HOME"

repo="$DEMO_DIR/demo"
git init -q "$repo"
git -C "$repo" -c user.name=demo -c user.email=demo@example.invalid commit -q --allow-empty -m init
cd "$repo"
dibs init --project demo >/dev/null

new() { dibs issue create --project demo --scope-kind project --priority "$1" --title "$2" >/dev/null; }
new 1 "Add retry to HTTP client"   # demo-1: both agents want this one
new 2 "Write CLI guide"            # demo-2
new 2 "Fix flaky login test"       # demo-3: first attempt fails
new 3 "Document API errors"        # demo-4: blocked by demo-2
dibs issue dependency add demo-4 --blocked-by demo-2 >/dev/null

# Agents pick their first task, then wait for this file so both claim
# demo-1 at the same moment.
(sleep "${DEMO_GO_DELAY:-3}"; touch "$DEMO_DIR/go") &

t="tmux -L dibs-demo -f /dev/null"
# Quitting the board (q) ends the demo; agent panes stay until then.
hold="while [ ! -e '$DEMO_DIR/done' ]; do sleep 0.2; done"
$t new-session -d -s demo -x "${COLUMNS:-120}" -y "${LINES:-40}" \
	"dibs watch --project demo; touch '$DEMO_DIR/done'"
$t set -g status off
$t set -g pane-border-status top
$t set -g pane-border-format ' #{pane_title} '
$t set -g pane-border-style fg=colour240
$t set -g pane-active-border-style fg=colour240
$t select-pane -t demo:0.0 -T 'dibs watch'
$t split-window -v -t demo:0.0 -l 10 "$here/agent.sh claude; $hold"
$t select-pane -T 'agent: claude'
$t split-window -h -t demo:0.1 "$here/agent.sh codex; $hold"
$t select-pane -T 'agent: codex'
$t select-pane -t demo:0.0
$t attach -t demo
