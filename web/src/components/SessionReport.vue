<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Held, Ledger, LogEvent } from '../api'
import type { Row } from '../present'
import { dataSent, markdown, outcomeOf, rowsFor, where } from '../present'
import BreakableText from './BreakableText.vue'

const props = defineProps<{
  ledger: Ledger
  held: Held
  events: LogEvent[]
  agent: string
  serversOpen: boolean
}>()

const emit = defineEmits<{ toggleServers: [] }>()

const timelineOpen = ref(false)
const copied = ref<'no' | 'yes' | 'failed'>('no')

// refused rows first, then in order of destination
const rows = computed<Row[]>(() => {
  const all = rowsFor(props.held.actions, props.agent)
  const refused = (r: Row): boolean => r.first.decision === 'refused'
  return [...all.filter(refused), ...all.filter((r) => !refused(r))]
})

const traffic = computed(() => {
  const t = props.ledger.totals
  const sent = `${t.requests === 1 ? '1 request' : `${t.requests} requests`} to ${t.servers === 1 ? '1 server' : `${t.servers} servers`}`
  if (t.flagged_servers === 0) return `${sent}. None outside the model provider received your details.`
  const flagged = t.flagged_servers === 1 ? '1 server' : `${t.flagged_servers} servers`
  return `${sent}. ${flagged} outside the model provider received your details.`
})

const timeline = computed(() => props.events.filter((e) => e.what !== ''))

const recordLine = computed(() => {
  const r = props.ledger.record
  if (r.verified && r.unsigned) {
    return 'Record verified, but unsigned: this session had no signing key, so entries removed from the end could not be noticed.'
  }
  if (r.verified) return 'Record verified.'
  return r.badEntry > 0 ? `Record does not verify, starting at entry ${r.badEntry}.` : 'Record does not verify.'
})

const commandsOpen = ref(false)
const r = computed(() => props.ledger.report)

// A line whose figure could not be read is left out: each of these is
// empty when there is nothing to say.
const beforeInput = computed(() => {
  if (!r.value.inputWatched) return ''
  const n = r.value.requestsBeforeInput
  const sent = n === 1 ? '1 request was' : `${n} requests were`
  return r.value.inputSeen ? `${sent} sent before you pressed a key.` : `${sent} sent, and no input ever reached the agent.`
})

const models = computed(() => {
  const list = r.value.models
  const first = list[0]
  if (first === undefined) return ''
  const rest = list.slice(1).map((m) => `${m.name} (${m.requests})`)
  const main = `Model: ${first.name}, ${first.requests === 1 ? '1 request' : `${first.requests} requests`}.`
  return rest.length === 0 ? main : `${main} Also used: ${rest.join(', ')}.`
})

const tokens = computed(() => {
  const t = r.value.tokens
  const counts: [number | null, string][] = [
    [t.input, 'in'],
    [t.output, 'out'],
    [t.cacheRead, 'read from cache'],
    [t.cacheWrite, 'written to cache'],
  ]
  const known = counts.flatMap(([n, what]) => (n === null ? [] : [`${n.toLocaleString()} ${what}`]))
  if (known.length === 0) return ''
  const share = t.cacheShare === null ? '' : ` Served from cache: ${t.cacheShare}%.`
  return `Tokens: ${known.join(', ')}.${share}`
})

const outside = computed(() =>
  `Content read from other servers: ${props.ledger.outside.map((o) => (o.hidden > 0 ? `${o.host} (${o.hidden} hidden)` : o.host)).join(', ')}.`,
)

const commandsLine = computed(() => {
  if (!r.value.commandsKnown) return ''
  const n = r.value.commands.length
  return n === 1 ? 'The agent ran 1 shell command.' : `The agent ran ${n} shell commands.`
})

// Only a claim the commands bear on is worth showing.
const claims = computed(() => r.value.claims.filter((c) => c.verdict === 'supported' || c.verdict === 'contradicted'))

// An install that was stopped is a decision too, made by the rules, and
// sits in the table with the others.
const stopped = computed(() => props.ledger.packages.filter((p) => p.blocked))
const ran = computed(() => props.ledger.packages.filter((p) => !p.blocked))

function registryOf(manager: string): string {
  return ['pip', 'pip3', 'uv'].includes(manager) ? 'the Python package index' : 'the npm registry'
}

function clock(time: string): string {
  const d = new Date(time)
  return Number.isNaN(d.getTime()) ? time : d.toLocaleTimeString()
}

const report = computed(() => markdown(props.ledger, props.held))

async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(report.value)
    copied.value = 'yes'
  } catch {
    // the browser refused the clipboard: the text is shown below to copy by hand
    copied.value = 'failed'
  }
}
</script>

<template>
  <div class="report">
    <div v-if="rows.length > 0 || stopped.length > 0" class="table">
      <div class="header">
        <div class="action">Action</div>
        <div class="destination">Destination</div>
        <div class="data">Data sent</div>
        <div class="decision">Decision</div>
      </div>
      <div v-for="row in rows" :key="row.key" class="line">
        <div class="action">
          <div class="title">{{ row.title }}</div>
          <div class="exact"><BreakableText :text="row.first.exact" /></div>
          <div v-if="row.first.outcome === 'failed'" class="exact">{{ row.first.detail }}</div>
        </div>
        <div class="destination"><BreakableText :text="where(row.first)" dots /></div>
        <div class="data">{{ dataSent(row.first) }}</div>
        <div :class="`decision decision-${outcomeOf(row.first)?.tone ?? 'plain'}`">
          {{ outcomeOf(row.first)?.word ?? 'Undecided' }}
        </div>
      </div>
      <div v-for="(p, i) in stopped" :key="`package-${i}`" class="line">
        <div class="action">
          <div class="title">Install {{ p.name }} with {{ p.manager }}</div>
          <div class="exact">{{ p.facts }}</div>
        </div>
        <div class="destination">{{ registryOf(p.manager) }}</div>
        <div class="data">Nothing: it did not run</div>
        <div class="decision decision-plain">Stopped</div>
      </div>
    </div>

    <div class="observed">
      <h2 class="heading">What was observed</h2>
      <div v-if="beforeInput !== ''" class="fact">{{ beforeInput }}</div>
      <div v-if="models !== ''" class="fact">{{ models }}</div>
      <div v-if="tokens !== ''" class="fact">{{ tokens }}</div>
      <div v-if="ledger.outside.length > 0" class="fact">{{ outside }}</div>
      <div v-if="commandsLine !== ''" class="fact">
        {{ commandsLine }}
        <button v-if="r.commands.length > 0" class="link" type="button" :aria-expanded="commandsOpen" @click="commandsOpen = !commandsOpen">
          {{ commandsOpen ? 'Hide commands' : 'Show commands' }}
        </button>
      </div>
      <div v-if="commandsOpen" class="block">
        <div v-for="(c, i) in r.commands" :key="i">{{ c.text }} ({{ c.failed === null ? 'result not reported' : c.failed ? 'reported an error' : 'ok' }})</div>
      </div>
      <div v-for="flag in r.flags" :key="flag" class="fact fact-flag">Worth a second look: {{ flag }}</div>
      <div v-for="(p, i) in ran" :key="i" class="fact">An {{ p.manager }} install went ahead: {{ p.facts }}.</div>
      <div v-if="ledger.policy.allowed > 0" class="fact">
        {{ ledger.policy.allowed === 1 ? '1 action was' : `${ledger.policy.allowed} actions were` }} let through by a rule in your policy and not held.
      </div>
      <div v-for="ig in ledger.policy.ignored" :key="ig.source + ig.rule" class="fact">
        A rule in the {{ ig.source }} policy was ignored: {{ ig.rule }}. {{ ig.why }}.
      </div>
      <div v-for="offer in ledger.policy.offers.filter((o) => o.added)" :key="offer.id" class="fact">
        Added to .rehearsal/policy.yaml because you ticked it: {{ offer.line }}
      </div>
      <div v-if="claims.length > 0" class="claims">
        <div class="fact">Claimed versus observed (experimental, written by an AI model on this computer):</div>
        <div v-for="(c, i) in claims" :key="i" class="fact">
          "{{ c.claim }}": {{ c.verdict }}<span v-if="c.command !== ''" class="aside"> ({{ c.command }})</span>
        </div>
      </div>
    </div>

    <div class="footer">
      <div class="totals">{{ traffic }}</div>
      <div :class="ledger.record.verified && !ledger.record.unsigned ? 'totals' : 'totals broken'">{{ recordLine }}</div>
      <div class="links">
        <button class="link" type="button" :aria-expanded="timelineOpen" @click="timelineOpen = !timelineOpen">
          {{ timelineOpen ? 'Hide timeline' : 'Timeline' }}
        </button>
        <button class="link" type="button" :aria-expanded="serversOpen" @click="emit('toggleServers')">
          {{ serversOpen ? 'Hide servers' : 'All servers' }}
        </button>
        <button class="link" type="button" @click="void copy()">Copy as Markdown</button>
        <span v-if="copied === 'yes'" class="copied">Copied.</span>
      </div>
    </div>

    <div v-if="copied === 'failed'" class="manual">
      <div class="totals">The browser would not allow copying. Select the text below and copy it yourself.</div>
      <div class="block">{{ report }}</div>
    </div>

    <div v-if="timelineOpen" class="timeline">
      <div v-if="timeline.length === 0" class="totals">Nothing was sent or held in this session.</div>
      <div v-for="event in timeline" :key="event.seq" class="event">
        <div class="time">{{ clock(event.time) }}</div>
        <div class="what">{{ event.what }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.report {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
}

.table {
  display: flex;
  flex-direction: column;
  margin-bottom: 24px;
}

.header {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  padding-bottom: 8px;
  font-size: 14px;
  color: var(--secondary);
}

.line {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  min-height: 44px;
  padding-top: 12px;
  padding-bottom: 12px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
  font-size: 16px;
}

.action {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  flex-basis: 240px;
  min-width: 0;
  padding-right: 16px;
}

.title {
  font-size: 18px;
  overflow-wrap: anywhere;
}

.exact {
  margin-top: 4px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  color: var(--secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.destination {
  flex-grow: 1;
  flex-basis: 120px;
  max-width: 224px;
  min-width: 0;
  box-sizing: border-box;
  padding-right: 16px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.data {
  flex-grow: 1;
  flex-basis: 120px;
  max-width: 168px;
  min-width: 0;
  box-sizing: border-box;
  padding-right: 16px;
  overflow-wrap: anywhere;
}

.decision {
  flex-shrink: 0;
  width: 72px;
  font-weight: 600;
  text-align: right;
}

.decision-allowed {
  color: var(--allowed);
}

.decision-plain {
  color: var(--secondary);
}

.observed {
  display: flex;
  flex-direction: column;
  margin-bottom: 24px;
}

.heading {
  margin: 0;
  margin-bottom: 8px;
  font-size: 24px;
  font-weight: 600;
}

.fact {
  margin-bottom: 4px;
  font-size: 16px;
}

.fact-flag {
  font-weight: 600;
}

.aside {
  color: var(--secondary);
}

.claims {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
}

.footer {
  display: flex;
  flex-direction: column;
  padding-top: 12px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
}

.totals {
  font-size: 16px;
  color: var(--secondary);
}

.broken {
  color: var(--text);
  font-weight: 600;
}

.links {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  margin-top: 8px;
}

.link {
  min-height: 44px;
  margin-right: 24px;
  padding: 0;
  border-width: 0;
  background-color: transparent;
  color: var(--accent);
  font-family: inherit;
  font-size: 16px;
  text-decoration: underline;
  cursor: pointer;
}

.link:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.copied {
  font-size: 16px;
  color: var(--secondary);
}

.manual {
  display: flex;
  flex-direction: column;
  margin-top: 16px;
}

.block {
  margin-top: 8px;
  padding: 8px 12px;
  border-radius: 4px;
  background-color: var(--raised);
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.timeline {
  display: flex;
  flex-direction: column;
  margin-top: 16px;
}

.event {
  display: flex;
  flex-direction: row;
  padding-top: 4px;
  padding-bottom: 4px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
}

.time {
  flex-shrink: 0;
  width: 112px;
  color: var(--secondary);
}

.what {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
