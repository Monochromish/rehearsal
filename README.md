# Rehearsal

Rehearsal wraps an AI coding agent in one command. It shows you every server
the agent contacted and which of your details reached each one, and it holds
the agent's risky outward actions until you approve them.

```
rehearse claude
```

You keep using the agent as usual. Rehearsal sits between it and the internet
and opens a page that updates as requests go out. When the agent exits, the
page becomes a decision screen: each held action is explained in a sentence,
ranked by risk, and performed only if you tick it.

This is milestone 2 of 3. Rehearsing file changes on a copy of the project is
still to build.

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

What gets recorded is which value reached which server, and whether it was in
the address, a header or the body. Request bodies, headers, cookies, keys and
tokens are never written to the ledger.

## What it holds

While the agent runs, these are held instead of performed:

- `git push`, in any form.
- `npm publish`.
- `gh` commands that create or change something: `pr create`, `issue
  comment`, `release create`, `repo delete` and the like, and `gh api` calls
  that write.
- Any `POST`, `PUT`, `PATCH` or `DELETE` request to a server that isn't the
  model provider.

The agent is told the action succeeded, so it carries on. Everything else
passes straight through: other git, npm and gh commands, and `GET` and `HEAD`
requests, which show up in the ledger as before.

When the agent exits, `rehearse` stays running and the page lists what was
held, in three groups: "Look at these" (high risk), "Probably fine" (medium)
and "Routine" (low, collapsed to one line). High-risk actions start
unticked, the rest ticked. Each row says in plain words what the action
would do, where it goes, and what the reviewer made of it, with the exact
command one click away. Identical actions to the same place share a row.

One button carries out the decision and says what it will do ("Allow 2,
refuse 1"). Allowed actions are replayed in the order they were held,
stopping at the first failure. "Discard all" performs none, and so does
pressing Ctrl+C in the terminal. Afterwards the page shows a table of every
held action with what was sent and what was decided, which can be copied as
Markdown. The same result is printed in the terminal and written to
`rehearsal-ledger.json`.

A held push is not sent if its branch has moved since it was held, because you
would be approving something other than what you were shown.

`--no-hold` turns all of this off and leaves only the ledger.

## The reviewer

The explanations and risk ratings come from a model running on your own
machine, through an OpenAI-compatible chat endpoint. By default that is
[Ollama](https://ollama.com/) with `qwen3.8:27b`, which takes about 3 seconds
per action on the laptop this was built on. `qwen3.5:9b` is quicker, about 2
seconds, and gave the same ratings in testing, but its explanations are more
technical.

```
rehearse --reviewer-model qwen3.5:9b claude
```

Nothing about a held action is sent anywhere else. `rehearse` refuses a
reviewer address that isn't on this machine or your private network.

The model is not trusted with the decision. Fixed rules rate every action
first, and the model can only raise that rating, never lower it. A force
push, a deletion, a publish, a change to who can access a repository, any
`DELETE` request, anything carrying one of your details or a planted value,
and a push of files that look like secrets (`.env`, private keys, `*.pem`,
`*.key`, credentials files) are always high risk. Only a few kinds can be
low: a push of a new branch that isn't the default one, opening or commenting
on an issue or pull request, and adding labels. Everything else is at least
medium. The exact command sits next to every explanation, and the page says
when an explanation was written by the model.

If the model can't be reached, answers too slowly, or twice returns something
unusable, the same rules write the explanation instead and the page says so.
Rehearsal works fully without a model.

### Reaching Ollama from WSL

Inside WSL2, `localhost` is the Linux side, so Ollama on Windows isn't there.
It is reachable at the default gateway address, the `via` address in:

```
ip route show default
```

That address can change when Windows restarts, so `rehearse` reads it from the
routing table each time it starts. Ollama has to be listening on that address
and not only on Windows' own localhost. To use a different address or server:

```
rehearse --reviewer-url http://192.168.96.1:11434/v1 claude
```

or set `REHEARSAL_REVIEWER_URL` and `REHEARSAL_REVIEWER_MODEL`. Outside WSL
the default is `http://localhost:11434/v1`.

## Stand-in answers

A held request gets a plain `200` with an empty JSON object unless a handler
file says otherwise. Handler files live in `handlers/`, one JSON file per
service, named after its host:

```json
{
  "host": "localhost",
  "about": "What this service is.",
  "responses": [
    { "method": "POST", "path": "/upload", "status": 201,
      "content_type": "application/json", "body": "{\"stored\":true}" },
    { "method": "POST", "path": "/tickets", "status": 201,
      "body": "{\"created\":true}", "needs_real_answer": true }
  ]
}
```

`path` may use `*` for one path segment. `needs_real_answer` marks a request
whose real reply the agent would normally use, such as the number of something
it just created. Rehearsal doesn't try to fake those. It labels the action on
the page so you know that what the agent did next may rest on a made-up answer.

The files in `handlers/` are built into the binary. Only `localhost.json`
ships, for the local server the tests and the demo use. `--handlers DIR` adds
your own; a file for a host that already has one replaces it.

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
| `--no-hold` | Hold nothing. Only record where requests go. |
| `--provider HOST` | Treat `HOST` as a model provider. Repeat it or separate hosts with commas. Default: `api.anthropic.com`, `mcp-proxy.anthropic.com`, `api.openai.com`. |
| `--allow-host HOST` | Never hold writes to `HOST`. Default: `http-intake.logs.us5.datadoghq.com`, where Claude Code sends its own telemetry. |
| `--handlers DIR` | Folder of extra handler files. |
| `--reviewer-url URL` | Address of the local model. |
| `--reviewer-model NAME` | Model that explains held actions. Default `qwen3.8:27b`. |
| `--reviewer-timeout TIME` | How long to wait for the model on each action. Default `45s`. |
| `--port N` | Serve the page on port `N` instead of any free port. |
| `--no-open` | Don't open a window, just print the address. |

Options go before the agent command. Everything after it is passed to the
agent untouched.

Add `rehearsal-ledger.json` and `CANARY.txt` to your project's `.gitignore`.

## How it works

`rehearse` starts an intercepting proxy on a free local port, with its own
certificate authority kept in `~/.local/state/rehearsal/ca`. It launches the
agent as a child process with the proxy and the certificate set through
environment variables (`HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`
and so on), and with stand-ins for `git`, `npm` and `gh` first on its `PATH`.
Only that process and its children are affected, and nothing is installed
system-wide.

Every request becomes a row in an append-only SQLite log, and so does every
held action and what became of it. The page, the terminal summary and the JSON
file are all built from that log.

A stand-in is the `rehearse` binary under another name. It hands every command
it doesn't hold to the real program. For one it does hold, it records the
folder, the arguments and any input, prints what the real command prints on
success, and exits 0.

What is needed to replay a held action, including a held request's full body
and headers, is kept in the session's private folder with owner-only
permissions. It is deleted when the session is applied or discarded.

If a request can't be recorded or held, it isn't sent. The agent gets an error
for it, and `rehearse` says so when it exits.

## Current limits

- Holding depends on the agent using the proxy and the stand-ins. A program
  that ignores the proxy settings, calls `/usr/bin/git` by its full path, or
  pushes over SSH with its own client is not held. Isolation that can't be
  bypassed is planned, not built.
- The agent is given a stand-in answer, not the real one. If it needs the real
  result to continue, for example the number of a pull request it just opened,
  what it does next may be wrong. These actions are labelled on the page.
- A held push doesn't update anything locally, so `git status` still shows the
  branch as ahead and the agent may push again. Each push is held separately.
- `gh` commands print nothing when held, and the stand-in for `git push`
  prints what git usually prints for a push like it, worked out without
  contacting the remote.
- `gh` isn't installed on the machine this was built on, so the `gh` stand-in
  has only been tested against a script standing in for the real program.
- The reviewer can be wrong. In testing the best model misread one detail, and
  against a planted instruction it kept its high rating but called the
  destination "public", which nothing in the action said. Read the exact
  command, not only the explanation.
- Decisions are accepted only after the agent has exited and only from the
  Rehearsal page. A process the agent left running in the background could
  still send one, because nothing isolates it yet.
- A held request with a body over 32 MB is refused, not held.
- Git and read-only `gh` commands use `POST` for reads, so they go through a
  second proxy that records but never holds. Other `npm` and unrecognised `gh`
  commands don't, and a read they make with `POST` gets a stand-in answer.
- It only finds values it can read. Anything encrypted, hashed, hex-encoded or
  split across requests is missed. Values shorter than 4 characters aren't
  searched for at all. Replies and WebSocket messages aren't inspected.
- HTTPS to a bare IP address (`https://127.0.0.1:8443`) fails through the
  proxy. Names such as `localhost` work.
- The wrapped command is told to trust only Rehearsal's certificate, so a tool
  that skips the proxy may fail to connect.
- The page stops updating when `rehearse` exits. An open window keeps the
  final state, but reloading it fails. Use `rehearsal-ledger.json` after that.
- The ledger file holds your real details (the values being watched for). It
  stays on your machine. Don't commit it.
- It has been run on one setup: Ubuntu 26.04 under WSL2 on ARM64, with Claude
  Code. Every automated test talks only to servers on the same machine.

## Development

```
sh check.sh
```

runs gofmt, `go vet`, staticcheck, the front-end type check and build, and the
Go tests under the race detector. It needs a C compiler and
[staticcheck](https://staticcheck.dev/).

One test needs a real model and is skipped unless you point it at one:

```
REHEARSAL_LIVE_MODEL_URL=http://192.168.96.1:11434/v1 go test -run LiveModel ./internal/review
```

| Path | What is there |
|---|---|
| `cmd/rehearse` | The command and the end-to-end tests |
| `handlers` | Stand-in answers for held requests, one file per service |
| `internal/events` | Event types and the append-only SQLite log |
| `internal/ident` | Collecting identifiers, making canaries, the matcher |
| `internal/redact` | Masking anything that looks like a credential |
| `internal/proxy` | The intercepting proxy: recording and holding requests |
| `internal/standin` | What runs when the agent calls `git`, `npm` or `gh` |
| `internal/hold` | Held actions and the private store that can replay them |
| `internal/review` | The local-model reviewer and the rules it can't go below |
| `internal/apply` | Performing approved actions |
| `internal/ledger` | Building the ledger from the log |
| `internal/server` | The local API and the page |
| `internal/run` | Launching the agent with a pseudo-terminal |
| `web` | The page (Vue 3, TypeScript, Vite) |
| `spikes` | Throwaway feasibility tests |

`DECISIONS.md` lists the choices made where the plan left things open.
