<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, shallowRef } from 'vue'
import { settings, useMediaQuery, useShortcut } from './page'
import type { Source } from './api'
import { source } from './api'
import type { Changes, Entry, Info, Ledger, RecordCheck, SessionInfo } from './types'
import type { Group, Item } from './model'
import { consequence, decisionLabel, facts, groupNames, items, outcome, plural, routineRow, unusual, workload } from './model'
import SideBar from './components/SideBar.vue'
import ItemRow from './components/ItemRow.vue'
import DetailPane from './components/DetailPane.vue'
import InvestigateView from './components/InvestigateView.vue'
import RawEntry from './components/RawEntry.vue'
import SegmentedControl from './components/SegmentedControl.vue'

type Tab = 'review' | 'servers' | 'record'
type View = 'session' | 'investigate' | 'policy'

const src = shallowRef<Source | null>(null)
const info = ref<Info>({ version: '', reviewer: '' })
const sessions = ref<SessionInfo[]>([])
const ledger = ref<Ledger | null>(null)
const changes = ref<Changes>({ captured: false, in_repo: false, parts: [] })
// the session being shown; '' until the list of sessions has arrived
const shown = ref('')
const view = ref<View>('session')
const tab = ref<Tab>('review')
const sel = ref('')
const ticked = reactive(new Set<string>())
// actions whose tick has been given its starting value
const met = new Set<string>()
const open = reactive<Record<'skim' | 'mechanical', boolean>>({ skim: false, mechanical: false })
const performing = ref(false)
// the routine actions are listed under their one row
const unfolded = ref(false)
const problem = ref('')
const texts = reactive<Record<string, string>>({})
const entries = ref<Entry[]>([])
const check = ref<RecordCheck | null>(null)
const openEntry = ref(0)
const narrow = useMediaQuery('(max-width: 900px)')
// the finding to open first, for looking at one state of the window on its own
const startFinding = ref('')
const startAll = ref(false)
// what the Investigate view found about its records, for the status line
const investStatus = ref('')
const investFailing = ref(false)

const current = computed(() => sessions.value.find((s) => s.current)?.name ?? '')
// the live session is asked for without a name, which works on any server
const name = computed(() => (shown.value === '' || shown.value === current.value ? null : shown.value))
const live = computed(() => name.value === null)

const list = computed<Item[]>(() => (ledger.value === null ? [] : items(ledger.value, changes.value, performing.value, unfolded.value)))
// what the agent said, or was told, against what was observed: shown when
// the session is over, and there is one
const truth = computed(() => (view.value === 'session' && ledger.value?.phase === 'finished' ? (ledger.value.truth?.sentence ?? '') : ''))
const sentence = computed(() => (ledger.value === null ? 'Loading the session…' : workload(ledger.value, changes.value, list.value)))
const held = computed(() => ledger.value?.actions.filter((a) => a.kind !== 'file') ?? [])
const undecided = computed(() => held.value.filter((a) => outcome(a) === null))
const canDecide = computed(() => live.value && ledger.value?.phase === 'deciding' && !performing.value && undecided.value.length > 0)
const label = computed(() => decisionLabel(held.value, ticked, ledger.value?.home))
const groups = computed(() => {
  const order: Group[] = ['out', 'claim', 'read', 'skim', 'mechanical']
  return order
    .map((g) => ({ group: g, name: groupNames[g], items: list.value.filter((i) => i.group === g), folds: g === 'skim' || g === 'mechanical' }))
    .filter((g) => g.items.length > 0)
})
// what j and k move through: only what is on screen
const visible = computed(() => groups.value.flatMap((g) => (g.folds && !open[g.group as 'skim' | 'mechanical'] ? [] : g.items)))
const selected = computed(() => list.value.find((i) => i.id === sel.value) ?? null)
const title = computed(() => {
  if (view.value === 'investigate') return 'Investigate'
  if (view.value === 'policy') return 'Policy'
  const l = ledger.value
  if (l === null) return ''
  const folder = l.work_dir.split('/').pop() ?? ''
  return `${l.command?.[0] ?? 'agent'} in ${folder}${l.replay ? ', replaying a recorded session' : ''}`
})
const recordWord = computed(() => {
  const r = ledger.value?.record
  if (r === undefined) return ''
  if (!r.verified) return r.bad_entry > 0 ? `Record does not verify, from entry ${r.bad_entry}` : 'Record does not verify'
  return r.unsigned ? 'Record verified, unsigned' : 'Record verified'
})
const serverRows = computed(() =>
  (ledger.value?.servers ?? []).map((s) => ({
    host: s.host,
    second:
      plural(s.requests, 'request', 'requests') +
      (s.category === 'provider' ? ', the model provider' : '') +
      (s.received.length > 0 ? `, received ${s.received.map((r) => r.label).join(', ')}` : ''),
    flagged: s.flagged,
  })),
)

async function load(): Promise<void> {
  const from = src.value
  if (from === null) return
  try {
    const [l, c] = await Promise.all([from.ledger(name.value), from.changes(name.value)])
    ledger.value = l
    changes.value = c
    problem.value = ''
    for (const a of l.actions) {
      // a tick starts on for what is not high risk, once it has been read
      if (a.review !== null && !met.has(a.id)) {
        met.add(a.id)
        if (a.review.risk !== 'high') ticked.add(a.id)
      }
    }
    if (l.phase === 'finished') performing.value = false
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

async function refreshSessions(): Promise<void> {
  const from = src.value
  if (from === null) return
  try {
    sessions.value = await from.sessions()
    if (shown.value === '') shown.value = current.value !== '' ? current.value : (sessions.value[0]?.name ?? '')
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

async function show(session: string): Promise<void> {
  view.value = 'session'
  if (session === shown.value) return
  shown.value = session
  sel.value = ''
  ledger.value = null
  entries.value = []
  check.value = null
  await load()
  if (tab.value === 'record') await loadRecord()
}

async function loadRecord(): Promise<void> {
  const from = src.value
  const session = shown.value
  if (from === null || session === '') return
  try {
    ;[entries.value, check.value] = await Promise.all([from.entries(session), from.record(session)])
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

function pick(next: Tab): void {
  tab.value = next
  sel.value = ''
  if (next === 'record') void loadRecord()
}

async function select(id: string): Promise<void> {
  if (id === routineRow) {
    // the one row for routine actions opens into its actions, not into a detail
    unfolded.value = !unfolded.value
    return
  }
  sel.value = id
  const item = list.value.find((i) => i.id === id)
  const from = src.value
  if (item?.part === undefined || from === null || texts[id] !== undefined) return
  try {
    texts[id] = await from.text(name.value, id)
  } catch (e: unknown) {
    texts[id] = `The change could not be read: ${e instanceof Error ? e.message : String(e)}`
  }
}

function tick(id: string, on: boolean): void {
  const item = list.value.find((i) => i.id === id)
  for (const one of item?.bundle?.map((a) => a.id) ?? [id]) {
    if (on) ticked.add(one)
    else ticked.delete(one)
  }
  // allowing something high risk puts its detail beside the list, so that
  // it is in view when the button is pressed; nothing stands in the way
  if (on && item?.action?.review?.risk === 'high') void select(id)
}

function tickable(i: Item): boolean {
  const all = i.bundle ?? (i.action !== undefined ? [i.action] : [])
  return live.value && all.length > 0 && all.every((a) => a.review !== null && outcome(a) === null && a.decision === '')
}

function isTicked(i: Item): boolean {
  return i.bundle !== undefined ? i.bundle.every((a) => ticked.has(a.id)) : ticked.has(i.id)
}

async function decide(): Promise<void> {
  const from = src.value
  const l = ledger.value
  if (from === null || l === null || !canDecide.value) return
  const approve = undecided.value.filter((a) => ticked.has(a.id)).map((a) => a.id)
  // shown at once: the rows say "Performing…" until the real result is known
  performing.value = true
  for (const a of undecided.value) a.decision = approve.includes(a.id) ? 'approved' : 'refused'
  try {
    await from.apply(approve)
    await load()
  } catch (e: unknown) {
    // nothing was done: put the rows and their ticks back as they were
    performing.value = false
    for (const a of l.actions) if (a.outcome === '') a.decision = ''
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

function move(by: number): void {
  if (view.value !== 'session' || tab.value !== 'review' || visible.value.length === 0) return
  const at = visible.value.findIndex((i) => i.id === sel.value)
  const to = Math.min(Math.max(at + by, 0), visible.value.length - 1)
  const item = visible.value[to]
  if (item !== undefined) void select(item.id)
}

useShortcut('j', () => move(1), { repeat: true })
useShortcut('k', () => move(-1), { repeat: true })
useShortcut('Space', () => {
  const item = selected.value
  if (item?.action !== undefined && canDecide.value) tick(item.id, !ticked.has(item.id))
})
useShortcut('Enter', () => {
  if (sel.value === '') move(1)
})
useShortcut('Escape', () => {
  sel.value = ''
  openEntry.value = 0
})
useShortcut('1', () => pick('review'))
useShortcut('2', () => pick('servers'))
useShortcut('3', () => pick('record'))

let timer: ReturnType<typeof setInterval> | undefined

onMounted(async () => {
  const config = settings()
  if (config.theme === 'light' || config.theme === 'dark') document.documentElement.dataset['theme'] = config.theme
  src.value = source(config)
  if (src.value === null) {
    problem.value = 'No session to show. Start one with rehearse, which opens this window itself.'
    return
  }
  try {
    info.value = await src.value.info()
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
  await refreshSessions()
  await load()
  // for looking at one state of the window on its own
  if (config.view === 'investigate' || config.view === 'investigate-all') {
    startFinding.value = config.select
    startAll.value = config.view === 'investigate-all'
    view.value = 'investigate'
  } else if (config.view === 'policy') view.value = 'policy'
  if (config.tab === 'servers' || config.tab === 'record') pick(config.tab)
  if (view.value !== 'session') {
    // the selection named is a finding's, not an item's
  } else if (config.select === 'first') move(1)
  else if (config.select !== '') void select(config.select)
  timer = setInterval(() => {
    if (view.value === 'session' && live.value && (ledger.value?.phase !== 'finished' || performing.value)) void load()
  }, 1000)
})

onUnmounted(() => {
  if (timer !== undefined) clearInterval(timer)
})
</script>

<template>
  <div class="window">
    <div class="shell">
      <SideBar v-if="!narrow" :sessions="sessions" :shown="shown" :view="view" @open="show" @view="view = $event" />
      <div :class="narrow ? 'panel narrow' : 'panel'">
        <div v-if="narrow || view === 'session'" :class="narrow ? 'head stacked' : 'head'">
          <select v-if="narrow" class="chooser" :value="shown" title="Session" @change="show(String(($event.target as HTMLSelectElement).value))">
            <option v-for="s in sessions" :key="s.name" :value="s.name">{{ s.project }}, {{ s.command }}</option>
          </select>
          <SegmentedControl v-if="view === 'session'" class="tabs" :model-value="tab" @update:model-value="pick($event as Tab)">
            <option value="review">Review</option>
            <option value="servers">Servers</option>
            <option value="record">Record</option>
          </SegmentedControl>
        </div>

        <InvestigateView
          v-if="view === 'investigate' && src !== null"
          :source="src"
          :session="shown"
          :narrow="narrow"
          :select="startFinding"
          :reviewer="info.reviewer"
          :all="startAll"
          @status="(text: string, failing: boolean) => ((investStatus = text), (investFailing = failing))"
        />
        <div v-else class="work">
          <div v-if="!(narrow && selected !== null)" :class="selected !== null ? 'list open' : 'list'">
            <div class="scroll">
              <div class="col">
                <h1 :class="selected !== null ? 'status small' : 'status'">{{ view === 'session' ? sentence : title }}</h1>
                <span v-if="truth !== ''" :class="selected !== null ? 'status small' : 'status'">{{ truth }}</span>
                <span v-if="problem !== ''" class="problem">{{ problem }}</span>

                <template v-if="view !== 'session'">
                  <span class="empty">
                    {{
                      view === 'policy'
                        ? 'The rules in .rehearsal/policy.yaml and the organisation policy will be listed here. Run rehearse policy check to see them now.'
                        : 'Findings from recorded sessions will be listed here, most serious first. Run rehearse investigate to produce them.'
                    }}
                  </span>
                </template>

                <template v-else-if="ledger === null">
                  <div v-for="n in 3" :key="n" class="ghost"></div>
                </template>

                <template v-else-if="tab === 'review'">
                  <template v-if="ledger.phase === 'running'">
                    <span class="group">UNUSUAL SO FAR</span>
                    <span v-if="unusual(ledger).length === 0" class="empty">Anything held, any server that receives one of your details and any stopped install appears here as it happens.</span>
                    <div v-for="(u, i) in unusual(ledger)" :key="i" class="plainrow">
                      <span>{{ u.title }}</span>
                      <span class="s">{{ u.second }}</span>
                    </div>
                  </template>
                  <template v-else>
                    <template v-for="g in groups" :key="g.group">
                      <div v-if="g.folds" class="group fold" @click="open[g.group as 'skim' | 'mechanical'] = !open[g.group as 'skim' | 'mechanical']">
                        <span class="groupname">{{ g.name.toUpperCase() }}</span>
                        <span class="show">{{ open[g.group as 'skim' | 'mechanical'] ? 'Hide' : 'Show' }}</span>
                      </div>
                      <span v-else class="group">{{ g.name.toUpperCase() }}</span>
                      <template v-if="!g.folds || open[g.group as 'skim' | 'mechanical']">
                        <ItemRow
                          v-for="i in g.items"
                          :key="i.id"
                          :item="i"
                          :selected="i.id === sel"
                          :tickable="tickable(i)"
                          :ticked="isTicked(i)"
                          :disabled="!canDecide"
                          @select="select"
                          @tick="tick"
                        />
                      </template>
                    </template>
                    <template v-if="list.length === 0 || ledger.phase === 'finished'">
                      <span class="group">WHAT WAS OBSERVED</span>
                      <span v-for="f in facts(ledger)" :key="f" class="fact">{{ f }}</span>
                    </template>
                    <span v-if="changes.reading_note" class="fact">{{ changes.reading_note }}</span>
                    <span v-if="changes.captured && !changes.in_repo" class="fact">The working folder is not in a git repository, so the session's changes to it were not recorded.</span>
                  </template>
                </template>

                <template v-else-if="tab === 'servers'">
                  <span class="group">SERVERS</span>
                  <span v-if="serverRows.length === 0" class="empty">Every server the agent contacts is listed here with what it received.</span>
                  <div v-for="s in serverRows" :key="s.host" class="plainrow">
                    <span class="mono">{{ s.host }}</span>
                    <span :class="s.flagged ? 's noted' : 's'">{{ s.second }}</span>
                  </div>
                </template>

                <template v-else>
                  <span class="group">RECORD</span>
                  <span v-if="check === null" class="empty">Checking the record…</span>
                  <template v-else>
                    <div v-if="check.verified" class="words line">
                      <span>All {{ check.entries }} entries</span>
                      <span class="ok">intact</span>
                      <span>and the signature matches this computer's key</span>
                    </div>
                    <span v-else :class="live && !check.bad_entry ? 'fact' : 'fact bad'">{{ check.problem }}</span>
                    <span class="fact mono">{{ check.final_hash }}</span>
                  </template>
                  <span class="group">INVESTIGATE</span>
                  <span class="fact">What the rules find in this record, each statement beside the entries it rests on.</span>
                  <button class="inline" @click="view = 'investigate'">Investigate this session</button>
                  <span class="group">TIMELINE</span>
                  <span v-if="entries.length === 0" class="empty">Each entry of the session's log is listed here in order.</span>
                  <div v-for="e in entries" :key="e.seq" class="plainrow" @click="openEntry = openEntry === e.seq ? 0 : e.seq">
                    <span class="mono">{{ e.seq }} {{ e.type }}</span>
                    <span class="s mono">{{ e.time }}</span>
                    <RawEntry v-if="openEntry === e.seq" :cite="String(e.seq)" :type="e.type" :time="e.time" :data="JSON.stringify(e.data)" :hash="e.hash" :prev="e.prev" />
                  </div>
                </template>
              </div>
            </div>

            <div v-if="view === 'session' && tab === 'review' && ledger !== null && undecided.length > 0 && live && ledger.phase !== 'running'" class="decide">
              <div class="col">
                <div class="box">
                  <span class="consequence">
                    {{ performing ? 'Performing what you allowed…' : canDecide ? consequence : 'Reading the actions before you decide…' }}
                  </span>
                  <button class="primary" :disabled="!canDecide" @click="decide">{{ label }}</button>
                </div>
              </div>
            </div>
          </div>

          <div v-if="selected !== null && !narrow && view === 'session' && tab === 'review'" class="divider"></div>
          <DetailPane
            v-if="selected !== null && view === 'session' && tab === 'review'"
            :item="selected"
            :home="ledger?.home"
            :ticked="ticked.has(selected.id)"
            :text="texts[selected.id]"
            :alone="narrow"
            @close="sel = ''"
          />
        </div>
      </div>
    </div>

    <div class="bar">
      <span v-if="view === 'investigate'" :class="investFailing ? 'bad' : ''">{{ investStatus }}</span>
      <span v-if="view === 'session'" :class="ledger !== null && !ledger.record.verified ? 'bad' : ''">{{ recordWord }}</span>
      <span v-if="ledger !== null && view === 'session'" class="mono">{{ narrow ? ledger.record.entries : `${ledger.record.entries} entries` }}</span>
      <span class="end mono">{{ info.reviewer !== '' ? (narrow ? info.reviewer : `${info.reviewer}, on this computer`) : narrow ? 'no model' : 'no local model: built-in rules only' }}</span>
      <span class="mono">v{{ info.version }}</span>
    </div>
  </div>
</template>

<style>
/* One place for every colour, space, size and radius. Components use these names only.
   --chrome is the window, --bg the panel, --raise what is raised or selected inside the panel.
   --accent is for selection markers, focus and links only. --high, --med and --ok colour the words
   High, Medium and Allowed and nothing else. --line is decoration; an edge that must be seen is --sec. */
:root {
  --chrome: #e8eaec;
  --bg: #fcfcfd;
  --raise: #e1e3e5;
  --text: #171a1d;
  --sec: #44474b;
  --line: #cdcfd2;
  --accent: #1f3d69;
  --high: #920a11;
  --med: #8b5002;
  --ok: #0b6f49;
  --add: rgba(11, 111, 73, 0.11);
  --del: rgba(146, 10, 17, 0.11);

  --s4: 4px;
  --s8: 8px;
  --s12: 12px;
  --s16: 16px;
  --s20: 20px;
  --s24: 24px;
  --s32: 32px;
  --s40: 40px;
  --s48: 48px;

  --r-panel: 12px;
  --r-row: 8px;
  --r-box: 16px;
  /* half the button's 44px height: round ends on every platform. A larger value draws an ellipse on Windows. */
  --r-pill: 22px;

  --fs: 15px;
  --fs-small: 13px;
  --fs-tiny: 12px;
  --fs-mono: 13px;
  --fs-h1: 22px;
  --fs-status: 26px;
  --fs-status-small: 20px;
  --mono: ui-monospace, 'Cascadia Mono', 'DejaVu Sans Mono', Consolas, monospace;

  --side-width: 248px;
  --col-width: 680px;
  --list-width: 420px;
  --detail-width: 820px;
  --step-width: 220px;
  --via-width: 152px;
  --seq-width: 56px;
  --time-width: 72px;
  --row-min: 40px;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme='light']) {
    --chrome: #0c0d0f;
    --bg: #161719;
    --raise: #252729;
    --text: #e8e9eb;
    --sec: #b2b4b8;
    --line: #343538;
    --accent: #b5d0f5;
    --high: #f76565;
    --med: #eab946;
    --ok: #82e2ad;
    --add: rgba(130, 226, 173, 0.11);
    --del: rgba(247, 101, 101, 0.11);
  }
}
:root[data-theme='dark'] {
  --chrome: #0c0d0f;
  --bg: #161719;
  --raise: #252729;
  --text: #e8e9eb;
  --sec: #b2b4b8;
  --line: #343538;
  --accent: #b5d0f5;
  --high: #f76565;
  --med: #eab946;
  --ok: #82e2ad;
  --add: rgba(130, 226, 173, 0.11);
  --del: rgba(247, 101, 101, 0.11);
}
</style>

<style scoped>
.window {
  display: flex;
  flex-direction: column;
  flex: 1;
  background: var(--chrome);
  color: var(--text);
  font-size: var(--fs);
  line-height: 1.5;
}
.shell {
  display: flex;
  flex-direction: row;
  flex: 1;
  min-height: 0;
}
.panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  margin: var(--s8) var(--s8) 0 0;
  border-radius: var(--r-panel);
  background: var(--bg);
  overflow: hidden;
}
.panel.narrow {
  margin: var(--s8) var(--s8) 0;
}
.head {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  align-items: center;
  justify-content: flex-end;
  gap: var(--s12);
  min-height: 44px;
  padding: var(--s8) var(--s20);
}
.head.stacked {
  flex-direction: column;
  align-items: stretch;
  gap: var(--s8);
  padding: var(--s12) var(--s12) var(--s4);
}
.divider {
  flex-shrink: 0;
  width: 1px;
  background: var(--line);
}
.title {
  flex: 1;
}
.chooser {
  flex: 1;
}
.work {
  display: flex;
  flex-direction: row;
  flex: 1;
  min-height: 0;
}
.list {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.list.open {
  flex-grow: 0;
  flex-shrink: 0;
  flex-basis: var(--list-width);
  width: var(--list-width);
}
.scroll {
  display: flex;
  flex-direction: column;
  flex: 1;
  align-items: center;
  overflow: auto;
}
.col {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 100%;
  max-width: var(--col-width);
  padding: 0 var(--s20) var(--s24);
}
.status {
  flex-shrink: 0;
  margin: var(--s40) var(--s12) var(--s8);
  font-size: var(--fs-status);
  font-weight: 400;
  line-height: 1.2;
  letter-spacing: -0.02em;
}
.status.small {
  margin-top: var(--s16);
  font-size: var(--fs-status-small);
}
.problem {
  flex-shrink: 0;
  margin: var(--s8) var(--s12);
  font-weight: 500;
}
.group {
  flex-shrink: 0;
  margin: var(--s24) var(--s12) var(--s4);
  font-size: var(--fs-tiny);
  letter-spacing: 0.06em;
  color: var(--sec);
}
.group.fold {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  min-height: 44px;
  margin: var(--s12) 0 0;
  padding: 0 var(--s12);
  border-radius: var(--r-row);
}
.group.fold:hover {
  background: var(--raise);
}
.groupname {
  flex: 1;
  font-size: var(--fs-tiny);
  letter-spacing: 0.06em;
}
.show {
  flex-shrink: 0;
  min-width: var(--s48);
  text-align: right;
  letter-spacing: 0;
  font-size: var(--fs-small);
  color: var(--accent);
  text-decoration: underline;
}
.plainrow {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  min-height: 44px;
  padding: var(--s8) var(--s12);
  border-radius: var(--r-row);
}
.plainrow:hover {
  background: var(--raise);
}
.s {
  font-size: var(--fs-small);
  color: var(--sec);
}
.s.noted {
  color: var(--text);
  font-weight: 500;
}
.fact {
  flex-shrink: 0;
  margin: var(--s4) var(--s12);
  max-width: 560px;
}
.empty {
  flex-shrink: 0;
  margin: var(--s8) var(--s12);
  max-width: 560px;
  color: var(--sec);
}
.ghost {
  flex-shrink: 0;
  height: 44px;
  margin: var(--s8) var(--s12) 0;
  border-radius: var(--r-row);
  background: var(--raise);
}
.decide {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  align-items: center;
  padding: var(--s12) 0 calc(var(--s16) + env(safe-area-inset-bottom));
}
.decide .col {
  padding-bottom: 0;
}
.box {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s16);
  padding: var(--s12) var(--s12) var(--s12) var(--s20);
  border-radius: var(--r-box);
  background: var(--raise);
}
.consequence {
  flex: 1;
  font-size: var(--fs-small);
  color: var(--sec);
}
.primary {
  min-height: 44px;
  padding: var(--s8) var(--s20);
  border-radius: var(--r-pill);
  background: var(--text);
  color: var(--bg);
  font-weight: 500;
}
.bar {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s20);
  padding: var(--s8) var(--s20);
  font-size: var(--fs-tiny);
  color: var(--sec);
}
.end {
  margin-left: auto;
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
}
.bar .mono {
  font-size: var(--fs-tiny);
}
/* a record that fails its check is a danger, and is coloured as one; the word "intact" is the colour for allowed */
.bad {
  color: var(--high);
}
.ok {
  color: var(--ok);
}
.words {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  gap: var(--s4);
}
.words.line {
  margin: var(--s4) var(--s12);
}
.inline {
  align-self: flex-start;
  margin: var(--s8) var(--s12);
}
</style>
