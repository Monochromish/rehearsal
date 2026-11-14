# The record, and attesting to a session

Rehearsal keeps a log of everything it saw and did in a session. This page
describes how that log is protected against quiet edits, how to export it,
and how to attach proof of a session to a pull request.

## The chain

Every entry in a session's log carries the SHA-256 hash of the entry before
it. An entry's own hash is taken over this text, with a line feed between
the five parts and nothing after the last:

```
<seq>
<time>
<type>
<prev>
<data>
```

- `seq` is the entry's number, starting at 1, in decimal.
- `time` is when it was written, in UTC, RFC 3339 with nanoseconds, exactly
  as stored (trailing zeros in the fraction are dropped).
- `type` is the event type, such as `request` or `action_held`.
- `prev` is the hash of the entry before, as 64 lower-case hex characters.
  The first entry carries 64 zeros.
- `data` is the entry's JSON exactly as stored.

Change, remove or reorder an entry and either its own hash or the next
entry's `prev` stops matching.

## The checkpoint

A chain on its own doesn't stop someone from cutting entries off the end,
or rewriting the log from some point on and recomputing every hash. So when
a session ends, Rehearsal signs the number of entries and the last hash
with an Ed25519 key, and writes the result to `checkpoint.json` in the
session's folder.

The key is made the first time a session is recorded, in the `rehearsal`
folder of your configuration directory (`~/.config/rehearsal` unless
`XDG_CONFIG_HOME` says otherwise):

- `signing-key.pem`: the private key, readable by you only. Rehearsal
  refuses to use it if anyone else can read it.
- `signing-key.pub`: the public key. This is the one you give to others.

The signature covers this text:

```
rehearsal checkpoint v1
<session name>
<number of entries>
<final hash>
<time signed, RFC 3339, to the second>
```

## Checking a session

```
rehearse verify [session]
```

recomputes the chain and checks the checkpoint against it and against your
own public key. With no session named it checks the most recent one. A
session is named by its folder under `~/.local/state/rehearsal/sessions`,
or by a path to that folder.

It exits 0 and prints "Record verified" when everything matches. Otherwise
it exits 1 and names the first entry that doesn't match:

```
rehearse: record does not verify, starting at entry 17: entry 17 has been changed since it was written
```

What it can and can't tell you:

- An edited, missing or reordered entry is named.
- An entry that was edited and had its own hash recomputed shows up at the
  entry after it, which no longer follows.
- Entries cut from the end, or added after signing, are found through the
  checkpoint.
- A log rewritten in full with a consistent chain fails, but no entry can
  be named.
- A session with no checkpoint (it was killed, or no key could be made)
  fails, because entries missing from its end could not be noticed.
- Anyone who holds your private key can sign a rewritten log. The signature
  shows that the holder of the key signed this log, and nothing stronger.

The page shows one line of this at the end of a session, "Record verified"
or "Record does not verify, starting at entry N". That line checks the
chain only: the checkpoint is signed after the page's last update.

## Exporting a session

```
rehearse export [session] --format jsonl
```

prints one JSON object per line, one per entry, oldest first:

| Field | What it is |
|---|---|
| `session` | The session's name. |
| `seq` | The entry's number, from 1. |
| `time` | When it was written, UTC, RFC 3339 with nanoseconds. |
| `type` | The event type. |
| `data` | The event itself. Its fields depend on `type`. |
| `prev` | Hash of the entry before, as in the log. |
| `hash` | This entry's hash, as in the log. |

These names are stable. New event types and new fields inside `data` may
appear in later versions; existing ones won't be renamed.

The log never holds request bodies, headers, cookies or the values of
secrets, so there is nothing of that kind to remove. Every line's `data` is
byte for byte what its `hash` was computed over, so the chain can be
recomputed from the export alone: `hash` is the SHA-256 of `seq`, `time`,
`type`, `prev` and `data`, joined by newlines.

The first entry (`session_started`) holds the values of your own details
that were being watched for: git name and email, username and computer
name. They are in the export too. Keep that in mind before sending it
anywhere.

The event types are `session_started`, `request`, `response`,
`identifier_seen`, `outside_content`, `first_input`, `model_usage`,
`agent_action`, `agent_result`, `action_held`, `action_reviewed`,
`action_approved`, `action_refused`, `action_applied`, `action_failed`,
`commits_created`, `session_ended` and `report_notes`.

## Attesting to a session

```
rehearse attest [session]
```

writes `rehearsal-attestation.json` (or the file named with `-o`). It
first verifies the session's record and refuses if that fails. The file
looks like this:

```json
{
  "statement": {
    "type": "rehearsal-attestation/v1",
    "session": "20260110-090000-k3f9aq",
    "tool_version": "0.3.0",
    "started_at": "2026-01-10T09:00:00.123456789Z",
    "ended_at": "2026-01-10T09:41:07.5Z",
    "commits_known": true,
    "commits": [
      "8c1f0e6a2b7d4c9e5f3a1b0d6e8c7f2a4b9d1e3c"
    ],
    "commits_omitted": 0,
    "policy_hash": "",
    "actions": {
      "held": 3,
      "approved": 2,
      "refused": 1
    },
    "chain": {
      "entries": 214,
      "final_hash": "5d0c...e91a"
    }
  },
  "public_key": "base64 of the 32-byte Ed25519 public key",
  "signature": "base64 of the signature"
}
```

- `commits` are the commits reachable from a local branch (or `HEAD`) when
  the agent exited that no branch, tag or remote-tracking branch could reach
  when it started, in the repository of the folder the session ran in. At
  most 500 are listed; `commits_omitted` counts the rest. When the folder
  was not a git repository, `commits_known` is false and the empty list
  means "not known", not "none".
- `policy_hash` is empty: there is no policy file yet.
- `actions` counts what was held in the session and what you decided.
- `chain` ties the statement to the session's log.

The signature is over the text `rehearsal attestation v1`, a line feed,
and the `statement` object with the white space between its tokens
removed. So the file can be re-indented without breaking it, and any change
to what the statement says does break it.

What an attestation shows: the holder of the key ran this version of
Rehearsal over a session that produced these commits, held this many
actions, and ended with this log. It doesn't show that the commits are
good, or that nothing was done outside the session.

## Checking an attestation

```
rehearse verify-attestation rehearsal-attestation.json --key signing-key.pub
```

needs only the file and the public key: no session, no log, no network.
`--key` takes the public key file or the key as base64 text, and it is
required. The key named inside the file is not trusted on its own, since
anyone can sign a statement with a key of their own.

It exits 0 and prints the statement when the signature is good, and exits 1
when the file was altered or was signed with another key.

## Requiring one on a pull request

A team can keep each member's public key in the repository and have its
checks fail a pull request that carries no valid attestation for its
commits. This is an example of such a step, written for GitHub Actions. It
is not part of this repository and nothing here runs it.

```yaml
# .github/workflows/attestation.yml (example only)
name: attestation
on: pull_request
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Build rehearse
        run: |
          git clone --depth 1 https://example.com/your/rehearsal.git /tmp/rehearsal
          (cd /tmp/rehearsal && sh build.sh)
      - name: Require a valid attestation for the commits
        run: |
          base="${{ github.event.pull_request.base.sha }}"
          head="${{ github.event.pull_request.head.sha }}"
          # The last commit adds the attestation and nothing else, so it
          # cannot be listed in it. Every commit before it must be.
          if [ "$(git diff --name-only "$head^" "$head")" != "rehearsal-attestation.json" ]; then
            echo "The last commit must add rehearsal-attestation.json and nothing else."
            exit 1
          fi
          for key in .rehearsal/keys/*.pub; do
            if out=$(/tmp/rehearsal/rehearse verify-attestation rehearsal-attestation.json --key "$key"); then
              echo "$out"
              for commit in $(git rev-list "$base..$head^"); do
                if ! echo "$out" | grep -q "^  $commit$"; then
                  echo "The attestation is valid but does not list $commit."
                  exit 1
                fi
              done
              exit 0
            fi
          done
          echo "No valid attestation from a known key."
          exit 1
```

The step passes only when the file verifies against one of the keys in
`.rehearsal/keys/` and lists every commit of the pull request except the
last, which is the one that adds the file. It has not been run anywhere: it
is here to show the shape of such a check. A pull request built over
several sessions would need one attestation per session, which this step
doesn't handle.
