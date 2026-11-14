#!/bin/bash
# Feasibility tests B and C, plus a local streaming check for test A.
# Run inside WSL from the spikes/ directory: bash run-bc.sh
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
cd "$(dirname "$0")"
bin="$HOME/.cache/rehearsal-spikes"
tmp=$(mktemp -d)
mkdir -p "$bin"
go build -o "$bin/proxyspike" ./proxyspike || exit 1
go build -o "$bin/sseserver" ./sseserver || exit 1

echo "=== Test B: curl run by a child shell of the wrapped process ==="
"$bin/proxyspike" -log "$tmp/b.log" -- bash -c \
  'echo "curl exit/status: $(curl -sS -o /dev/null -w "%{http_code}" https://example.com)"'
cat "$tmp/b.log"

echo
echo "=== Streaming + readable body through the proxy (local HTTPS test server) ==="
"$bin/sseserver" > "$tmp/url" &
srv=$!
sleep 1
# Use the name "localhost": the proxy library cannot mint a certificate for
# a bare IP address (it names the certificate after the TLS server name).
url=$(sed 's/127\.0\.0\.1/localhost/' "$tmp/url")
"$bin/proxyspike" -insecure -log "$tmp/s.log" -- bash -c "
  t0=\$(date +%s%N)
  curl -sSN $url/events | while IFS= read -r line; do
    [ -n \"\$line\" ] && echo \"client got '\$line' at +\$(( (\$(date +%s%N) - t0) / 1000000 ))ms\"
  done
  curl -sS -X POST -d 'model=none&note=local-test-body' $url/echo
"
kill $srv 2>/dev/null
cat "$tmp/s.log"

echo
echo "=== Test C: stand-in git sees a push (to a local bare repo, not the internet) ==="
git init -q --bare "$tmp/remote.git"
git init -q -b main "$tmp/work"
git -C "$tmp/work" -c user.name=spike -c user.email=spike@example.invalid commit -q --allow-empty -m "spike"
git -C "$tmp/work" remote add origin "$tmp/remote.git"
"$bin/proxyspike" -shim ./shim -log "$tmp/c.log" -- bash -c \
  "cd $tmp/work && echo \"git resolves to: \$(command -v git)\" && git push -q origin main && echo push-exit=\$?"
cat "$tmp/c.log"
echo "remote now has: $(git -C "$tmp/remote.git" log --oneline -1 main 2>&1)"
rm -rf "$tmp"
