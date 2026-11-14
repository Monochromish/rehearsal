<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Held, HeldAction } from '../api'
import HeldItem from './HeldItem.vue'

const props = defineProps<{
  held: Held
  // true while a decision is on its way to rehearse
  busy: boolean
}>()

const emit = defineEmits<{
  apply: [approve: string[]]
  discard: []
}>()

// which actions are ticked, by ID. An action gets its starting state when
// its description arrives: high risk starts unticked, the rest ticked.
const ticked = reactive(new Map<string, boolean>())
const routineOpen = ref(false)

watch(
  () => props.held.actions,
  (actions) => {
    for (const a of actions) {
      if (a.review !== null && !ticked.has(a.id)) {
        ticked.set(a.id, a.review.risk !== 'high')
      }
    }
  },
  { immediate: true },
)

const rank = { high: 0, medium: 1, low: 2 }

function byRisk(wanted: (a: HeldAction) => boolean): HeldAction[] {
  return props.held.actions
    .filter(wanted)
    .slice()
    .sort((a, b) => rank[a.review?.risk ?? 'medium'] - rank[b.review?.risk ?? 'medium'])
}

const important = computed(() => byRisk((a) => a.review === null || a.review.risk !== 'low'))
const routine = computed(() => byRisk((a) => a.review !== null && a.review.risk === 'low'))

const described = computed(() => props.held.actions.filter((a) => a.review !== null).length)
const canDecide = computed(() => props.held.phase === 'deciding' && !props.busy)
const decided = computed(() => props.held.phase === 'applying' || props.held.phase === 'finished')
const approve = computed(() => props.held.actions.filter((a) => ticked.get(a.id) === true).map((a) => a.id))

const byRules = computed(() => props.held.actions.filter((a) => a.review?.source === 'rules').length)

const heading = computed(() => {
  if (props.held.phase === 'finished') return 'What happened to the held actions'
  if (props.held.phase === 'applying') return 'Performing what you approved'
  return 'Needs your decision'
})

const hint = computed(() => {
  const n = props.held.actions.length
  if (props.held.phase === 'reviewing') return `Reading what the agent tried to do: ${described.value} of ${n} done.`
  if (decided.value) return 'Each held action is shown with what became of it.'
  return 'The agent tried to do these things. None of them has happened yet. Tick the ones you want performed.'
})

function toggle(id: string): void {
  if (canDecide.value) {
    ticked.set(id, ticked.get(id) !== true)
  }
}

</script>

<template>
  <div class="panel">
    <h2 class="title">{{ heading }}</h2>
    <div class="hint">{{ hint }}</div>
    <div v-if="byRules > 0" class="fallback">
      The AI model on this computer did not describe {{ byRules === held.actions.length ? 'these' : `${byRules} of these` }}.
      Those explanations come from Rehearsal's built-in rules instead.
    </div>

    <div v-if="important.length === 0" class="none">Nothing here needs a close look.</div>
    <HeldItem
      v-for="action in important"
      :key="action.id"
      :action="action"
      :ticked="ticked.get(action.id) === true"
      :can-decide="canDecide"
      @toggle="toggle"
    />

    <div v-if="routine.length > 0" class="routine">
      <h2 class="title">Routine</h2>
      <div class="hint">
        {{ routine.length === 1 ? '1 action' : `${routine.length} actions` }} rated low risk. They start ticked.
      </div>
      <button class="expand" type="button" :aria-expanded="routineOpen" @click="routineOpen = !routineOpen">
        {{ routineOpen ? 'Hide the routine actions' : 'Show the routine actions' }}
      </button>
      <div v-if="routineOpen" class="routine-list">
        <HeldItem
          v-for="action in routine"
          :key="action.id"
          :action="action"
          :ticked="ticked.get(action.id) === true"
          :can-decide="canDecide"
          @toggle="toggle"
        />
      </div>
    </div>

    <div v-if="!decided" class="buttons">
      <button class="apply" type="button" :disabled="!canDecide" @click="emit('apply', approve)">
        {{ approve.length === 1 ? 'Perform the 1 ticked action' : `Perform the ${approve.length} ticked actions` }}
      </button>
      <button class="discard" type="button" :disabled="!canDecide" @click="emit('discard')">Discard everything</button>
    </div>
    <div v-if="busy && !decided" class="hint">Sending your decision…</div>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
}

.title {
  margin: 0;
  font-size: 34px;
  font-weight: 700;
}

.hint {
  margin-bottom: 12px;
  font-size: 22px;
  color: var(--muted);
}

.fallback {
  margin-bottom: 12px;
  padding: 10px 14px;
  border-width: 2px;
  border-style: dashed;
  border-color: var(--real-border);
  border-radius: 10px;
  background-color: var(--real-bg);
  color: var(--real-text);
  font-size: 20px;
}

.none {
  margin-bottom: 14px;
  padding: 16px 20px;
  border-width: 2px;
  border-style: dashed;
  border-color: var(--border);
  border-radius: 12px;
  font-size: 22px;
  color: var(--muted);
}

.routine {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
}

.routine-list {
  display: flex;
  flex-direction: column;
  margin-top: 12px;
}

.expand {
  align-self: flex-start;
  min-height: 48px;
  padding: 6px 16px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 8px;
  background-color: var(--surface);
  color: var(--text);
  font-family: inherit;
  font-size: 22px;
  cursor: pointer;
}

.buttons {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  margin-top: 16px;
}

.apply {
  min-height: 56px;
  margin-right: 14px;
  margin-bottom: 10px;
  padding: 10px 22px;
  border-width: 3px;
  border-style: solid;
  border-color: var(--live);
  border-radius: 10px;
  background-color: var(--live);
  color: var(--bg);
  font-family: inherit;
  font-size: 24px;
  font-weight: 700;
  cursor: pointer;
}

.discard {
  min-height: 56px;
  margin-bottom: 10px;
  padding: 10px 22px;
  border-width: 3px;
  border-style: solid;
  border-color: var(--flag-border);
  border-radius: 10px;
  background-color: var(--surface);
  color: var(--flag-text);
  font-family: inherit;
  font-size: 24px;
  font-weight: 700;
  cursor: pointer;
}
</style>
