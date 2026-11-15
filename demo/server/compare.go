package main

import (
	"fmt"
	"net/http"
	"slices"
)

// world is one set of the stand-in services that an agent can act on: a
// cloud host and an inbox. Two of them, started together, are the same
// demo twice: once with nothing in front of the agent and once under
// Rehearsal.
type world struct {
	cloud *cloud
	inbox *inbox
}

// worldState is what the compare page shows of one world.
type worldState struct {
	// what the attacker's inbox has received, oldest first
	Inbox []string `json:"inbox"`
	// whether the production database still exists
	Production bool `json:"production"`
}

type compareState struct {
	Without worldState `json:"without"`
	With    worldState `json:"with"`
}

func (c *cloud) hasProduction() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.databases, "production")
}

func (w *world) state() worldState {
	received := w.inbox.received()
	if received == nil {
		received = []string{} // a list for the page, never null
	}
	return worldState{Inbox: received, Production: w.cloud.hasProduction()}
}

// comparing adds two addresses to a cloud host: /compare, a page that
// sets the two worlds side by side, and /compare/state, what the page
// reads to keep itself current. Everything else goes to the cloud host.
func comparing(next http.Handler, without, with *world) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /compare", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprint(w, comparePage) // fails only when the browser has gone away
	})
	mux.HandleFunc("GET /compare/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, compareState{Without: without.state(), With: with.state()})
	})
	mux.Handle("/", next)
	return mux
}

// comparePage is two columns of plain text. Its colours are three of the
// Rehearsal page's own, with the same values for each theme. What it shows is set as text,
// never as markup, since an inbox holds whatever was sent to it.
const comparePage = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Rehearsal demo: side by side</title>
<style>
:root {
  --panel: #fcfcfd;
  --text: #171a1d;
  --secondary: #44474b;
}
@media (prefers-color-scheme: dark) {
  :root {
    --panel: #161719;
    --text: #e8e9eb;
    --secondary: #b2b4b8;
  }
}
body {
  margin: 0;
  padding: 48px 24px;
  background-color: var(--panel);
  color: var(--text);
  font-family: system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
  font-size: 18px;
  font-weight: 400;
  line-height: 1.4;
}
.meta {
  margin-bottom: 32px;
  font-size: 14px;
  color: var(--secondary);
}
.columns {
  display: flex;
  flex-direction: row;
}
.column {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  flex-basis: 0;
  min-width: 0;
  margin-right: 48px;
}
.heading {
  margin: 0;
  margin-bottom: 32px;
  font-size: 24px;
  font-weight: 600;
  color: var(--secondary);
}
.status {
  margin: 0;
  font-size: 32px;
  font-weight: 600;
  line-height: 1.2;
}
.item {
  margin-top: 16px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 24px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.gap {
  margin-top: 32px;
}
</style>
<div class="meta" id="meta">Rehearsal demo. The same agent script, run twice at the same moment.</div>
<div class="columns">
  <div class="column" id="without">
    <h2 class="heading">Without Rehearsal</h2>
    <div class="status" data-database>Production database: waiting</div>
    <div class="status gap" data-inbox>Attacker's inbox: waiting</div>
    <div data-items></div>
  </div>
  <div class="column" id="with">
    <h2 class="heading">With Rehearsal</h2>
    <div class="status" data-database>Production database: waiting</div>
    <div class="status gap" data-inbox>Attacker's inbox: waiting</div>
    <div data-items></div>
  </div>
</div>
<script>
const meta = document.getElementById('meta')
const said = meta.textContent
function show(id, state) {
  const column = document.getElementById(id)
  const n = state.inbox.length
  column.querySelector('[data-inbox]').textContent = "Attacker's inbox: " + (n === 0 ? 'nothing received' : n + ' received')
  column.querySelector('[data-items]').replaceChildren(...state.inbox.map((text) => {
    const item = document.createElement('div')
    item.className = 'item'
    item.textContent = text
    return item
  }))
  column.querySelector('[data-database]').textContent = 'Production database: ' + (state.production ? 'still there' : 'deleted')
}
async function read() {
  try {
    const res = await fetch('/compare/state', { cache: 'no-store' })
    const state = await res.json()
    show('without', state.without)
    show('with', state.with)
    meta.textContent = said
  } catch (e) {
    meta.textContent = said + ' Not connected: the demo servers have stopped.'
  }
}
read()
setInterval(read, 500)
</script>
`
