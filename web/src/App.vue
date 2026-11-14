<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { applyApproved, discardAll, getHeld, getLedger, subscribe } from './api'
import type { Held, Ledger, LogEvent, Subscription } from './api'
import { statusSentence } from './present'
import { palettes, systemTheme, watchSystemTheme } from './theme'
import type { ThemeName } from './theme'
import StatusHeading from './components/StatusHeading.vue'
import UnusualList from './components/UnusualList.vue'
import DecisionPanel from './components/DecisionPanel.vue'
import SessionReport from './components/SessionReport.vue'
import ServerGroup from './components/ServerGroup.vue'
import WatchList from './components/WatchList.vue'

// the most events kept for the timeline
const maxEvents = 2000

const ledger = ref<Ledger | null>(null)
const held = ref<Held | null>(null)
const events = ref<LogEvent[]>([])
const connected = ref(false)
const ledgerProblem = ref('')
const updateProblem = ref('')
const decisionProblem = ref('')
const sending = ref(false)
const serversOpen = ref(false)

const query = new URLSearchParams(window.location.search)
const asked = query.get('theme')
// follows the system until the reader picks one
const followSystem = ref(asked !== 'light' && asked !== 'dark')
const theme = ref<ThemeName>(asked === 'light' || asked === 'dark' ? asked : systemTheme())

let subscription: Subscription | null = null
let stopWatchingTheme: (() => void) | null = null
let refreshTimer: number | undefined

const phase = computed(() => ledger.value?.phase ?? 'running')
const agent = computed(() => ledger.value?.command[0] ?? 'the agent')
const providers = computed(() => (ledger.value?.servers ?? []).filter((s) => s.category === 'provider'))
const others = computed(() => (ledger.value?.servers ?? []).filter((s) => s.category === 'other'))
const deciding = computed(() => phase.value === 'reviewing' || phase.value === 'deciding' || phase.value === 'applying')

const meta = computed(() => {
  const doing = {
    running: 'watching',
    reviewing: 'the agent has exited',
    deciding: 'the agent has exited',
    applying: 'the agent has exited',
    finished: 'session over',
  }[phase.value]
  const link = connected.value || phase.value === 'finished' ? '' : ', not connected'
  if (ledger.value?.replay === true) {
    return `Rehearsal, replaying a recorded session, ${phase.value === 'finished' ? 'replay over' : 'nothing here is real'}${link}`
  }
  return `Rehearsal, wrapping ${agent.value}, ${doing}${link}`
})

const status = computed(() => {
  if (ledger.value === null || held.value === null) return ''
  return statusSentence(ledger.value, held.value)
})

const problems = computed(() =>
  [ledgerProblem.value, updateProblem.value, decisionProblem.value].filter((p) => p !== ''),
)

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
  } catch (e: unknown) {
    ledgerProblem.value = `Could not read the session from Rehearsal: ${describe(e)}. Check that rehearse is still running in your terminal. If it has exited, the final record is in rehearsal-ledger.json.`
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
  if (events.value.length < maxEvents) events.value.push(event)
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
    decisionProblem.value = `Your decision did not reach Rehearsal, so nothing was done: ${describe(e)}. Try the button again, or press Ctrl+C in the terminal to discard everything.`
  }
  sending.value = false
  await refresh()
}

function switchTheme(): void {
  followSystem.value = false
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
}

onMounted(() => {
  void refresh()
  subscription = subscribe(
    onEvent,
    (isConnected) => {
      connected.value = isConnected
      // The report's last notes can arrive after the session is over, so
      // the connection stays open until rehearse stops serving. Closing it
      // then keeps the browser from reconnecting to nothing.
      if (!isConnected && ledger.value?.phase === 'finished') subscription?.close()
    },
    (error) => {
      updateProblem.value = `Rehearsal sent an update this page could not read: ${error.message}. Reload the page. If it keeps happening, the page and rehearse are different versions: rebuild with sh build.sh.`
    },
  )
  stopWatchingTheme = watchSystemTheme((next) => {
    if (followSystem.value) theme.value = next
  })
})

onUnmounted(() => {
  subscription?.close()
  stopWatchingTheme?.()
  window.clearTimeout(refreshTimer)
})
</script>

<template>
  <div class="page" :style="{ ...palettes[theme], colorScheme: theme }">
    <div class="column">
      <div v-if="ledger === null || held === null" class="loading">
        <div class="meta">Rehearsal</div>
        <h1 v-if="problems.length === 0" class="waiting">Connecting to Rehearsal.</h1>
      </div>
      <StatusHeading v-else :meta="meta" :status="status" />

      <div v-if="problems.length > 0" class="problems" role="alert">
        <h2 class="problem-heading">Something went wrong</h2>
        <div v-for="problem in problems" :key="problem" class="problem">{{ problem }}</div>
        <div v-if="ledger !== null" class="problem">What you see below is the last state that was read.</div>
      </div>

      <div v-if="ledger !== null && held !== null" class="body">
        <UnusualList v-if="phase === 'running'" :ledger="ledger" :held="held" :agent="agent" />

        <DecisionPanel
          v-if="deciding && held.actions.length > 0"
          :held="held"
          :agent="agent"
          :busy="sending"
          :policy="ledger.policy"
          @apply="(approve, rules) => void send(() => applyApproved(approve, rules))"
          @discard="void send(discardAll)"
        />

        <SessionReport
          v-if="phase === 'finished'"
          :ledger="ledger"
          :held="held"
          :events="events"
          :agent="agent"
          :servers-open="serversOpen"
          @toggle-servers="serversOpen = !serversOpen"
        />

        <div v-if="phase !== 'finished'" class="quiet">
          <span class="sentence">
            {{ phase === 'running' ? 'Everything else is normal.' : `${ledger.totals.requests} requests went out while the agent ran.` }}
          </span>
          <button class="link" type="button" :aria-expanded="serversOpen" @click="serversOpen = !serversOpen">
            {{ serversOpen ? 'Hide servers' : 'Show all servers' }}
          </button>
        </div>

        <div v-if="serversOpen" class="servers">
          <ServerGroup title="Model provider" :servers="providers" />
          <ServerGroup title="Everything else" :servers="others" />
          <div v-if="ledger.servers.length === 0" class="none">No server has been contacted.</div>
          <WatchList :identifiers="ledger.watching" />
        </div>
      </div>

      <div class="bottom">
        <button class="link small" type="button" @click="switchTheme">
          {{ theme === 'dark' ? 'Use the light theme' : 'Use the dark theme' }}
        </button>
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
  padding: 48px 24px;
  background-color: var(--bg);
  color: var(--text);
  font-family: system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
  font-size: 18px;
  font-weight: 400;
  line-height: 1.4;
}

.column {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 760px;
}

.loading {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
}

.meta {
  margin-bottom: 8px;
  font-size: 14px;
  color: var(--secondary);
}

.waiting {
  margin: 0;
  font-size: 32px;
  font-weight: 600;
  line-height: 1.2;
}

.problems {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
  padding-top: 12px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--high);
}

.problem-heading {
  margin: 0;
  margin-bottom: 8px;
  font-size: 18px;
  font-weight: 600;
  color: var(--high);
}

.problem {
  margin-bottom: 8px;
  font-size: 16px;
  overflow-wrap: anywhere;
}

.body {
  display: flex;
  flex-direction: column;
}

.quiet {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  min-height: 44px;
  margin-bottom: 16px;
  font-size: 16px;
  color: var(--secondary);
}

.sentence {
  margin-right: 8px;
}

.link {
  min-height: 24px;
  padding: 0;
  border-width: 0;
  background-color: transparent;
  color: var(--text);
  font-family: inherit;
  font-size: 16px;
  text-decoration: underline;
  cursor: pointer;
}

.link:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.small {
  font-size: 14px;
  color: var(--secondary);
}

.servers {
  display: flex;
  flex-direction: column;
  margin-bottom: 16px;
}

.none {
  margin-bottom: 24px;
  font-size: 16px;
  color: var(--secondary);
}

.bottom {
  display: flex;
  flex-direction: row;
  margin-top: 32px;
}
</style>
