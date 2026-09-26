#!/usr/bin/env bash
# Simulated agent for the demo: repeatedly take the top ready task through
# `dibs issue run --require-complete`, and report what happened.
set -u
name=$1
here=$(cd "$(dirname "$0")" && pwd)

green=$'\e[32m' red=$'\e[31m' yellow=$'\e[33m' dim=$'\e[2m' bold=$'\e[1m' off=$'\e[0m'

top() { dibs --json issue ready --project demo | jq -r '.[0] | select(.) | "\(.short_id)\t\(.title)"'; }

printf '%swaiting for the queue…%s\n' "$dim" "$off"
next=$(top)
while [ ! -e "$DEMO_DIR/go" ]; do sleep 0.05; done

while :; do
	[ -n "$next" ] || next=$(top)
	if [ -z "$next" ]; then
		printf '%squeue empty, stopping%s\n' "$dim" "$off"
		break
	fi
	id=${next%%$'\t'*} title=${next#*$'\t'}
	next=
	printf '%s→ claim %s%s  %s\n' "$bold" "$id" "$off" "$title"
	dibs issue run "$id" --actor "$name" --require-complete -- "$here/work.sh" "$id" \
		>"$DEMO_DIR/$name.log" 2>&1
	case $? in
	0) printf '  %s✓ %s done, closed%s\n' "$green" "$id" "$off" ;;
	3)
		holder=$(dibs --json issue get "$id" | jq -r '.issue.holder // "another agent"')
		printf '  %s✗ %s already leased by %s%s → next\n' "$red" "$id" "$holder" "$off"
		;;
	*)
		printf '  %s↩ %s tests failed → HANDOFF, requeued%s\n' "$yellow" "$id" "$off"
		sleep 3
		;;
	esac
done
