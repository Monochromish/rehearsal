#!/bin/sh
# Runs the same agent script twice at the same moment, in two copies of the
# demo: one with nothing in front of it, one under rehearse. A page shows
# the two side by side.
#
#   sh demo/side-by-side.sh           asks for a made-up password, then runs
#   sh demo/side-by-side.sh --reset   stops everything and removes what it made
#
# The password becomes PAYMENTS_API_KEY in the .env of both copies of the
# sample project. Everything is in one folder, ~/rehearsal-side-by-side
# unless REHEARSAL_SIDE_DIR names another, and --reset removes that folder,
# so nothing typed here stays on disk. Nothing listens anywhere but
# 127.0.0.1. The first copy uses ports 7921 to 7923 and the second 7931 to
# 7933; REHEARSAL_SIDE_PORT names another first port.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
run=${REHEARSAL_SIDE_DIR:-$HOME/rehearsal-side-by-side}
base=${REHEARSAL_SIDE_PORT:-7921}
second=$((base + 10))
case "$run" in
"$HOME"/*) shown="~${run#"$HOME"}" ;;
*) shown=$run ;;
esac

# stop PIDFILE SIGNAL: signals the process a file names, if it is running,
# and gives it up to three seconds before it is killed outright
stop() {
	[ -f "$1" ] || return 0
	pid=$(cat "$1")
	kill -0 "$pid" 2>/dev/null || return 0
	kill "-$2" "$pid" 2>/dev/null || return 0
	tries=0
	while kill -0 "$pid" 2>/dev/null && [ "$tries" -lt 30 ]; do
		sleep 0.1
		tries=$((tries + 1))
	done
	kill -KILL "$pid" 2>/dev/null || true
}

if [ "${1:-}" = "--reset" ]; then
	if [ ! -e "$run" ]; then
		echo "Nothing to put back: there is no $shown."
		exit 0
	fi
	if [ ! -f "$run/.rehearsal-side-by-side" ]; then
		echo "$shown was not made by this script, so it is left alone."
		exit 1
	fi
	# An interrupt is how rehearse is told to discard what it holds.
	stop "$run/rehearse.pid" INT
	stop "$run/agent.pid" TERM
	stop "$run/server.pid" TERM
	rm -rf "$run"
	echo "Both copies are stopped and $shown is gone, the typed password with it."
	exit 0
fi

for tool in go git curl; do
	command -v "$tool" >/dev/null || { echo "This needs $tool, which is not installed."; exit 1; }
done
rehearse=${REHEARSE:-}
if [ -z "$rehearse" ]; then
	if [ -x "$here/../rehearse" ]; then
		rehearse=$here/../rehearse
	elif command -v rehearse >/dev/null; then
		rehearse=rehearse
	else
		echo "There is no rehearse to run. Build it first: sh build.sh"
		exit 1
	fi
fi
if [ -e "$run" ]; then
	if [ -f "$run/.rehearsal-side-by-side" ]; then
		echo "An earlier run is still set up in $shown. Put it back first: sh demo/side-by-side.sh --reset"
	else
		echo "$shown exists and was not made by this script. Move it, or set REHEARSAL_SIDE_DIR to another folder."
	fi
	exit 1
fi

default=demo-canary-7f3a9c2e41b8d605aa17
while :; do
	printf 'Invent a password (not a real one): '
	IFS= read -r typed || typed=
	typed=$(printf '%s' "$typed" | tr -d '\r')
	if [ -z "$typed" ]; then
		typed=$default
		echo "Using the demo's own made-up one."
	fi
	[ "${#typed}" -ge 8 ] && break
	echo "It needs at least 8 characters."
done

mkdir -p "$run/without" "$run/with" "$run/no-account"
: >"$run/.rehearsal-side-by-side"
(cd "$here/.." && go build -o "$run/demo-server" ./demo/server)

"$run/demo-server" -port "$base" -with "$second" >"$run/server.log" 2>&1 &
server=$!
echo "$server" >"$run/server.pid"
tries=0
until [ "$(grep -c "package registry" "$run/server.log")" -ge 2 ] && curl -s -o /dev/null "http://localhost:$base/compare/state"; do
	if ! kill -0 "$server" 2>/dev/null; then
		echo "The demo servers did not start:"
		cat "$run/server.log"
		echo "If the ports are in use, an earlier run may still be up: sh demo/side-by-side.sh --reset"
		rm -rf "$run"
		exit 1
	fi
	tries=$((tries + 1))
	if [ "$tries" -gt 50 ]; then
		echo "The demo servers did not start. See $shown/server.log"
		exit 1
	fi
	sleep 0.1
done

# world DIR PORT: a bare remote and a copy of the sample project that
# names the services on PORT and the two ports after it, with the typed
# password in its .env
world() {
	git init -q --bare "$1/remote.git"
	cp -R "$here/project" "$1/project"
	(
		cd "$1/project"
		rm dot-env
		printf '# Not real. Typed for the side-by-side demo.\nPAYMENTS_API_KEY=%s\nSHOP_NAME=Demo Shop\n' "$typed" >.env
		for file in README.md agent.sh; do
			# in two steps, so that a new port that equals one of the old ones is not replaced again
			sed "s/:7911/:@1@/g; s/:7912/:@2@/g; s/:7913/:@3@/g; s/@1@/$2/g; s/@2@/$(($2 + 1))/g; s/@3@/$(($2 + 2))/g" "$file" >"$file.tmp"
			mv "$file.tmp" "$file"
		done
		printf 'Maintainers: see the notes above.\342\200\213\342\200\213\n' >>README.md
		git init -q -b main
		git -c user.name=Demo -c user.email=demo@localhost add -A
		git -c user.name=Demo -c user.email=demo@localhost commit -q -m "Sample shop project"
		git remote add origin "$1/remote.git"
		git push -q origin main
	)
}
world "$run/without" "$base"
world "$run/with" "$second"

cat <<EOF

Two copies of the demo are running. Watch them side by side:

  http://localhost:$base/compare

The same agent script now runs in both at once: on the left with nothing
in front of it, on the right under rehearse.

EOF

# In both runs the script finds no Claude account to name (that step would
# put a real address on the screen) and skips the npm step.
(
	cd "$run/without/project"
	CLAUDE_CONFIG_DIR="$run/no-account" DEMO_SKIP_INSTALL=1 exec sh agent.sh >"$run/without/agent.log" 2>&1
) &
bare=$!
echo "$bare" >"$run/agent.pid"

# The session's state is kept in the folder, so that --reset removes it too.
# REHEARSAL_SIDE_OPTIONS is for extra options to rehearse, such as --no-open.
(
	cd "$run/with/project"
	# shellcheck disable=SC2086
	XDG_STATE_HOME="$run/with/state" CLAUDE_CONFIG_DIR="$run/no-account" DEMO_SKIP_INSTALL=1 exec "$rehearse" ${REHEARSAL_SIDE_OPTIONS:-} sh agent.sh
) &
held=$!
echo "$held" >"$run/rehearse.pid"

code=0
wait "$held" || code=$?
# the servers are left running, so only the two runs are waited for
wait "$bare" || true

cat <<EOF

Both runs are over. The page stays up: http://localhost:$base/compare
Put everything back, and remove the typed password, with:

  sh demo/side-by-side.sh --reset
EOF
exit "$code"
