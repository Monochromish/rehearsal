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

## The same scenarios without the demo

`rehearse bench` plays a set of recorded scenarios, these among them,
against the rules with no agent and no network. See the main README.
