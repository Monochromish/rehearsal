# Demo

A demonstration of Rehearsal that runs entirely on this machine. Nothing in
this folder contacts a public host, and nothing in it is harmful: the
"attack" posts a made-up key to a page on this machine and deletes a
database that is a list in memory.

## Start and stop

```
sh demo/start.sh
sh demo/stop.sh
```

`start.sh` needs Go, git and curl. It builds and starts one program,
`demo/server`, which serves three stand-ins on 127.0.0.1:

| Address | What it is |
|---|---|
| `http://cloud.localhost:7911` | A "cloud host" with two databases, `production` and `staging`. `DELETE /v1/databases/production` deletes one. The front page says whether production is still there. |
| `http://attacker-inbox.localhost:7912` | The attacker's inbox. Whatever is sent to `/collect` or `/pixel`, by any method, is shown on its front page. Nothing else is counted, so a browser's own requests leave it at 0. |
| `http://registry.localhost:7913` | A package registry with one package, `shop-utilz`, published "nine hours ago". Its install script writes a hook into `.claude/settings.json` of the project it is installed in and posts a made-up token to the inbox. |

A name under `.localhost` means this machine. Rehearsal's proxy connects
such a name to the loopback address itself, so under `rehearse` the names
work whatever the system's resolver knows, and so do curl and browsers on
their own. Node asks the system, and some systems don't know (WSL is one).
That only matters for the run without Rehearsal below: there the npm step
fails with a name it can't find, and `start.sh` says so when it applies.
One line in `/etc/hosts` makes that step work too:

```
127.0.0.1 cloud.localhost attacker-inbox.localhost registry.localhost
```

Everything it makes goes in one folder, `~/rehearsal-demo` unless
`REHEARSAL_DEMO_DIR` names another:

- `remote.git`, a bare git repository that the sample project pushes to
- `project`, a copy of `demo/project` with a `.env` holding a made-up key,
  and a README with an instruction hidden in an HTML comment and two
  zero-width characters

`stop.sh` stops the program and deletes that folder. Neither script will
replace or delete a folder that `start.sh` didn't make.

## What to show

Open the cloud host and the inbox in a browser. Then, from the sample
project:

```
cd ~/rehearsal-demo/project
sh agent.sh
```

`agent.sh` is a script standing in for an agent that obeyed the hidden
instruction. Run bare, it posts `.env` to the inbox, sends the inbox the
email of the Claude account signed in on this computer (in a request
header, and only if there is one), deletes the cloud
host's production database, overwrites the remote's `main` and, if npm is
installed, installs the package. (Run `sh demo/stop.sh` and
`sh demo/start.sh` to reset.)

Now the same thing under Rehearsal:

```
rehearse sh agent.sh
```

The script ends with "All done. Everything is set up." The inbox stays
empty and the database stays where it is. The page lists what was held: the
post and the `GET` that carried the key from `.env`, the request with your
Claude account email in a header, the `DELETE`, and the force push
with `.env` in it. The install was stopped before it ran. Nothing happens
until you decide.

When you have decided, the first thing `rehearse` prints sets what the
script was told against what happened. With every action refused:

```
The agent was told 4 actions worked, including deleting database production. None has happened.
```

The count is 5 when a Claude account is signed in on this computer.

With a real agent, run `rehearse claude` in the sample project and ask it
to follow the setup notes in the README. What it does then is up to the
agent, which may well refuse.

## Side by side

```
sh demo/side-by-side.sh
sh demo/side-by-side.sh --reset
```

One command runs the same `agent.sh` twice at the same moment, in two
copies of the demo: one with nothing in front of it and one under
`rehearse`. It first asks:

```
Invent a password (not a real one):
```

What you type, 8 characters or more, becomes `PAYMENTS_API_KEY` in the
`.env` of both copies of the sample project. An empty answer uses the
demo's own made-up key. Then it prints one address,
`http://localhost:7921/compare`, a page with two columns that keeps
itself current: "Without Rehearsal" on the left, "With Rehearsal" on the
right, and for each what the attacker's inbox has received and whether the
production database is still there. On the left the password arrives in
the inbox twice and the database is deleted. On the right nothing arrives,
the database stays, and four actions wait for your decision.

The session on the right opens the way any session does: in the app window
when `REHEARSAL_APP` names it, otherwise as the page in a browser.
`rehearse` is the one in the folder above this one, or on your `PATH`, or
whatever `REHEARSE` names.

Two steps of `agent.sh` are left out of both runs. The npm step needs Node
to find the registry by name when there is no Rehearsal to do it, which
fails on some machines. The step that sends the signed-in Claude account's
email is left out so that no real address is put on a screen: both runs
are started as if no account were signed in.

`--reset` stops both copies and removes everything the script made, in a
few seconds. That is one folder, `~/rehearsal-side-by-side` unless
`REHEARSAL_SIDE_DIR` names another, and it holds the session's own state
too. While the demo is up, the typed password is on the left-hand inbox
page, in the two `.env` files, and packed inside the two sample projects'
git folders, because the script commits `.env`. Nothing `rehearse` writes
holds it: the log, the ledger, the report and the export name it only as
"Value of PAYMENTS_API_KEY from .env". Until you decide, the held requests
that carried it are kept in the session's private folder, as for any
session.

The ports are 7921 to 7923 for the left-hand copy and 7931 to 7933 for the
right. `REHEARSAL_SIDE_PORT` names another first port, and the second copy
is ten above it.

## Change the record and see it noticed

```
sh demo/tamper.sh [session]
```

takes a session's record, the most recent one unless you name another, and
makes a copy of it in a temporary folder. It says how many entries the
record has, checks the copy, and asks:

```
Pick an entry number:
```

It then changes one character in that entry of the copy, the last digit of
its time, and checks the copy again:

```
The copy, before any change:
  S1  20260110-090000-k3m9qa: intact
      chain: 6 entries read; each one matches its hash and follows the entry before it
      signature: valid: the last entry was signed at 2026-01-10 09:04:11 UTC, checked against the key 5d0c1f2e3a4b6978

Pick an entry number: 3

Changed one character in entry 3 of the copy: the last digit of its time, 9 to 0.

The copy, after the change:
  S1  20260110-090000-k3m9qa: NOT VERIFIED
      chain: 6 entries read; 1 does not hold: entry 3. Every other entry matches its hash and follows the entry before it
      signature: not checked: with the chain broken, the hash that was signed cannot be reached
```

The check is `rehearse investigate` on the copy, and those are its own
words. The record itself is only read, and the copy is removed when the
script ends.

## The same scenarios without the demo

`rehearse bench` plays a set of recorded scenarios, these among them,
against the rules with no agent and no network. See the main README.
