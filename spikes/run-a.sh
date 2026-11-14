#!/bin/bash
# Feasibility test A: Claude Code through the proxy.
# Needs `claude` installed and logged in inside WSL. Run: bash run-a.sh
# Prints timing of streamed text pieces and the request log (host, method,
# path, sizes). Prints no reply text, headers or keys.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
cd "$(dirname "$0")"
command -v claude >/dev/null || { echo "claude is not installed in this Linux environment"; exit 1; }
bin="$HOME/.cache/rehearsal-spikes"
mkdir -p "$bin"
go build -o "$bin/proxyspike" ./proxyspike || exit 1
log=$(mktemp)

"$bin/proxyspike" -log "$log" -- claude -p "Count from 1 to 40, one number per line, nothing else." \
  --output-format stream-json --include-partial-messages --verbose |
  python3 -u -c '
import sys, json, time
t0 = time.time(); n = 0; first = last = None
for line in sys.stdin:
    try:
        e = json.loads(line)
    except ValueError:
        continue
    if e.get("type") == "stream_event" and e.get("event", {}).get("type") == "content_block_delta":
        n += 1; last = time.time() - t0
        first = first if first is not None else last
    elif e.get("type") == "result":
        print("claude result:", e.get("subtype"), "is_error =", e.get("is_error"))
if n:
    print(f"streamed pieces seen by claude: {n}, first at +{first:.2f}s, last at +{last:.2f}s")
else:
    print("no streamed pieces seen")
'
echo "--- proxy log ---"
cat "$log"
rm -f "$log"
