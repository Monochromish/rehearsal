#!/bin/bash
# Feasibility test D: bubblewrap with its own network namespace and an
# unprivileged overlay mount, as a normal user. Report only.
# Run inside WSL: bash run-d.sh
set -uo pipefail
echo "user: $(whoami) (uid $(id -u))"
echo "kernel: $(uname -r)"
echo "bwrap: $(bwrap --version)  setuid: $([ -u "$(command -v bwrap)" ] && echo yes || echo no)"

t=$(mktemp -d "$HOME/.rehearsal-d-XXXXXX")
mkdir -p "$t/lower" "$t/upper" "$t/work"
echo original > "$t/lower/a.txt"

echo
echo "--- D1: separate network namespace ---"
bwrap --unshare-user --unshare-net --ro-bind / / --dev /dev --proc /proc \
  bash -c 'echo "interfaces inside: $(tail -n +3 /proc/net/dev | cut -d: -f1 | tr -d " " | tr "\n" " ")";
           curl -sS -m 5 -o /dev/null https://example.com 2>&1 | head -1; echo "curl exit inside: ${PIPESTATUS[0]}"'
echo "D1 bwrap exit: $?"

overlay_test() { # $1 = label, $2 = lower dir
  echo
  echo "--- $1 (lower: $2) ---"
  rm -rf "$t/upper" "$t/work"; mkdir -p "$t/upper" "$t/work"
  bwrap --unshare-user --unshare-net --ro-bind / / --dev /dev --proc /proc \
    --overlay-src "$2" --overlay "$t/upper" "$t/work" "$2" \
    bash -c 'cd "$0" && echo changed > a.txt && echo new > b.txt && echo "inside sees: a.txt=$(cat a.txt) b.txt=$(cat b.txt)"' "$2"
  echo "bwrap exit: $?"
  echo "real a.txt after: $(cat "$2/a.txt")   real b.txt exists: $([ -e "$2/b.txt" ] && echo yes || echo no)"
  echo "captured in upper layer: $(ls "$t/upper" 2>/dev/null | tr '\n' ' ')"
}

overlay_test "D2: overlay, project on the Linux file system" "$t/lower"

win="/mnt/c/Users/$(whoami)/AppData/Local/Temp/rehearsal-d-lower"
if mkdir -p "$win" 2>/dev/null; then
  echo original > "$win/a.txt"
  overlay_test "D3: overlay, project on the Windows drive (/mnt/c)" "$win"
  rm -rf "$win"
else
  echo "D3 skipped: cannot create $win"
fi
rm -rf "$t"
