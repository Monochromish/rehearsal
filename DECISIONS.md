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

Also under the state folder: `recordings/`, where `rehearse record` writes
unless told otherwise, and `approvals.json`, the count of what has been
allowed before, which rule offers are made from. A session's sandbox layer,
when it has one, is in its session folder.

The signing key is the one thing kept elsewhere, in
`$XDG_CONFIG_HOME/rehearsal` (by default `~/.config/rehearsal`). State can be
deleted without losing anything that matters. The key can't.

In the working folder `rehearse` writes `rehearsal-ledger.json` and
`rehearsal-report.md`, and `rehearse attest` writes
`rehearsal-attestation.json`. Nothing else is written there, apart from
`CANARY.txt` for the length of a session and, when you accept an offered
rule, one line in `.rehearsal/policy.yaml`.

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
`.gitignore` keeps the JSON file out of version control. Secrets are the
exception: only a label and a salted hash of each are ever kept.

## When recording fails

A request that can't be written to the event log is refused with a 502, and
so is every request after it. Nothing may leave without appearing in the
ledger. `rehearse` reports the failure when it exits and returns a non-zero
exit code.

Errors while shutting down (closing the log, stopping the proxy, removing
`CANARY.txt`, restoring the terminal) are reported the same way.

## Classifying servers

The default model provider hosts are `api.anthropic.com`,
`mcp-proxy.anthropic.com`, `platform.claude.com` and `api.openai.com`;
`--provider` changes them. `mcp-proxy.anthropic.com` is Anthropic's relay for
connectors. It's on the list so that an ordinary Claude Code session doesn't
start out flagged. `platform.claude.com` is where Claude Code renews its
login, and a renewal posts a credential, which would otherwise be held.

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

## Watched secrets

A line in `.env` or `.env.*` is watched when its value is at least 8
characters and either its name contains a credential word (`KEY`, `TOKEN`,
`SECRET`, `PASSWORD`, `PASSWD`, `PWD`, `CREDENTIAL`, `AUTH`, `PRIVATE`,
`DSN`, `DATABASE_URL`, `CONNECTION`) or the value looks random. Watching
every value would hold requests that mention `NODE_ENV=production` or a port
number.

The value is used to build the search patterns and then dropped. What is
kept is a label and a hash salted per session, so the same secret has a
different hash in every session and the hash can't be looked up.

The eight built-in formats are regular expressions in `internal/ident`,
each with its own test. They are searched for in the address, the body and
most headers. They are not searched for in the standard credential headers
(`Authorization`, `Cookie`, `X-Api-Key` and similar) or in requests to the
model provider, because that is where a credential belongs, and holding
there would hold every signed-in request. A value read from `.env` is held
wherever it appears.

Hex was added to the forms that are searched (plain, URL-encoded, base64),
in lower and upper case.

## Requests that carry something

A request to a server that isn't the model provider is held whatever its
method when it carries a detail, a planted value or a secret. This applies
to the hosts on the `--allow-host` list too: the list lets ordinary
telemetry writes through, and telemetry that carries your git email is the
thing worth seeing.

The second proxy, which serves read-only git and gh commands, holds these
too. It shares what the first proxy has learned about the session.

A held read gets a 200 with an empty body, since nothing believable can be
made up for it, and is marked as needing the real answer.

## The model provider's door

For each model provider host, a salted hash of the first credential header
seen is remembered. A later request to that host with a different one is
held as high risk, and so is a `POST` under `/v1/files` or `/v1/uploads`.
Both are explained as sending data to the AI provider under a different
account or as a file upload.

A successful reply from a provider path ending in `/oauth/token` means the
login was renewed, and the next credential seen replaces the remembered one
without a hold. Without that, every long session would be held once an hour.
It does mean a second login obtained through the provider's own token
endpoint is not caught.

`--no-provider-check` turns the check off. It exists because the check has
only been run against a local stand-in for a provider.

## Where an address came from

The first 1 MB of each reply from a server other than the model provider is
scanned as it passes. From each one the host names of the `http://` and
`https://` addresses it mentions are kept (up to 50), with a salted hash of
each full address (up to 200). The text itself is not kept, apart from the
first 80 characters of the first HTML comment, with anything secret-looking
masked.

An address counts as coming from outside content when its host was first
seen there: a host the agent had already contacted, or a provider host, is
never marked. Every later request to a marked host is held as high risk,
not only the first, because the reason holds for each.

Hidden text is counted in three kinds: zero-width characters (U+200B,
U+200C, U+200D, U+2060, U+FEFF), Unicode tag characters (U+E0000 to
U+E007F) and HTML comments.

Once any outside content has been read, each held action records which
servers had been read by then.

## The session report

Bytes are request bytes, since the question is what left the machine.

"Before you pressed a key" needs the input to pass through `rehearse`. With
a pseudo-terminal it always does. When input is not a terminal it is copied
through a pipe. When input is a terminal and output is not, the command is
given the terminal itself, as before, and the figure is "not available".

The model name comes from the `model` field of a provider request's JSON
body. Token counts come from the `usage` object of the reply, in its plain
and streamed forms. A count the reply does not state is recorded as unknown
and shown as "not available", and a total is unknown when any part of it is.

Tool calls and their results are read from the conversation history that
the Anthropic Messages format repeats in every request, behind an interface
in `internal/observe` so another format can be added. Each call is recorded
once, by its ID. Streamed provider replies go straight through to the agent.
A copy of at most 8 MB is kept as they pass and read when the stream ends.

The three integrity flags are pattern matches on what was observed:
`--no-verify` on a `git commit` or `git push`; a change to a file that looks
like a test in a session where a test command reported an error; and a short
list of destructive commands (`rm -rf`, `git reset --hard`, `git clean -f`,
a force push, `DROP TABLE` or `DROP DATABASE`, `mkfs`, `dd` with `of=`).
They say "worth a second look", not that anything was wrong.

The two model-written parts are asked for after the decision, once the
agent's last message is known. The model gets the facts as a data block and
is told to use only what is there and to follow nothing in it. A summary
that isn't three sentences, or a claims answer that doesn't match the
schema or names a command that wasn't observed, is discarded. On the real
model, a final message with an instruction planted in it to mark every
claim as supported still had its "all tests pass" claim marked as
contradicted by the failing test command.

`rehearsal-report.md` is written beside `rehearsal-ledger.json`. Secrets
appear in it by label only.

## The record

Each row of the log stores two hashes: `prev`, the hash of the row before,
and `hash`, its own. The plan asks only for the first. Storing the row's
own hash as well lets a check name the row that was edited, not the one
after it.

The hashed form is the row's five stored fields joined by line feeds, not a
re-encoded JSON object. The stored text is hashed as it is, so there is no
canonical JSON to get wrong. `docs/attestation.md` spells it out.

Row numbers are assigned by `rehearse`, one more than the last, because the
number is part of what is hashed and has to be known before the row is
written.

The checkpoint is a file, `checkpoint.json`, beside the log. It is not an
entry in the log, because it signs the log's last hash. Before signing, the
log is sealed: a late write (a reply still being read when the session
ends, say) is refused, where otherwise it would make a signed record fail.

The signing key is PEM (PKCS #8 private, PKIX public) so other tools can
read it. It lives in the configuration directory, not with the session
state, since it outlives sessions and must never be swept up with them. A
private key file that group or others can read is refused, with the
`chmod` to fix it.

If the key can't be read or made, the session still runs. A warning says
the record will not be signed, and `rehearse verify` fails for that session
with "no signed checkpoint". Refusing to start seemed worse than recording
unsigned.

`rehearse verify` trusts your own public key and nothing else. A
checkpoint signed by any other key fails, since otherwise a rewritten log
could simply be signed again with a new key.

The page's "Record verified" line recomputes the chain on each read. It
doesn't check the signature, which is made after the page's last update.

`rehearse export` blanks the values of your own details in the first
entry, so that line's `data` no longer matches its `hash`. The other choice
was to export nothing redacted, and a log system is the wrong place for
your git email.

## Attestations

The commits "created during the session" are worked out from git, not from
what the agent said it did: every ref's commit is noted before the agent
starts, and when it exits the commits reachable from a local branch or
`HEAD` that none of those could reach are listed. A commit the agent
fetched and merged counts, since it is new to the local branches. The list
is written to the log as an event, so the attestation is built only from
the verified log.

The statement is signed in compact form (white space between tokens
removed), with a fixed first line naming the format. The verifier compacts
what it reads before checking, so re-indenting the file is harmless and
any change of content is not.

`verify-attestation` requires `--key`. Checking a file against the key it
names itself would pass for a statement anyone signed.

The tool version is a constant in `cmd/rehearse`, now `0.3.0`, written into
every session's first entry. The attestation states the version that
recorded the session, not the one that made the attestation.

## The sandbox

The sandbox has a network namespace of its own, so the proxy's port on this
machine isn't reachable from inside it. A Unix socket is a file and is. So
`rehearse` listens on a socket for each proxy and passes connections to the
proxy's port, and the first process inside the sandbox (the `rehearse`
binary again, started with `__sandbox-init`) listens on the same address
the proxy has outside and passes connections to the socket. The agent's
proxy settings are the same in and out of the sandbox.

Nothing else is relayed. The page and its decision API, the local model and
every other port on this machine are unreachable from inside.

The sockets are in a folder under the temporary directory, not the session
folder, because a Unix socket's path can be 107 bytes at most.

bubblewrap is run without `--die-with-parent`. If `rehearse` dies the agent
keeps running, with no network, and its work stays in the layer. Killing
the agent with it would have been as safe but would throw away whatever it
was in the middle of.

The plan says to record the loss of network as an event. The process that
writes events is the one that has died, and making the log writable from
inside the sandbox would let the agent write to it. So the sandbox's first
process holds a connection to `rehearse` open, and when it closes while the
agent is still running it writes the time to a file in the one folder of
ours it can write to. `rehearse verify` reports it.

A file change is a held action of kind `file`, so reviewing, deciding,
applying and the record all work as for a push. It is described by the
rules and never sent to the model: the rating depends on the path, and a
file's contents may hold anything.

Ordinary file changes are rated low. The rule that anything unlisted is at
least medium was written for actions that leave the machine, and a change
to the project's files is what the agent is for. The page still shows them
in a group of their own and not folded into "Routine".

A new folder is one row, not one per file, so that `npm install` is not
three thousand rows. Its row is high risk if any file in it would be.

Everything under `.git` other than `config` and `hooks/` is one row. Its
files mean nothing one at a time.

Whether a real file changed during the session is decided by comparing its
modification time with that of `CANARY.txt`, which is written into the
project when the session starts. Both times come from the same file system,
which matters on a Windows drive, where the clock is not this machine's.

Allowed file changes are applied before held commands and requests,
whatever order they were held in, because a held push needs the commits.

The layer is deleted at the end of the session unless an allowed change
failed to apply. Then it is the only copy of the agent's work and its path
is printed.

Deletions appear in the layer as character devices numbered 0:0, and a
folder that was deleted and made again carries the `user.overlay.opaque`
attribute. Both were checked on this machine's kernel (6.18) before the
code was written.

Reads cannot be observed with this design. Nothing claims they can.

Per-turn snapshots, `rehearse undo` and `rehearse turns` are not built.

## Policy

The policy is applied in one place, where an action is about to be held,
which the proxy and the stand-ins already share. So a rule means the same
for a request and for a command.

Order of strictness is allow, hold, refuse. The built-in rules come first:
an action they rate high is held whatever any file says. Then the
organisation's first matching rule, then the project's, which may make
things stricter than the organisation's result and never looser. A rule
that would loosen is dropped for that action and reported once per session.

Whether a project rule loosens an organisation rule is decided per action,
when both match it. That is exact. `rehearse policy check` has no action to
hand, so it compares patterns, and reports only what it can be sure of.

A rule with a `risk` and no `action` holds. A rating only means something
for an action that is held.

A refused action is recorded as held and refused in the same moment, with
the rule that refused it, so it is in the report and the record like any
other refusal. Nothing is kept to replay. The decision API will not accept
an approval for it.

An allowed action is not a held action. It is noted (`policy_allowed`) with
the rule, so the report can say how many went through and why.

The policy's hash covers the bytes of both files, with their lengths, so
the same rules written differently have different hashes. It says "these
files", not "these rules".

The files are read before the session's first event and never again. An
edit to the project's file during the session is found at the end by
comparing the file with what was read, and reported as a sensitive write.
In a sandbox the edit lands in the layer, and `.rehearsal/` is on the
sensitive list.

YAML needs a parser, and `gopkg.in/yaml.v3` is the one dependency added
for it. Unknown fields are an error, so a misspelt key cannot quietly turn
a rule into one that matches everything.

A learned rule is offered on the decision screen, not afterwards, because
the page stops being served when the session ends. It is written only
after the decision is carried out, as one line in flow style, which is
valid as an entry of the `rules` list whether the rest of the file is in
flow or block style. The file is parsed again with the line added before
anything is written. If the result isn't the same rules plus that one, the
file is left alone and the line is printed for adding by hand.

An offer needs three earlier sessions in which the action was allowed and
then performed. The session in which the offer is made doesn't count
towards its own offer.

## Package installs

The plan offered two ways to handle an install: pause the session until
the user decides, or, if that can't be done cleanly, stop high-risk
installs with an error and let the rest through. The second was taken. A
pause needs a decision taken while the agent runs, and everything here that
takes a decision (the page's three states, the decision API, the terminal
prompt) is built around the agent having exited. Stopping with an error the
agent can read is honest about what happened and needs none of that.

So an install is never a held action. It is checked, recorded, and either
stopped or run.

The stand-in asks the registry itself, through the proxy, before the real
package manager starts. Its client is told to use the proxy even for a
registry on this machine, which Go's default client would reach directly,
so the question is in the ledger wherever the registry is.

The registry's address comes from the same environment variables the
package managers read (`npm_config_registry`; `PIP_INDEX_URL`,
`UV_INDEX_URL`, `UV_DEFAULT_INDEX`), else the public one. A registry set
only in `.npmrc` or `pip.conf` is not picked up.

For npm the whole package document is fetched, because the short form has
no publish times. Documents over 32 MB are not read, and the package is
then rated medium as unknown.

"Runs a script when installed" means, for npm, a `preinstall`, `install` or
`postinstall` script, or `hasInstallScript`. PyPI has no such field. A
version with no wheel has to be built from source on this machine, which
runs the package's build code, so that is what counts there.

Seven days is the plan's figure. The publish time is taken from the
registry and compared with this machine's clock.

"Already in a lockfile" is a text search for the package's name in the form
each lockfile writes it, in the folder the command is run from. It doesn't
parse the lockfile or look in parent folders.

A version range (`^1.0.0`) or a tag is treated as "the latest version",
which is what the facts are then about. That is not always the version the
package manager will pick.

`pnpm`, `yarn`, `pip`, `pip3` and `uv` are stand-ins now, alongside `git`,
`npm` and `gh`. For anything but an install of named packages they hand
over to the real program at once.

## Replay and bench

A recording is made from a finished session's log, not captured on the
side while the session runs. The log already has everything a replay needs
and nothing it shouldn't keep: hosts, paths, sizes, which watched values
were seen where, what replies named which hosts, and each held action as
it was shown. So there is no second copy of the session to protect, and
`rehearse record` works on any past session.

A recording holds the kind of thing a request carried (`real`, `canary`,
`secret`), never the value. A replay makes values of its own to watch for
and puts one of the right kind back into the request, in the address for a
`GET` and in the body otherwise, so the scan finds it the way it would in a
live session.

A replay does not go through the proxy's socket. It hands each request to
the same object the proxy hands requests to, built the way a session
builds it. That was chosen so that a replay cannot contact anything: a
request that the rules let through has nowhere to go. The test for it runs
a replay inside a network namespace that has only a loopback interface.

A replayed command is the stand-in's report of it, not the command line
run again. Running it would need the repository it was run in.

Requests to the model provider are left out of recordings, apart from one
held at the provider's door, which is recorded with the request before it
so the two logins can be told apart.

Actions allowed during a replay are marked applied with a note that
nothing was done. Nothing is learned from a replay either: no approval is
counted and no rule is offered.

`rehearse bench` uses the rules alone, without the model, so its output is
the same every time. The reviewer can only raise a rating, so the model
could turn a "fine" into a held-as-high, never a "stopped" into a miss.

Bench's test for "stopped" is a high-risk hold or a refused install. A
held action still waits on the user, who may allow it. The table says what
the rules did, and a note under it says so.

The first run of each scenario, "without Rehearsal", plays it with nothing
held and counts the steps that go ahead. It is every step. It is there so
the two columns come from the same code and not from a sentence in a file.

Scenario files are built into the binary and also read from a folder when
one is named, so adding one is adding a file.

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
