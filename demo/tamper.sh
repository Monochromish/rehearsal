#!/bin/sh
# Changes one character in a copy of a session's record and shows that the
# check notices, and where.
#
#   sh demo/tamper.sh [session]
#
# With no session named it takes the most recent one. The record itself is
# only read: the change is made to an export of it in a temporary folder,
# which is removed when the script ends.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
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

copy=$(mktemp -d)
trap 'rm -rf "$copy"' EXIT INT TERM

# An export is the log, one entry to a line, each exactly as it was hashed.
if ! "$rehearse" export "$@" --format jsonl >"$copy/export.jsonl" 2>"$copy/export.err"; then
	cat "$copy/export.err"
	exit 1
fi
name=$(sed -n '1s/^{"session":"\([^"]*\)".*/\1/p' "$copy/export.jsonl")
if [ -z "$name" ]; then
	echo "The export does not look like a session's record."
	exit 1
fi
record=$copy/$name.jsonl
mv "$copy/export.jsonl" "$record"
# With the session's signature beside the export, that is checked too.
state=${XDG_STATE_HOME:-$HOME/.local/state}
if [ -f "$state/rehearsal/sessions/$name/checkpoint.json" ]; then
	cp "$state/rehearsal/sessions/$name/checkpoint.json" "$copy/$name.checkpoint.json"
fi
entries=$(wc -l <"$record" | tr -d ' ')

# check prints what the check of the copy finds: whether it holds, the
# chain of entries, and the signature.
check() {
	"$rehearse" investigate "$record" --no-model 2>&1 | sed -n '/^Custody/,/^$/p' | sed '1d; /^$/d; /to check it yourself/d'
}

echo "The record of session $name has $entries entries."
echo "This works on a copy. The record itself is not touched."
echo
echo "The copy, before any change:"
check
echo

while :; do
	printf 'Pick an entry number: '
	if ! IFS= read -r pick; then
		echo
		echo "No entry was picked."
		exit 1
	fi
	pick=$(printf '%s' "$pick" | tr -d '\r ')
	case "$pick" in
	'' | *[!0-9]*) ;;
	*)
		if [ "$pick" -ge 1 ] && [ "$pick" -le "$entries" ]; then
			break
		fi
		;;
	esac
	echo "A number from 1 to $entries."
done

# One character: the last digit of the entry's time goes up by one.
start='^\({"session":"[^"]*","seq":[0-9]*,"time":"[^"]*\)'
old=$(sed -n "${pick}s/${start}\([0-9]\)\(Z\{0,1\}\",\).*/\2/p" "$record")
if [ -z "$old" ]; then
	echo "Entry $pick has no time where one was expected, so nothing was changed."
	exit 1
fi
new=$(((old + 1) % 10))
sed "${pick}s/${start}[0-9]\(Z\{0,1\}\",\)/\1${new}\2/" "$record" >"$record.changed"
mv "$record.changed" "$record"

echo
echo "Changed one character in entry $pick of the copy: the last digit of its time, $old to $new."
echo
echo "The copy, after the change:"
check
