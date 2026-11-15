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

`rehearsal-ledger.json` and `rehearsal-report.md` are written in the
session's folder, readable by you alone, and their paths are printed when
`rehearse` exits. They used to go in the working folder. In a real session
the agent read the ledger there, which holds your own details and
everything Rehearsal had noticed. In the working folder `rehearse attest`
writes `rehearsal-attestation.json`, and nothing else is written there
apart from `CANARY.txt` for the length of a session and, when you accept an
offered rule, one line in `.rehearsal/policy.yaml`. Without `--sandbox` an
agent can still read the session's folder if it goes looking. With it, the
folder is hidden.

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
- The email of the account Claude Code is signed in to is read from that
  program's own settings file (`.claude.json` in the home folder, or in the
  folder `CLAUDE_CONFIG_DIR` names), field `oauthAccount.emailAddress`. It
  is watched for like the git email. With no such file or field there is
  nothing to watch for and nothing is said. It matters because the agent
  program knows this address and you may not expect it to: a request to
  some other host that names the account says who is running the agent.

## What is stored

Per request: time, server, method, path without the query string, size and
response status. Per identifier found: which one, which part of the request
(address, header or body) and which form. Headers, cookies, query strings and
bodies are never stored.

A held request keeps one thing more, so the decision screen can show where
a watched value was going: the names of its query parameters. The values
are not kept. Where a value held something being watched for, the name of
that thing stands in its place (`k=[PAYMENTS_API_KEY]`), and every other
value is written `[hidden]`, because a credential can sit under any
parameter name. A watched value in the path is replaced by its name in the
same way.

A push to a remote that is a folder on this computer has that folder's
path in its command and its destination. The path isn't sent anywhere, so
a username found only there is not reported as carried. Commit messages
and file names are sent, and are still checked.

Only the program name of the wrapped command is stored. Its arguments may
hold secrets.

The watched values themselves, your real ones included, appear on the page
and in `rehearsal-ledger.json`. Both are local, and the file is in the
session's folder with owner-only permissions. Secrets are the
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

## Names under .localhost

A name ending in `.localhost` is reserved to mean this computer (RFC 6761).
curl and browsers connect such a name to the loopback address without
asking anyone. Go asks the system's resolver, and some systems don't answer:
under WSL the lookup fails. So `rehearse` never looks these names up. Its
proxy and the code that sends an allowed request connect them to
`127.0.0.1`, or to `::1` when nothing answers there. Plain `localhost` is
left to the system, which always knows it.

The proxy library makes its own connections and has no setting for how. It
does have one for an upstream proxy, so for these names it is pointed at a
small relay inside `rehearse` that accepts a `CONNECT` to a name under
`.localhost` and nothing else. A program run under `rehearse` reaches such a
name through the proxy and needs no resolution of its own. Run on its own,
it is back to what its own resolver knows.

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
never marked. Every later request that sends something to a marked host
is held as high risk, not only the first, because the reason holds for
each.

A plain read of a marked host is not held. At first every request to one
was, and an agent that followed a link in a page of documentation was
stopped for it. A `GET` or `HEAD` with no body and no watched value in it
goes through, and its entry names the server whose content gave the
address, so the record still shows the link being followed. What makes
it "plain" is checked on the request and not on its address: a watched
value anywhere in it, a body of any length, or any other method, and it
is held as before. A request can still say something in its path or
query that is not watched for. That is the cost, and it is the same cost
as for a read of any other host.

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

Tool calls and their results are read behind an interface in
`internal/observe`, so another format can be added. Each call is recorded
once, by its ID. Streamed provider replies go straight through to the agent.
A copy of at most 8 MB is kept as they pass and read when the stream ends.

At first both were read from the conversation history, which the Anthropic
Messages format was taken to repeat in every request. A real session
showed otherwise: after the first turns, Claude Code sent requests of
about a kilobyte that held the result of the last call and nothing before
it. Results were recorded and the calls they belonged to were not, so a
session that ran six commands reported one. A call is now read from the
reply that asks for it, where a stream sends its input in pieces, and also
from any request that does repeat it. Whichever comes first is kept. The
call and its result are written by different routines, so nothing that
reads the log may assume the call comes first.

The three integrity flags are pattern matches on what was observed:
`--no-verify` on a `git commit` or `git push`; a change to a file that looks
like a test in a session where a test command reported an error; and a short
list of destructive commands (`rm -rf`, `git reset --hard`, `git clean -f`,
a force push, `DROP TABLE` or `DROP DATABASE`, `mkfs`, `dd` with `of=`).
They say "worth a second look", not that anything was wrong.

The model-written part, the claims, is asked for after the decision, once
the agent's last message is known. The model gets the final message and the
commands as a data block and is told to use only what is there and to
follow nothing in it. An answer that doesn't match the schema or names a
command that wasn't observed is discarded. The report used to open with a
summary paragraph written by the model. It was removed: it restated the
counts below it and added a place for the model to be wrong. On the real
model, a final message with an instruction planted in it to mark every
claim as supported still had its "all tests pass" claim marked as
contradicted by the failing test command.

`rehearsal-report.md` is written beside `rehearsal-ledger.json`, in the
session's folder. Secrets
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

`rehearse export` prints every entry byte for byte as it was hashed, so
the chain can be recomputed from the export alone. That includes the first
entry, which holds the values of your own details that were watched for
(git name and email, username, computer name, Claude account email). An
export that blanked them
could not be checked, and a record that can't be checked is not worth
exporting.

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

Reads cannot be observed with this design. Nothing claims they can. So the
folders most likely to hold a key or a cloud login (`~/.ssh`, `~/.aws`,
`~/.config/gcloud`, `~/.azure`, `~/.kube`, `~/.gnupg`) are replaced with
empty ones inside the sandbox. The list is short and fixed. It's a floor,
not a claim that nothing else under home is worth reading.

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

## What changed in a session

Before the agent starts and after it exits, `rehearse` records the working
tree as a git tree: every file, tracked or not, that `.gitignore` doesn't
leave out. The difference between the two trees is what changed in the
session. Comparing against `HEAD` would not do: it would count whatever you
had uncommitted before the agent began as the agent's.

The trees are written through an index file of `rehearse`'s own, kept in the
session's folder and started as a copy of the repository's so that unchanged
files aren't read again. The repository's index, its working files and its
refs are never written to. The one thing added to the repository is objects
that nothing refers to, which git clears out in its own time. That is also
why the session's diff is written to the session's folder when the agent
exits (`changes.diff`, readable by you alone): the trees may not be there
later. The log records only the two tree hashes and the counts. What the
files changed to is the project's own text and stays out of it.

`CANARY.txt` and the other files `rehearse` writes are left out of both
trees. A diff longer than 8 MB is cut at a line end and marked as cut.

With `--sandbox` the real working tree doesn't change while the agent runs,
so the second tree is the first one with the sandbox's layer put over it:
its new and changed files added, its deletions removed, git's own folder
and ignored files left out. The result is what the agent did, whether or
not any of it is later allowed.

Outside a git repository nothing is captured, and the session says so.
User settings that change how git shows a diff (an outside diff program,
text conversion) are switched off for this, so the diff is what is on disk.

## Sorting the changes

The session's diff is split into parts: one for each hunk of a file that
was changed, and the whole file when it is new, deleted, renamed or binary.
Each part gets one of three levels by rules alone, with no model:

- Mechanical, shown folded: white space only, a move with nothing else
  changed, lockfiles, generated files, vendored folders.
- Skim: documentation, comments only, a new test file, editor and formatter
  settings.
- Read: everything else.

The rules set a minimum. Nothing in them is a reason to read less than
"read", only reasons that a part may be read less carefully, and each of
those is narrow on purpose:

- "White space only" compares the removed and added lines with white space
  taken out, except inside quoted text, where it is part of a value. It is
  never applied to Python, YAML, Makefiles or the other languages where the
  space at the start of a line carries meaning.
- "Comments only" needs every changed line to be a comment in a language
  the rules know. A comment that a compiler or checker acts on
  (`//go:build`, `// @ts-ignore`, `# noqa`, a `#!` line) is read.
- A renamed file is as demanding as the most demanding change made to it on
  the way. Moved and one character of logic changed is "read".

Some things are always read whatever else is true of them: any change to a
test that already existed (said as "removes an assertion", "deletes a
test", "marks a test skipped" or "changes an assertion" when the rules can
tell), dependency files, CI configuration, the paths the sandbox calls
sensitive, database migrations, a file whose name suggests secrets, and any
added line that looks like a credential. These are checked before the
rules that lower a level, so a credential pasted into a guide is read.

The test for a weakened assertion is crude: it counts lines that look like
assertions on each side of the hunk. It can't tell a weaker assertion from
a stronger one, so both are read, with the reason "changes an assertion".

Each part carries its reason in a few words. A person who disagrees with a
level can see what it rests on.

## Every session, read-only, and a window of its own

The page server used to answer for one session, the live one. It now also
answers for every session recorded on this computer, read-only:

- `GET /api/sessions` lists them, newest first, with the folder each ran in.
- `GET /api/sessions/{name}/ledger`, `/changes`, `/change?id=`, `/entries`
  and `/record` are that session's ledger, its sorted changes, the text of
  one change, its log entry by entry exactly as hashed, and whether its
  record verifies (the chain, and the signature against this computer's
  key).
- `GET /api/info` gives the version and the name of the local model.

A past session is opened for reading when asked about and closed again. Its
name must be a plain folder name, so nothing outside the sessions folder
can be reached. Nothing about a past session can be changed through the
API: the only request that changes anything is still the decision on the
live session's held actions.

These exist so that more than one front end can show a session. The page
built from `web/` is the one anyone can build from this repository.
`REHEARSAL_APP` may name another program to show the session in a window of
its own. When it is set and the program is there, `rehearse` starts it as
`PROGRAM -url ADDRESS`, in the program's own folder, with the address the
session is served at. The address is an argument and not an environment
variable because the program may be built for another system and started
across the boundary (a Windows program from inside WSL), where neither the
environment nor the working folder carries over. That program talks to
`rehearse` only through this API, so it needs nothing else. When it is set and the
program can't be started, `rehearse` says so and opens the page in a
browser as before. Such a program is built separately and is not part of
this repository, which is why `desktop/` is in `.gitignore`.

## Investigating a record

`rehearse investigate` answers "what happened here, and why should I
believe you" for someone who was not there. The second half of that
question decided most of the design.

**Rules find, a model only retells.** Extraction, linking and findings are
plain code over the log's entries. They give the same bytes for the same
records, and a test runs them twice and compares. The local model is
handed the rules' statements after the fact and may add a few sentences.
It cannot add a finding, remove one or change one, because it is never
asked to.

**A model's sentence needs a citation that code can check.** The model is
told to end each sentence with entry citations. Code then keeps a sentence
only if it has at least one citation and every citation is an entry the
model was given. A fluent sentence with an invented entry number is
removed, and so is one with none. This is also the defence against text in
the record that tells the model what to conclude: an obedient conclusion
has nothing to cite. The report counts what was removed, so a model that
wanders is visible.

**Three labels, never mixed.** "Observed" is one entry. "Derived" is a
named rule over several, with the rule's wording listed. "Written by a
model" is prose. A reader who trusts nothing but the log can read the
first kind only; the raw lines are printed under every finding with the
supporting part named.

**Per-entry verification, as well as per-record.** `rehearse verify` stops
at the first bad entry. An investigator needs more: which findings does
the damage touch? So intake recomputes every entry and marks each one that
does not match its hash or does not follow the one before. When a link is
broken, both sides of it are marked, since either could be the altered
one. A finding says so when it rests on a marked entry. Every finding from
a record that fails as a whole says that too, in a separate line, so the
two are not confused.

**The capture side had to say more.** A finding can only show what the
log holds, so three things were added to what is recorded: for each newly
named host in content read from outside, a short excerpt around its
address and whether that text sat in an HTML comment; for each watched
value found in a held request, where it was and in what form; and for an
encoded value, the encoded text and a hash.

**Proof without the secret.** The log has always kept a salted hash for
each `.env` secret and never the value. When a secret is found encoded,
the recorder now decodes the text, takes the part that is the secret, and
hashes it with the same salt. It is hashed from the decoded bytes and not
copied from the identifier, so the two hashes being equal is a check and
not a restatement. The encoded text of a real secret is stored cut to its
first eight characters with its length: the whole of it would be the
secret to anyone who can run `base64 -d`. A planted value's is kept whole,
and for those the rules decode it again at investigation time and compare
it with the value the first entry lists.

**Excerpts keep no paths.** Every address inside a stored excerpt is cut
to its scheme and host, as everywhere else in the log, so an excerpt
cannot carry a token in a query string into the record.

**Two findings beyond the list.** "A watched value sent outside" and "an
action the rules rate as high risk" were added so that every attack in the
scenario set has a finding to be measured against. The second applies the
same rating rules that decide what is held, to the held action's entry,
and says so as a derived statement.

**Limits are part of the report.** "What the record cannot show" is
printed every time. An absence of findings means the rules found nothing
in what was recorded, and the section says what was not recorded.

**A commit message is searched too.** A push whose commit subject holds a
watched value now records a sighting with "commit message" as the place,
and a value that is not a planted one is replaced by its name in the
stored subject. Before this, a secret in a subject that matched no known
credential format was stored as it was.

## The reviewer reads the changes

The rules sort a session's changes without understanding them. The local
model can say what a change does, so it is asked, about the parts the rules
ranked "read" and "skim", for one sentence each.

**It can raise and cannot lower.** The model's answer is a sentence and
one of two words, "read" or "skim". The log entry that records it has a
field for "raised" and no field for the opposite, and the code sets
"raised" only for a part the rules had under "skim". So a part the rules
say to read stays there whatever the model answers, and a mechanical part
is never asked about. A model that is fooled can make a person read
something they did not need to. It cannot make them miss something.

**The diff is data.** It is wrapped in one block, text in it that would
close the block is rewritten, credentials are masked, and the instructions
say that text in the diff telling the model what to conclude is part of
what it is describing. The same rule as for held actions applies to
purposes: it says what the code does and never why.

**A budget, and an honest account of it.** 40 parts and 90 seconds, the
parts most in need of reading first. Two entries mark the reading starting
and ending, and the second says how many parts were left and why: past the
first 40, out of time, or the model stopped answering. A front end can
show which parts are still to come from the first entry alone.

**Sentences are in the log.** They are entries like any other, so they
are covered by the chain and the signature, a past session shows the
sentences it was given, and the investigation can cite them. The cost is
that the record cannot be closed until the reading ends, so `rehearse`
waits for it, at most the 90 seconds.

**Two failures in a row end it.** One unusable answer costs one sentence.
After a second in a row the model is taken to have gone, and the session
is not held up waiting for 38 more timeouts.

## Review without a session

`rehearse review` exists because the sorting is useful on changes that no
`rehearse` session produced. It reuses everything: the same snapshot
through a scratch index, the same diff, the same rules, the same budgeted
reading by the model.

**It is recorded as a session.** A review writes a session folder with a
log, the diff and a signed checkpoint. That costs a few kilobytes and buys
three things without new code: the API already serves it, so the app
window shows it; `rehearse verify` and `rehearse investigate` work on it;
and "what did I look at before I merged this" has an answer later. The
first entry says it was a review and of what, and lists no watched values,
because nothing was run and nothing could leave.

**Ranges are git's, read narrowly.** `A..B`, `A...B` and a single name are
accepted, each resolved to a tree with `rev-parse --verify` after
`--end-of-options`, so a name that starts with a dash is a name. A single
name means "since that commit", against the files as they are, which is
what `git diff NAME` means and what someone checking an agent's
uncommitted work on a branch wants.

**A review that cannot be made leaves nothing.** A bad range or a folder
that is not a repository is an error, and the session folder made for it
is removed.

**The window, or the terminal.** The browser page does not show changes,
so opening it would show an empty session. The review opens the app when
one is named, and otherwise prints.

## Measuring the sorting

The sorting rules decide what a person is told they can skip, so they
need a number, and the number has to be able to come out badly.

**Labels are a person's, written first.** Each labelled part says what a
careful reviewer would want: read, skim or mechanical. The labels were
not adjusted after the rules were run. Two parts disagree with the rules
and were left that way, because they are true: a reworded Python
docstring and added blank lines in Python are both read by the rules and
would be skimmed or skipped by a person.

**One direction of error fails the bench.** Reading more than needed
wastes minutes. Folding away something that had to be read is the failure
the whole feature exists to avoid. So only that one, a part labelled read
and placed in Mechanical, is printed by name and makes `bench` exit 1.
Read placed in Skim is listed and counted and does not fail it: a skimmed
part is still looked at.

**One part per file, with its label in the file.** A labelled part is a
`.diff` that anyone can open, with three comment lines on top. There is
no separate index to drift out of step, and a file that holds more than
one part is refused, so a label always refers to exactly one thing.

**It says it is small.** 39 parts by the author of the rules measure
whether the rules do what was intended. They are not a sample of real
changes, and the output and the README both say so.

## What review is for

The project began as a way to hold what an agent sends out. That is one
third of what someone needs after an agent has worked unattended. The
other two are what it changed and whether what it said about its work is
true. The opening of the README now says all three, in the order a person
meets them, and the tool has a part for each: the sorted changes, the
claims set against the commands, and the held actions.

**The lazy path has to be safe.** People who run agents unattended do not
read much, so what is read first has to be the thing that matters. The
choices that follow from that are the same in every front end. The
sentence at the top names the single most serious thing in plain words
and only then counts the rest; a bare count is never shown when something
is high risk. Low-risk actions are one row, because six rows that all say
"routine" teach a person to stop reading rows. A row's second line is a
fact about the action that a person would not expect (the address came
from a page; it overwrites the remote), taken from the action's fields,
and not a model's description of the request. The button names the most
serious thing it will allow, so that it is read at the moment of pressing.
Ticking something high risk opens its detail and does nothing else: no
timer, no second confirmation, nothing to type. Friction that is the same
every time is learned and stops being read.

**Shorter, with nothing taken away.** Every one of those cuts moves text
behind one click and removes none. In the investigation the rule is
stricter, because that screen is judged on whether its claims can be
checked: a finding's headline is ten words or fewer, and the raw log
entry that proves it is shown in full in the detail without a click.
Custody is one line that is always on screen, with the long form behind
it. The pipeline gives each finding its headline, its supporting line and
its key entry, so that every front end shortens it the same way and none
has to decide which entry is the proof.

**It is counted.** With three or fewer things waiting, everything on
screen before a click is at most 60 words. A test counts it on the
scripted sessions.

**The window is separate.** The app window is another program, built from
other code and kept out of this repository. Everything it shows comes
over the local HTTP API, which is documented and tested here, so the page,
the window and anything else can be front ends on equal terms, and the
window can run on a different system from `rehearse`.

## One palette, and what each colour may be used for

The page's colours are ten named values for each theme, defined once in
`web/src/theme.ts`: three surfaces (window, panel, raised), two text tones,
a hairline, one accent and three risk colours. Nothing else is a colour.

**Each colour has one job.** The accent is for focus rings and links. It is
a dark blue close to the text colour, so it cannot mark a link by colour
alone, and every link is underlined as well. A button is ink: the text colour behind, the panel colour for its label. The
hairline is decoration; an edge that has to be seen, such as a button's
outline, is the secondary text tone.

**Risk colours belong to three words.** Red, amber and green appear only
on "High", "Medium" and "Allowed". A warning, an error, a failed action or
a server that received something is said in words at a heavier weight and
is not coloured. Before this, red also marked warnings and failures, which
made it mean "look here" as well as "high risk", and a reader who cannot
tell red from green lost both. Colour never carries a meaning by itself.

**Measured.** Contrast against the panel and against the raised tone, the
two surfaces text sits on:

| | Light, panel | Light, raised | Dark, panel | Dark, raised |
|---|---|---|---|---|
| Primary text | 17.04 | 13.58 | 14.77 | 12.34 |
| Secondary text | 9.11 | 7.26 | 8.64 | 7.22 |
| Accent | 10.61 | 8.46 | 11.37 | 9.50 |
| High | 9.01 | 7.18 | 5.98 | 5.00 |
| Medium | 6.31 | 5.03 | 9.85 | 8.23 |
| Allowed | 6.05 | 4.82 | 11.47 | 9.59 |

Every pair is above 4.5 to 1, and secondary text in the light theme is
above 7 to 1. The values are written as hex. The page hands them to the
browser as custom properties, where a colour the browser cannot read has
no fallback, so a second notation would add a way to fail and no benefit.

## The first words of a session

A list of sessions named "claude, 15:35" and "claude, 15:34" says nothing
about which is which. The record now keeps the first eight words of the
first thing the person asked, in one entry, and the list can show them.

This was the first part of a conversation to be stored, so it is kept
small on purpose (the second is in "Said against observed"): eight words and sixty characters at most, taken
from the first message from the user and no later one, with watched values
replaced by their names and anything shaped like a credential hidden. A
note the agent program wraps in a tag for the model is passed over, and
so is a single word, which is what the program's own start-up checks send.
The words are read from the Anthropic Messages format only, like the
commands and the token counts. A session with another provider, or with
no readable conversation, has no such entry and is named by the program
and the time as before.

## Said against observed

The report already had "Claimed versus observed", written by the local
model after the decision. This is the same idea reduced to one sentence
that needs no model, so that every session has it, it is the same every
time, and each half can be traced to entries.

**Three forms, in order.** A claim the record contradicts comes first,
because it is about the agent's own words. What the agent was told about
held actions comes second: it is true of every session that held
anything, so it would crowd out the rarer and sharper first form if it
came first. When neither applies there is no sentence. A line that is
always there is a line nobody reads.

**Only what the record shows.** A claim is contradicted by an entry or
not at all. "No test command ran" needs the session's commands to have
been readable, and rests on the commands that were seen. "This repository
has no new commit" is worded that way because commits made elsewhere are
not seen. A push is judged by what became of the held push, not by the
command, which the stand-in reported as a success.

**The claim has to be an entry.** The last message was never stored, and
a sentence that cites entries cannot rest on memory. So when the agent
exits, the rules read the last message and write one `agent_claims` entry:
the kind of each claim found and the first fourteen words of the sentence
that made it, with watched values replaced by their names. That is a
second piece of the conversation in the record, kept as small as the
first. Sentences that claim nothing are not kept.

**Patterns, tilted towards silence.** Recognising a claim is a short list
of patterns over one sentence at a time, and a sentence with a hedge in
it ("not", "should", "once", "if", "can") is skipped whole. A missed
claim costs a sentence that was not shown. A claim invented from "once
the tests pass" would be an accusation with nothing behind it.

**The most serious action is named from its fields.** The same order as
the reviewer's floor: a deletion, then a secret, a detail of yours, a
planted value, a force push, a publish. A deletion is named from the last
two parts of its address, so `/v1/databases/production` is "database
production". Nothing a model wrote is in the sentence.

**Built with the ledger.** The sentence is worked out wherever the ledger
is, from the log alone, once the session is finished. So the page, the
terminal, the report and a past session read back later all say the same
thing, and `rehearse investigate` gets it by building the ledger from the
record it was given.

## Side by side

`demo/side-by-side.sh` exists to show one thing at a glance: the same
script, the same moment, two outcomes.

**The secret is the viewer's.** A made-up key in a file is easy to
shrug at. A word someone has just typed, turning up in an attacker's
inbox, is not. So the script asks for one, and the rest follows from
taking that seriously: it is written to two `.env` files and nowhere
else by the script, any character is allowed (the agent script now
encodes it when it puts it in an address), and `--reset` removes every
file the script made, the session's state included, which is why that
state is kept inside the script's own folder.

**Two copies in one server.** The demo server can serve a second set of
its three services on other ports, and the page that compares them reads
both from memory. Two processes and a page that asked each across origins
would have needed the services to allow that.

**The page is text.** Two columns, the Rehearsal page's colours by the
same names and its sizes, nothing boxed. A test compares the colours with
the page's own palette file, so the two cannot drift apart unnoticed.
What an inbox received is put on the page as text and never as markup,
because it is whatever someone typed.

**Two steps left out.** The npm step, which fails without Rehearsal on a
machine where Node cannot find the registry by its name, and the step
that sends the signed-in Claude account's email. The second would put a
real address on a shared screen, and would make the count of held actions
depend on the machine. Both runs are started as if no
account were signed in.

**The session's diff no longer keeps a secret.** Writing the test for
this found the typed password in `changes.diff`: the script adds `.env`
by force, so the file is part of what changed, and the diff was kept as
it stood. A secret's value is not supposed to be kept anywhere, so the
diff now has each secret replaced by its name before it is written. Only
secrets are replaced. A diff with the user's own name in it is the
project's text and stays as it is.

## What the agent left running

`rehearse` starts the agent, which is the one fact that makes this
possible: everything the agent starts can be traced back to a process
`rehearse` knows.

**A process is its number and its start time.** A number alone is reused
by the system, and a name says nothing about where a process came from.
The pair is read from `/proc` and is the only identity used, for listing
and for stopping.

**Two kinds of evidence, either one enough.** Every 300 milliseconds the
process table is read, and any process whose parent is already followed
is followed too. That finds a child however it detaches, as long as it is
seen once while its parent lives. The agent is also started with a
variable in its environment whose value is made for the session. Children
inherit it, so a server that forks twice and is adopted by the system
within a few milliseconds is still recognised. Neither is asked to do the
other's job: descent covers a child started with an empty environment, and
the mark covers a child that loses its parent at once.

**It errs towards missing.** A process is listed only on positive
evidence. What escapes both kinds of evidence is not listed and so cannot
be stopped. Making `rehearse` the adopter of orphaned processes would
close that gap, but then its own children (the app window, git) would
have to be told apart from the agent's, and a mistake there stops
something the agent did not start. That trade was not taken.

**Stopping checks again.** A handle on the process is taken first, and
only then is its start time compared with the one on the list. The signal
goes through the handle, so a process that exited and whose number was
given to something else is never signalled. One found to have exited is
"already gone" and is sent nothing. The polite signal is `SIGTERM`. The
forced one, `SIGKILL`, follows after 3 seconds, and the answer says which
it took. Each process is signalled alone, not its group: the others in
the group are on the list themselves if they came from the agent.

**Half a second.** A pipeline the agent was running when it exited takes
a moment to die. The list is made half a second after the agent exits, so
that what is on it was left and not just slow.

**Two entries in the record.** `process_left` for each process on the
list, and `process_stopped` for each one somebody asked to have stopped,
with what became of it. The command line is stored, which is more than
the program name kept for the agent itself. It is treated as a held
command's is: anything shaped like a credential is hidden, and a watched
secret is replaced by its name.

**When `rehearse` waits.** With the page open and nothing chosen yet,
`rehearse` stays until the page says which to stop, since a list that
disappears with the page server would be no use. With `--no-open` it
prints and leaves everything running, and `--stop-leftovers` is the way
to have them stopped without being asked. The app window does not show
the list yet, so with it `rehearse` behaves as with `--no-open`. A wait
for an answer the window cannot give would never end.

**In the sandbox, from outside it.** The sandbox does not end when the
agent does. Its first process waits for everything inside to exit, so a
server the agent left keeps running in there, cut off from the network
once `rehearse` has gone. Processes inside a sandbox have numbers out
here too, so they are followed, listed and signalled from outside like
any others, and nothing new runs inside. The sandbox's first process
descends from what `rehearse` started and would otherwise be on the list.
It is recognised by being process 1 of a process namespace directly
inside this one, which the kernel reports, and left off.

**The rows.** The group is a component of its own, drawn with the same
rules as a held action's row and the decision's button and with no colour
or size of its own. It is not the action row itself, because that row is
built around a held action and its reviewer's sentence.
