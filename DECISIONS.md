# Decisions

Choices made where the plan left things open. The simplest option was taken
each time unless a reason is given.

## Environment

Development happens in Ubuntu 26.04 under WSL2. Go and Node.js are installed
in the home folder, so nothing needed root.

The proxy library is go-mitmproxy v1.9.5. It passed all four feasibility
tests, so goproxy and the Python fallback were never tried.

The Go module path is `rehearsal`, a placeholder. It should become the GitHub
path once the public repository exists.

The front end is on TypeScript 6. vue-tsc 3.3 doesn't run on TypeScript 7.

## Where things are kept

State lives in `$XDG_STATE_HOME/rehearsal`, by default
`~/.local/state/rehearsal`: the certificate authority in `ca/` and one folder
per session under `sessions/`, each with its `events.db`.

The event log isn't in the project folder because SQLite's WAL mode is
unreliable on the Windows drive (`/mnt/c`). The certificate authority isn't
there either: the library writes its private key next to the certificate, and
that must never end up in a repository.

The certificate authority is generated once and reused by later sessions.

go-mitmproxy can't report which port it bound, so we find a free port first
and hand it over. If another program takes that port in between, the proxy
fails to start and says why.

## Canaries

The secret word goes into `CANARY.txt` in the working folder. The file is
removed when the session ends, unless a file of that name was already there.

The plan didn't say where to plant the fake email address and API key. They
are given to the wrapped command as `REHEARSAL_CANARY_EMAIL` and
`REHEARSAL_CANARY_API_KEY`, since the environment is what a tool that uploads
"everything" would pick up.

## Finding identifiers

- Identifiers shorter than 4 characters are skipped. They match by accident
  too often.
- Plain and URL-encoded forms are matched ignoring case and only as a whole
  word, so the username `adal` isn't found inside `adalberto`. A word may
  follow a URL escape (`%2Fadal`) or a JSON escape (`\nadal`).
- Base64 is only searched for identifiers of 8 characters or more, in the
  standard and URL-safe alphabets and at all three alignments. That finds a
  value inside a larger encoded blob too.
- Request bodies over 32 MB pass through unsearched and are recorded with
  `body_checked: false`.
- Compressed bodies are decoded with the library's decoder first. If that
  fails, the raw bytes are searched.
- If your git name, git email, username or computer name can't be read,
  `rehearse` prints a warning and carries on without that one.

## What is stored

Per request: time, server, method, path without the query string, size and
response status. Per identifier found: which one, which part of the request
(address, header or body) and which form. Headers, cookies, query strings and
bodies are never stored.

Only the program name of the wrapped command is stored. Its arguments may
hold secrets.

The watched values themselves, your real ones included, appear on the page
and in `rehearsal-ledger.json`. Both are local. This repository's
`.gitignore` keeps the JSON file out of version control.

## When recording fails

A request that can't be written to the event log is refused with a 502, and
so is every request after it. Nothing may leave without appearing in the
ledger. `rehearse` reports the failure when it exits and returns a non-zero
exit code.

Errors while shutting down (closing the log, stopping the proxy, removing
`CANARY.txt`, restoring the terminal) are reported the same way.

## Classifying servers

The default model provider hosts are `api.anthropic.com`,
`mcp-proxy.anthropic.com` and `api.openai.com`; `--provider` changes them.
`mcp-proxy.anthropic.com` is Anthropic's relay for connectors. It's on the
list so that an ordinary Claude Code session doesn't start out flagged.

A server is flagged when it isn't a model provider and received at least one
identifier, real or planted.

## The page

The page re-reads `/api/ledger` when events arrive on `/api/events`, a few
times a second at most. The ledger is computed in one place, the Go code.

`web/src/api.ts` checks the shape of everything it receives by hand and
throws on a mismatch. There is no schema library because it would be a new
dependency. A failed read or an unreadable update is shown in a banner at the
top of the page, above the last ledger that was read.

Headings are real `h1` and `h2` elements and the theme switch is a real
`button`. There are no landmark elements (`main`, `header`, `section`, `nav`)
yet: the components will be ported to a native renderer that maps a limited
set of tags, and which ones it supports is still to be confirmed.

The two colour palettes are plain data applied to the root element as CSS
variables. The page starts in the system's theme and has a button to switch.
Styles use flexbox, single class selectors and margins only.

The built page isn't committed. `sh build.sh` builds it into
`internal/webui/dist`, where a placeholder file keeps `go build` and
`go test` working before the first front-end build.

Opening Edge in app mode is only attempted inside WSL. Elsewhere the address
is printed.

## Running the command

The wrapped command gets a pseudo-terminal only when both input and output
are a terminal. Otherwise they are passed straight through, so pipes work.

The terminal summary goes to standard error, so piping the wrapped command's
output somewhere else stays clean.
