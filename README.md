# Rehearsal

Rehearsal wraps an AI coding agent in one command and shows you every server
it contacted and which of your details reached each one.

```
rehearse claude
```

You keep using the agent as usual. Rehearsal sits between it and the internet
and opens a page that updates as requests go out.

This is milestone 1 of 3, the ledger. It shows what the agent sent. It doesn't
hold or block anything yet; that, and rehearsing file changes on a copy of the
project, are the two milestones still to build.

## What it shows

The page lists every server the agent contacted, split into the model provider
and everything else. Each server has a request count and the details of yours
it received.

"Your details" means your git name and email, your computer username and your
computer's name. Rehearsal also plants three fake values for the session: an
email address, an API key, and a word in `CANARY.txt`. A planted value has no
reason to leave your machine, so if one shows up under a server, your data
went there.

A server that isn't the model provider and received any of these is flagged.

When the agent exits, the same ledger is printed in the terminal and saved as
`rehearsal-ledger.json` in the folder you ran it from.

What gets recorded is which value reached which server, and whether it was in
the address, a header or the body. Request bodies, headers, cookies, keys and
tokens are never stored.

## Install

Linux, or WSL2 on Windows. You need [Go](https://go.dev/dl/) 1.27 or newer and
[Node.js](https://nodejs.org/) 26 or newer. Nothing needs root.

```
git clone <this repository>
cd rehearsal
sh build.sh
```

That leaves a single binary, `./rehearse`. Copy it somewhere on your `PATH`.

## Run

```
rehearse <agent command...>
```

On WSL the page opens in its own Edge window. Anywhere else, open the address
`rehearse` prints when it starts.

| Option | What it does |
|---|---|
| `--provider HOST` | Treat `HOST` as a model provider. Repeat it or separate hosts with commas. Default: `api.anthropic.com`, `mcp-proxy.anthropic.com`, `api.openai.com`. |
| `--port N` | Serve the page on port `N` instead of any free port. |
| `--no-open` | Don't open a window, just print the address. |

Options go before the agent command. Everything after it is passed to the
agent untouched.

Add `rehearsal-ledger.json` and `CANARY.txt` to your project's `.gitignore`.

## How it works

`rehearse` starts an intercepting proxy on a free local port, with its own
certificate authority kept in `~/.local/state/rehearsal/ca`. It then launches
the agent as a child process with the proxy and the certificate set through
environment variables (`HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`
and so on). Only that process and its children are affected, and nothing is
installed system-wide.

Every request becomes a row in an append-only SQLite log: time, server,
method, path, size, response status. The request's address, headers and body
are searched for the watched values in plain, URL-encoded and base64 form.
The page, the terminal summary and the JSON file are all built from that log.

If a request can't be written to the log, it isn't sent. The agent gets an
error for it and for every request after it, and `rehearse` says so when it
exits.

## Current limits

- It shows, it doesn't stop. By the time a request is on the page it has been
  sent.
- It only sees traffic that goes through the proxy. A program that ignores the
  proxy settings or pins its own certificates is either invisible or can't
  connect. Isolation that can't be bypassed is planned, not built.
- It only finds values it can read. Anything encrypted, hashed, hex-encoded or
  split across requests is missed, and so is anything in a body over 32 MB.
  Values shorter than 4 characters aren't searched for at all.
- Replies aren't inspected, only what the agent sends.
- A WebSocket shows up as one connection. The messages inside it aren't
  searched, and WebSocket pass-through hasn't been tested.
- HTTPS to a bare IP address (`https://127.0.0.1:8443`) fails through the
  proxy. Names such as `localhost` work.
- The wrapped command is told to trust only Rehearsal's certificate, so a tool
  that skips the proxy may fail to connect.
- The page stops updating when the agent exits. An open window keeps the final
  ledger, but reloading it fails. Use `rehearsal-ledger.json` after that.
- The ledger file holds your real details (the values being watched for). It
  stays on your machine. Don't commit it.
- It has been run on one setup: Ubuntu 26.04 under WSL2 on ARM64, with Claude
  Code. The end-to-end test covers plain HTTP to a local server.

## Development

```
sh check.sh
```

runs gofmt, `go vet`, staticcheck, the front-end type check and build, and the
Go tests under the race detector. It needs a C compiler and
[staticcheck](https://staticcheck.dev/).

| Path | What is there |
|---|---|
| `cmd/rehearse` | The command and the end-to-end tests |
| `internal/events` | Event types and the append-only SQLite log |
| `internal/ident` | Collecting identifiers, making canaries, the matcher |
| `internal/proxy` | The intercepting proxy and the request recorder |
| `internal/ledger` | Building the ledger from the log |
| `internal/server` | The local API (`/api/ledger`, `/api/events`) and the page |
| `internal/run` | Launching the agent with a pseudo-terminal |
| `web` | The page (Vue 3, TypeScript, Vite) |
| `spikes` | Throwaway feasibility tests |

`DECISIONS.md` lists the choices made where the plan left things open.
