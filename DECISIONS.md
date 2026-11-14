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
dependency. A failed read or an unreadable update is shown at the top of the
page, above the last state that was read, with what to do about it.

Headings are real `h1` and `h2` elements, controls are real buttons, and
tick boxes are real checkboxes with labels. There are no landmark elements
(`main`, `header`, `section`, `nav`) yet: the components will be ported to a
native renderer that maps a limited set of tags, and which ones it supports
is still to be confirmed.

The two colour palettes are plain data applied to the root element as CSS
variables, and no colour is written anywhere else. The page follows the
system's theme until the reader picks one with the link at the bottom.
Styles use flexbox, class selectors (with `:focus-visible` and `:disabled`
where a control needs them) and margins only.

The page has one column and three states, and shows only what fits the
moment: what is unusual while the agent runs, the decision once it has
exited, and a report afterwards. The full server list and the watched values
are one click away in every state, never on screen by default.

With `?fixture=NAME` in its address the page shows a scripted session and
contacts nothing. The names are in `web/src/fixtures.ts`. They exist so each
state can be looked at, and screenshotted, without an agent or a model.
`&theme=dark` or `&theme=light` fixes the theme the same way.

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

## Stand-ins for git, npm and gh

The stand-ins are the `rehearse` binary itself, started under the other
program's name. A copy is put in the session's folder with `git`, `npm` and
`gh` linked to it, because every git call the agent makes starts it and the
original may sit on a slow drive.

`git push` is held in any form, including through a simple alias, and so are
`git send-pack` and `git http-push`, the plumbing underneath it.
`git push --dry-run` and `npm publish --dry-run` change nothing, so they go to
the real program.

A push of a branch that doesn't exist fails the way git fails, and is not
held. Reporting success for it would not be believable.

The output for a held push is worked out locally: the remote's address, the
branch names and the commit IDs come from the repository, with no contact
with the remote. Nothing local is changed either. The remote-tracking branch
stays where it was, so `git status` still says "ahead". Faking that would
leave the repository lying to the user after a refused push.

Held `gh` commands print nothing and exit 0. Which `gh` commands write is a
table in `internal/standin`; those that return something the agent will use
(`pr create`, `issue create`, `gist create`, `pr revert`) are marked there as
needing the real answer. The plan said to mark these in the handler files,
but handler files describe web requests, and for a command the stand-in is
where the knowledge is. Handler files carry the same mark for requests.

A `gh` command in neither the "writes" nor the "reads" table is run as it
is. The proxy still holds any write it makes.

Standard input is recorded only when the command was told to read it
(`--body-file -` and the like). Reading it otherwise can wait forever on a
pipe nobody closes.

The socket the stand-ins report to accepts only `git`, `npm` and `gh` as
programs, since anything on this machine can write to it and what is reported
may later be run.

## The second proxy

git and gh both use `POST` for reads: `git fetch` posts to
`git-upload-pack`, and `gh pr list` posts a GraphQL query. Holding every
`POST` would break "everything else passes through". So a second proxy
records requests without holding any, and the stand-ins send the commands
they know to be read-only through it: all git other than push, and the `gh`
commands in the "reads" table.

npm doesn't use it. Other npm commands can write to the registry
(`npm unpublish`, `npm deprecate`), and those should still be caught.

## Held requests

A held request is not in the ledger's request count: it hasn't been sent. If
it is approved and sent, it is added then, with the identifiers it carried,
like any other request.

To replay a request its headers are stored along with its body, and they can
include credentials. They are in the same private file and deleted at the
same time.

A request too large to keep (over 32 MB) is refused. Letting it through
would break the hold, and holding it would mean streaming it to disk, which
isn't built.

When replayed, a request goes straight to the server, not through the proxy,
and redirects are not followed. Only a 2xx answer counts as applied.

## The reviewer

The model is reached through the OpenAI-compatible endpoint, so that other
local servers work as well as Ollama. The settings found to work through
Ollama's own `/api/chat` are sent in their OpenAI form:
`reasoning_effort: "none"` for `think: false`, a JSON schema in
`response_format`, temperature 0 and `max_tokens: 200`.

Whether those two settings really take effect through the compatible
endpoint was checked against `qwen3.8:27b` on Ollama, and the checks are kept
as tests (`TestLive...` in `internal/review`, run only when
`REHEARSAL_LIVE_MODEL_URL` is set):

- Thinking is off. Our request came back in 3.2 seconds with 65 output
  tokens and no reasoning text. The same request with thinking left on took
  13.3 seconds, with 384 tokens and 1,373 characters of reasoning.
- The schema is enforced by the server. A request whose instructions asked
  for a different shape and a risk word outside the three allowed still came
  back with exactly `summary`, `risk` and `concern`, and `risk` set to
  `high`.

So there is no Ollama-specific path. Had thinking stayed on, the plan was to
detect Ollama and use its native endpoint for it.

The first call after the model loads takes much longer, 16 seconds when
measured.

A warm-up request goes out in the background at start-up so that the slow
first call happens while the agent works. Its failure is ignored on purpose.

Up to three actions are reviewed at once. Each result is written to the log
as it arrives, so the page fills in one by one.

The first time the model fails to answer (connection refused, timeout, an
error status) it is not asked again in that session, so ten held actions
don't mean ten timeouts. A reply that fails validation is retried once, then
that action falls back to the rules.

The model sees the command or request line, the destination, the files and
commit messages a push would send, which of your details it carries (by
name, not value), and up to 1,500 characters of a text body with anything
secret-looking masked. Binary bodies are described by size only.

The reviewer address must resolve to this machine or a private network
address. An address the user typed that doesn't is an error at start-up. If
no address was given and none can be worked out, the rules are used.

## Risk ratings

Every held action has a rating from the rules, and that rating is also its
minimum: the model may rate an action higher, never lower. Without a model
the rules' rating is used as it is.

High:

- a force push or a mirror push
- deleting a branch or tag on the remote
- any publish
- deleting a repository or a release
- changing a repository's visibility, collaborators or branch protection
- any `DELETE` request
- anything carrying one of your details or a planted value
- a push whose commits add or change secret-looking files

Low:

- a push of new branches only, none of them the remote's default branch
- opening or commenting on an issue or pull request
- adding labels

Everything else is medium. That includes a push to the default branch,
creating a release, and every other write request. The plan singled out
writes "to a host not contacted earlier in the session" as medium; since
anything unlisted is medium too, a write to a host seen before comes out the
same, and the distinction is not computed.

How the facts behind these are found:

- The remote's default branch is the one `refs/remotes/<remote>/HEAD` names.
  A remote that was added and never fetched has none, and then `main` and
  `master` are both treated as the default.
- A branch is new when there is no remote-tracking branch for it. Nothing is
  asked of the remote.
- Visibility is `gh repo edit --visibility` and `gh repo create --public`.
  Collaborators and branch protection have no `gh` command of their own, so
  they are recognised by path (`/collaborators`, `/protection`, `/rulesets`)
  in `gh api` calls and in held requests.
- "Adding labels" is `gh label create`, and `gh issue edit` or `gh pr edit`
  whose only options are `--add-label`.
- Deleting something other than a repository or a release through `gh`
  (an issue, a gist, a secret) is medium. A `gh api -X DELETE` is high, as
  any `DELETE` request is.

Secret-looking names are `.env` and `.env.*`, the usual private key names
(`id_rsa`, `id_ed25519` and so on, not their `.pub` halves), key and
certificate stores by extension, and files named after "credentials" or
"secrets" as a whole word. `.env.example` is caught too. It often holds
nothing real, but the name alone can't tell.

The file list comes from the commits that no remote-tracking branch has yet,
up to 500 commits back. The floor uses the whole list; the page shows the
first 100 names and 30 commit messages.

An earlier version let the model take a medium action down to low, which
moved it into the collapsed "Routine" list. That gave text the agent wrote a
way to make an action less visible, so it was removed.

## Deciding and applying

The six new event types are the ones the plan lists. Whether the session is
running, reviewing, waiting for a decision, applying or finished is worked
out from them, not stored.

A decision is accepted only once the agent has exited and every action has
been described, only as JSON, and only with the page's own origin. Pressing
Ctrl+C in the terminal counts as "discard everything".

When the agent exits, the proxies and the stand-in socket are stopped before
anything is reviewed, so the list the user decides on can't grow.

Applying stops at the first failure. Actions approved after it are marked
"not applied". `rehearse` exits with status 1 when an approved action failed
and the agent itself had exited 0.

Before a held push is sent, every local branch it would push is compared
with the commit recorded when it was held.

## The decision screen

The largest line of a row says what the action would do, and it is built by
the page from facts in the API ("Overwrite main at ...", "Send 2 KB to ...").
The model's two sentences come below it in smaller text, always behind the
label "Reviewer (AI, on this computer)". So the line a reader takes in first
is never something the model wrote. When the rules wrote the explanation the
label says "Reviewer (built-in rules, no AI)".

Each row also has a monospace line, "what, from where -> where", that is
always visible. The full command, with the files and commit messages of a
push, is behind "Show command", which starts open for high-risk rows only.

Actions that are identical in every way that shows (same command, same
destination, same rating) share one row and one tick box, titled with the
count: "8 pushes to origin". Rows are in order of destination.

Actions the reviewer has not described yet are listed under "Still being
read" and not under a risk heading, and the decision buttons appear only
once all of them have been described.

The button that carries out the decision states the outcome ("Allow 2,
refuse 1") and names what cannot be undone among the ticked actions: a
publish, a force push or a deletion. There is no confirmation dialog.

`j` and `k` move between rows, space ticks, Enter shows or hides the focused
row's command. Collapsed lists open with buttons, not `details`, to keep to
tags a limited renderer will have.

## Claude Code's auto mode

Tested with Claude Code 2.1.325 pushing to a local bare repository, run
non-interactively (`claude -p`).

With `--permission-mode auto`, Claude's own checker allowed `git push origin
main`, the stand-in held it, and Claude reported the push as done. With auto
mode off and the command pre-allowed, the same.

With no permission flags, `claude -p` did not run the push at all ("the
command needs your approval"), so nothing reached the stand-in. Rehearsal
can only hold what the agent actually tries.

Not tested: an interactive session, a push to a real remote, or a force
push, any of which the checker may treat differently.
