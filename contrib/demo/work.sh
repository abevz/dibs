#!/usr/bin/env bash
# Simulated work for one demo task. Completion is signalled only when the
# task "passes"; the first attempt at demo-3 fails and hands the task off.
set -u
id=$1
case $id in
demo-1) sleep 6 ;;
demo-2) sleep 4 ;;
demo-3)
	if [ ! -e "$DEMO_DIR/demo-3.tried" ]; then
		touch "$DEMO_DIR/demo-3.tried"
		sleep 3
		exit 1
	fi
	sleep 3
	;;
*) sleep 2 ;;
esac
dibs hooks complete
