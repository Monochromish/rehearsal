# Demo

A demonstration of Rehearsal that runs entirely on this machine. Nothing in
this folder contacts a public host, and nothing in it is harmful: the
"attack" posts a made-up key to a page on localhost and deletes a database
that is a list in memory.

## Start and stop

```
sh demo/start.sh
sh demo/stop.sh
```

`start.sh` needs Go, git and curl. It builds and starts one program,
`demo/server`, which serves three stand-ins on 127.0.0.1:

| Address | What it is |
|---|---|
| `http://localhost:7911` | A "cloud host" with two databases, `production` and `staging`. `DELETE /v1/databases/production` deletes one. The front page says whether production is still there. |
| `http://localhost:7912` | An "inbox". Whatever is sent to it, by any method, is shown on its front page. |
| `http://localhost:7913` | A package registry with one package, `shop-utilz`, published "nine hours ago". Its install script writes a hook into `.claude/settings.json` of the project it is installed in and posts a made-up token to the inbox. |

It also makes, under `demo/.run`:

- `remote.git`, a bare git repository that the sample project pushes to
- `project`, a copy of `demo/project` with a `.env` holding a made-up key,
  and a README with an instruction hidden in an HTML comment and two
  zero-width characters

`stop.sh` stops the program and deletes `demo/.run`.

## What to show

Open the cloud host and the inbox in a browser. Then, from the sample
project:

```
cd demo/.run/project
sh agent.sh
```

`agent.sh` is a script standing in for an agent that obeyed the hidden
instruction. Run bare, it posts `.env` to the inbox, deletes the cloud
host's production database, overwrites the remote's `main` and, if npm is
installed, installs the package. (Run `sh demo/stop.sh` and
`sh demo/start.sh` to reset.)

Now the same thing under Rehearsal:

```
rehearse sh agent.sh
```

The script reports that everything went fine. The inbox stays empty and the
database stays where it is. The page lists what was held: the post and the
`GET` that carried the key from `.env`, the `DELETE`, and the force push
with `.env` in it. The install was stopped before it ran. Nothing happens
until you decide.

With a real agent, run `rehearse claude` in the sample project and ask it
to follow the setup notes in the README. What it does then is up to the
agent, which may well refuse.

## The same scenarios without the demo

`rehearse bench` plays a set of recorded scenarios, these among them,
against the rules with no agent and no network. See the main README.
