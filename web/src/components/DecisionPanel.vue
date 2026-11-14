<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import type { Held, HeldAction, PolicyState, Risk } from '../api'
import type { Row } from '../present'
import { decisionLabel, riskOf, rowsFor } from '../present'
import ActionRow from './ActionRow.vue'

const props = defineProps<{
  held: Held
  agent: string
  // true while a decision is on its way to rehearse
  busy: boolean
  policy: PolicyState
}>()

const emit = defineEmits<{
  apply: [approve: string[], rules: string[]]
  discard: []
}>()

// offered rules the user has ticked. None starts ticked: a rule is never
// added without being asked for.
const acceptedRules = reactive(new Set<string>())

function toggleRule(id: string): void {
  if (!canDecide.value) return
  if (!acceptedRules.delete(id)) acceptedRules.add(id)
}

// which actions are ticked, by ID. An action gets its starting state when
// its description arrives: high risk starts unticked, the rest ticked.
const ticked = reactive(new Map<string, boolean>())
const routineOpen = ref(false)
const panel = ref<HTMLElement | null>(null)

watch(
  () => props.held.actions,
  (actions) => {
    for (const a of actions) {
      // one the policy has already refused is not the user's to tick
      if (a.review !== null && a.decision === '' && !ticked.has(a.id)) {
        ticked.set(a.id, a.review.risk !== 'high')
      }
    }
  },
  { immediate: true },
)

// File changes get a group of their own, whatever their rating: they are
// about this computer, and the other groups are about what leaves it.
function rows(risk: Risk): Row[] {
  return rowsFor(
    props.held.actions.filter((a) => a.review !== null && a.file === null && riskOf(a) === risk),
    props.agent,
  )
}

const fileRows = computed(() => {
  const all = rowsFor(
    props.held.actions.filter((a) => a.review !== null && a.file !== null),
    props.agent,
  )
  const careful = (r: Row): boolean => r.risk === 'high'
  return [...all.filter(careful), ...all.filter((r) => !careful(r))]
})

// not yet described, so not yet in any risk group
const unread = computed(() =>
  rowsFor(
    props.held.actions.filter((a) => a.review === null),
    props.agent,
  ),
)
const high = computed(() => rows('high'))
const medium = computed(() => rows('medium'))
const low = computed(() => rows('low'))
const lowCount = computed(() => low.value.reduce((n, r) => n + r.actions.length, 0))

const decided = computed(() => props.held.phase === 'applying' || props.held.phase === 'finished')
const canDecide = computed(() => props.held.phase === 'deciding' && !props.busy)

const open = computed(() => props.held.actions.filter((a) => a.decision === '' || ticked.has(a.id)))
const allowed = computed<HeldAction[]>(() => props.held.actions.filter((a) => ticked.get(a.id) === true))
const label = computed(() => decisionLabel(allowed.value, open.value.length - allowed.value.length))

const routineLine = computed(() => {
  const n = lowCount.value
  const what = n === 1 ? '1 routine action' : `${n} routine actions`
  return decided.value ? `${what}.` : `${what}, ticked.`
})

function isTicked(row: Row): boolean {
  return row.actions.every((a) => ticked.get(a.id) === true)
}

function toggle(row: Row): void {
  if (!canDecide.value) return
  const next = !isTicked(row)
  for (const a of row.actions) {
    ticked.set(a.id, next)
  }
}

// j and k move between rows, Enter opens or closes the focused row's
// command. Space ticks, which the tick box does by itself when focused.
function onKey(event: KeyboardEvent): void {
  if (event.ctrlKey || event.metaKey || event.altKey || panel.value === null) return
  const boxes = [...panel.value.querySelectorAll<HTMLElement>('[data-row] input, [data-row] button')]
  const firstOfEachRow = boxes.filter((el, i) => i === 0 || el.closest('[data-row]') !== boxes[i - 1]?.closest('[data-row]'))
  const here = document.activeElement instanceof HTMLElement ? document.activeElement.closest('[data-row]') : null
  const at = firstOfEachRow.findIndex((el) => el.closest('[data-row]') === here)
  if (event.key === 'j' || event.key === 'k') {
    const next = at + (event.key === 'j' ? 1 : -1)
    firstOfEachRow[Math.max(0, Math.min(firstOfEachRow.length - 1, next))]?.focus()
    event.preventDefault()
  } else if (event.key === 'Enter' && here !== null && !(event.target instanceof HTMLButtonElement)) {
    here.querySelector<HTMLButtonElement>('button')?.click()
    event.preventDefault()
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div ref="panel" class="panel">
    <div v-if="high.length > 0" class="group">
      <h2 class="heading">Look at these</h2>
      <ActionRow v-for="row in high" :key="row.key" :row="row" :ticked="isTicked(row)" :can-decide="canDecide" @toggle="toggle" />
    </div>

    <div v-if="medium.length > 0" class="group">
      <h2 class="heading">Probably fine</h2>
      <ActionRow v-for="row in medium" :key="row.key" :row="row" :ticked="isTicked(row)" :can-decide="canDecide" @toggle="toggle" />
    </div>

    <div v-if="low.length > 0" class="group">
      <h2 class="heading">Routine</h2>
      <div class="quiet">
        <span class="count">{{ routineLine }}</span>
        <button class="link" type="button" :aria-expanded="routineOpen" @click="routineOpen = !routineOpen">
          {{ routineOpen ? 'Hide' : 'Show' }}
        </button>
      </div>
      <div v-if="routineOpen" class="rows">
        <ActionRow v-for="row in low" :key="row.key" :row="row" :ticked="isTicked(row)" :can-decide="canDecide" @toggle="toggle" />
      </div>
    </div>

    <div v-if="fileRows.length > 0" class="group">
      <h2 class="heading">File changes</h2>
      <ActionRow v-for="row in fileRows" :key="row.key" :row="row" :ticked="isTicked(row)" :can-decide="canDecide" @toggle="toggle" />
    </div>

    <div v-if="unread.length > 0" class="group">
      <h2 class="heading">Still being read</h2>
      <ActionRow v-for="row in unread" :key="row.key" :row="row" :ticked="false" :can-decide="false" @toggle="toggle" />
    </div>

    <div v-if="policy.ignored.length > 0" class="group">
      <h2 class="heading">Policy rules that were ignored</h2>
      <div v-for="ig in policy.ignored" :key="ig.source + ig.rule" class="note">
        <div class="rule">{{ ig.rule }}</div>
        <div class="why">In the {{ ig.source }} policy. It was not applied: {{ ig.why }}.</div>
      </div>
    </div>

    <div v-if="held.phase === 'deciding' && policy.offers.length > 0" class="group">
      <h2 class="heading">Stop being asked</h2>
      <div v-for="offer in policy.offers" :key="offer.id" class="offer" data-row>
        <div class="tick">
          <input
            :id="`rule-${offer.id}`"
            class="box"
            type="checkbox"
            :checked="acceptedRules.has(offer.id)"
            :disabled="!canDecide"
            @change="toggleRule(offer.id)"
          />
        </div>
        <div class="main">
          <label class="title" :for="`rule-${offer.id}`">Add this rule to .rehearsal/policy.yaml</label>
          <div class="rule">{{ offer.line }}</div>
          <div class="why">
            You have allowed this in {{ offer.times }} earlier sessions. With the rule, it is let through without being held. Nothing is
            written unless you tick this.
          </div>
        </div>
      </div>
    </div>

    <div v-if="held.phase === 'deciding'" class="footer">
      <button class="primary" type="button" :disabled="!canDecide" @click="emit('apply', allowed.map((a) => a.id), [...acceptedRules])">
        {{ label }}
      </button>
      <button class="plain" type="button" :disabled="!canDecide" @click="emit('discard')">Discard all</button>
    </div>
    <div v-if="!decided" class="hint">
      Nothing here has happened yet.
      {{ held.phase === 'deciding' ? 'Keys: j and k move, space ticks, enter shows the command or the changes.' : 'You can decide once every action has been read.' }}
    </div>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
}

.group {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
}

.heading {
  margin: 0;
  margin-bottom: 8px;
  font-size: 24px;
  font-weight: 600;
}

.rows {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
}

.quiet {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  min-height: 44px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
  font-size: 18px;
  color: var(--secondary);
}

.count {
  margin-right: 8px;
}

.link {
  min-height: 24px;
  padding: 0;
  border-width: 0;
  background-color: transparent;
  color: var(--text);
  font-family: inherit;
  font-size: 18px;
  text-decoration: underline;
  cursor: pointer;
}

.link:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.note {
  display: flex;
  flex-direction: column;
  padding-top: 12px;
  padding-bottom: 12px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
}

.offer {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  min-height: 44px;
  padding-top: 12px;
  padding-bottom: 12px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
}

.tick {
  display: flex;
  flex-shrink: 0;
  width: 32px;
  padding-top: 4px;
}

.box {
  width: 20px;
  height: 20px;
  margin: 0;
  accent-color: var(--text);
}

.box:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.main {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  min-width: 0;
}

.title {
  font-size: 18px;
  font-weight: 600;
}

.rule {
  margin-top: 4px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.why {
  margin-top: 4px;
  font-size: 16px;
  color: var(--secondary);
}

.footer {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  padding-top: 16px;
  border-top-width: 1px;
  border-top-style: solid;
  border-top-color: var(--hairline);
}

.primary {
  min-height: 44px;
  margin-right: 12px;
  margin-bottom: 8px;
  padding: 8px 16px;
  border-width: 1px;
  border-style: solid;
  border-color: var(--text);
  border-radius: 4px;
  background-color: var(--text);
  color: var(--bg);
  font-family: inherit;
  font-size: 18px;
  font-weight: 600;
  cursor: pointer;
}

.plain {
  min-height: 44px;
  margin-bottom: 8px;
  padding: 8px 16px;
  border-width: 1px;
  border-style: solid;
  border-color: var(--hairline);
  border-radius: 4px;
  background-color: transparent;
  color: var(--text);
  font-family: inherit;
  font-size: 18px;
  cursor: pointer;
}

.primary:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.plain:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.primary:disabled {
  border-color: var(--hairline);
  background-color: transparent;
  color: var(--secondary);
  cursor: default;
}

.plain:disabled {
  color: var(--secondary);
  cursor: default;
}

.hint {
  margin-top: 8px;
  margin-bottom: 32px;
  font-size: 14px;
  color: var(--secondary);
}
</style>
