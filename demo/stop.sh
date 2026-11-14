#!/bin/sh
# Stops the demo's servers and removes the folder start.sh made.
set -eu

run=${REHEARSAL_DEMO_DIR:-$HOME/rehearsal-demo}
case "$run" in
"$HOME"/*) shown="~${run#"$HOME"}" ;;
*) shown=$run ;;
esac

if [ ! -e "$run" ]; then
	echo "The demo is not running: there is no $shown."
	exit 0
fi
if [ ! -f "$run/.rehearsal-demo" ]; then
	echo "$shown was not made by demo/start.sh, so it is left alone."
	exit 1
fi
if [ -f "$run/server.pid" ]; then
	pid=$(cat "$run/server.pid")
	if kill -0 "$pid" 2>/dev/null; then
		kill "$pid"
	fi
fi
rm -rf "$run"
echo "The demo is stopped and $shown is gone."
