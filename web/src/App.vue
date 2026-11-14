<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { applyApproved, discardAll, getHeld, getLedger, subscribe } from './api'
import type { Held, Ledger, LogEvent, Subscription } from './api'
import { palettes, preferredTheme } from './theme'
import type { ThemeName } from './theme'
import SummaryBar from './components/SummaryBar.vue'
import ServerGroup from './components/ServerGroup.vue'
import WatchList from './components/WatchList.vue'
import DecisionPanel from './components/DecisionPanel.vue'

const ledger = ref<Ledger | null>(null)
const held = ref<Held | null>(null)
const connected = ref(false)
const ledgerProblem = ref('')
const updateProblem = ref('')
const decisionProblem = ref('')
const sending = ref(false)
const problems = computed(() =>
  [ledgerProblem.value, updateProblem.value, decisionProblem.value].filter((p) => p !== ''),
)
const theme = ref<ThemeName>(preferredTheme())

let subscription: Subscription | null = null
let refreshTimer: number | undefined

const phase = computed(() => ledger.value?.phase ?? 'running')
const agentName = computed(() => ledger.value?.command[0] ?? 'the agent')
const providers = computed(() => (ledger.value?.servers ?? []).filter((s) => s.category === 'provider'))
const others = computed(() => (ledger.value?.servers ?? []).filter((s) => s.category === 'other'))
const showDecision = computed(() => held.value !== null && held.value.actions.length > 0 && phase.value !== 'running')

const statusText = computed(() => {
  if (phase.value === 'finished') return 'Session ended. This is the final ledger.'
  if (!connected.value) return 'Not connected'
  if (phase.value === 'reviewing') return 'The agent has finished. Reading what it tried to do.'
  if (phase.value === 'deciding') return 'Waiting for your decision'
  if (phase.value === 'applying') return 'Performing what you approved'
  return 'Watching live'
})
const statusClass = computed(() => (connected.value && phase.value !== 'finished' ? 'status status-live' : 'status'))

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

// Never rejects: a failure is shown on the page.
async function refresh(): Promise<void> {
  try {
    const [nextLedger, nextHeld] = await Promise.all([getLedger(), getHeld()])
    ledger.value = nextLedger
    held.value = nextHeld
    ledgerProblem.value = ''
    if (nextLedger.phase === 'finished') {
      // rehearse stops serving shortly after this, so stop the browser
      // from reconnecting
      subscription?.close()
      connected.value = false
    }
  } catch (e: unknown) {
    ledgerProblem.value = `Could not read the ledger: ${describe(e)}`
  }
}

// events can arrive in bursts; redraw a few times a second at most
function refreshSoon(): void {
  if (refreshTimer !== undefined) return
  refreshTimer = window.setTimeout(() => {
    refreshTimer = undefined
    void refresh()
  }, 150)
}

function onEvent(event: LogEvent): void {
  if (event.type !== 'session_ended') {
    refreshSoon()
    return
  }
  // with nothing held, rehearse exits right after this event
  window.clearTimeout(refreshTimer)
  refreshTimer = undefined
  void refresh()
}

// Never rejects: a decision that could not be delivered is shown on the
// page and the buttons work again.
async function send(decision: () => Promise<void>): Promise<void> {
  sending.value = true
  decisionProblem.value = ''
  try {
    await decision()
  } catch (e: unknown) {
    decisionProblem.value = `Your decision did not reach Rehearsal, so nothing was done: ${describe(e)}`
  }
  sending.value = false
  await refresh()
}

function toggleTheme(): void {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
}

onMounted(() => {
  void refresh()
  subscription = subscribe(
    onEvent,
    (isConnected) => {
      connected.value = isConnected
    },
    (error) => {
      updateProblem.value = `Rehearsal sent an update this page could not read: ${error.message}`
    },
  )
})

onUnmounted(() => {
  subscription?.close()
  window.clearTimeout(refreshTimer)
})
</script>

<template>
  <div class="page" :style="palettes[theme]">
    <div class="content">
      <div class="top">
        <div class="heading">
          <h1 class="title">Rehearsal</h1>
          <div class="subtitle">
            Where <span class="agent">{{ agentName }}</span> sent your data
          </div>
        </div>
        <div class="controls">
          <div :class="statusClass">{{ statusText }}</div>
          <button class="theme-button" type="button" @click="toggleTheme">
            {{ theme === 'dark' ? 'Light theme' : 'Dark theme' }}
          </button>
        </div>
      </div>

      <div v-if="problems.length > 0" class="problem" role="alert">
        <div v-for="problem in problems" :key="problem">{{ problem }}</div>
        <div v-if="ledger !== null">What you see below is the last ledger that was read.</div>
      </div>

      <div v-if="ledger === null && problems.length === 0" class="loading">Waiting for the ledger…</div>

      <div v-if="ledger !== null" class="body">
        <DecisionPanel
          v-if="showDecision && held !== null"
          :held="held"
          :busy="sending"
          @apply="(approve) => void send(() => applyApproved(approve))"
          @discard="void send(discardAll)"
        />
        <SummaryBar :totals="ledger.totals" :holding="ledger.holding" />
        <ServerGroup
          title="Model provider"
          hint="The AI service the agent is meant to talk to."
          empty-text="Nothing sent to the model provider yet."
          :servers="providers"
        />
        <ServerGroup
          title="Everything else"
          hint="Every other server the agent or its tools contacted."
          empty-text="No other servers contacted."
          :servers="others"
        />
        <WatchList :identifiers="ledger.watching" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-height: 100vh;
  box-sizing: border-box;
  padding: 32px;
  background-color: var(--bg);
  color: var(--text);
  font-family: system-ui, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
  font-size: 22px;
  line-height: 1.35;
}

.content {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 1200px;
}

.top {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: flex-end;
  margin-bottom: 28px;
}

.heading {
  display: flex;
  flex-direction: column;
  margin-right: 24px;
  margin-bottom: 8px;
}

.title {
  margin: 0;
  font-size: 44px;
  font-weight: 700;
}

.subtitle {
  font-size: 26px;
  color: var(--muted);
}

.agent {
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  color: var(--text);
}

.controls {
  display: flex;
  flex-direction: row;
  align-items: center;
  margin-bottom: 8px;
}

.status {
  font-size: 22px;
  font-weight: 600;
  color: var(--muted);
  margin-right: 20px;
}

.status-live {
  color: var(--live);
}

.theme-button {
  font-family: inherit;
  font-size: 20px;
  padding: 8px 16px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 8px;
  background-color: var(--surface);
  color: var(--text);
  cursor: pointer;
}

.problem {
  margin-bottom: 20px;
  padding: 16px 20px;
  border-width: 3px;
  border-style: solid;
  border-color: var(--flag-border);
  border-radius: 12px;
  background-color: var(--flag-bg);
  color: var(--flag-text);
  font-size: 22px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.loading {
  font-size: 26px;
  color: var(--muted);
}

.body {
  display: flex;
  flex-direction: column;
}
</style>
