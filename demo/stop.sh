#!/bin/sh
# Stops the demo's servers and removes everything start.sh made.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
run="$here/.run"

if [ -f "$run/server.pid" ]; then
	pid=$(cat "$run/server.pid")
	if kill -0 "$pid" 2>/dev/null; then
		kill "$pid"
	fi
fi
rm -rf "$run"
echo "The demo is stopped and demo/.run is gone."
