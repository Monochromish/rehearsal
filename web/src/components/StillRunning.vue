<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { LeftProcess, Leftovers } from '../api'
import BreakableText from './BreakableText.vue'

// The processes the agent started that were still running when it
// exited. The rows and the button are drawn by the same rules as a held
// action's row and the decision's button.
const props = defineProps<{
  leftovers: Leftovers
  agent: string
  // true while a request to stop is on its way to rehearse
  busy: boolean
}>()

const emit = defineEmits<{ stop: [ids: string[]] }>()

// which processes are ticked, by ID. Each starts ticked.
const ticked = reactive(new Map<string, boolean>())

watch(
  () => props.leftovers.processes,
  (processes) => {
    for (const p of processes) {
      if (!ticked.has(p.id)) ticked.set(p.id, true)
    }
  },
  { immediate: true },
)

const canStop = computed(() => props.leftovers.open && !props.busy)
const running = computed(() => props.leftovers.processes.filter((p) => p.status === 'running'))
const chosen = computed(() => running.value.filter((p) => ticked.get(p.id) === true))

// the button says what it will do
const label = computed(() => {
  const stop = chosen.value.length
  const leave = running.value.length - stop
  if (stop === 0) return leave === 1 ? 'Leave it running' : 'Leave them running'
  return leave === 0 ? `Stop ${stop}` : `Stop ${stop}, leave ${leave} running`
})

function toggle(p: LeftProcess): void {
  if (!canStop.value) return
  ticked.set(p.id, ticked.get(p.id) !== true)
}

function some(n: number, unit: string): string {
  return n === 1 ? `1 ${unit}` : `${n} ${unit}s`
}

function lasted(seconds: number): string {
  if (seconds < 60) return some(seconds, 'second')
  if (seconds < 3600) return some(Math.floor(seconds / 60), 'minute')
  return `${some(Math.floor(seconds / 3600), 'hour')} ${some(Math.floor((seconds % 3600) / 60), 'minute')}`
}

function line(p: LeftProcess): string {
  const ran = p.status === 'running' ? 'running for' : 'ran for'
  let ports = ''
  if (p.ports.length === 1) ports = `, listening on port ${p.ports[0]}`
  if (p.ports.length > 1) ports = `, listening on ports ${p.ports.join(', ')}`
  return `process ${p.pid}, ${ran} ${lasted(p.seconds)}${ports}`
}

function note(p: LeftProcess): string {
  if (p.status === 'stopped') return 'It exited when asked to.'
  if (p.status === 'forced') return 'It did not exit when asked to, and was stopped by force.'
  if (p.status === 'gone') return 'It had already exited by itself. Nothing was sent to it.'
  if (p.status === 'failed') return `It could not be stopped: ${p.detail}`
  return props.leftovers.open ? '' : 'Left running.'
}

const words = { running: 'Running', stopped: 'Stopped', forced: 'Stopped', gone: 'Gone', failed: 'Failed' }
</script>

<template>
  <div class="panel">
    <div class="group">
      <h2 class="heading">Still running</h2>
      <div v-for="p in leftovers.processes" :key="p.id" class="row">
        <div class="tick">
          <input
            v-if="p.status === 'running' && leftovers.open"
            :id="`left-${p.id}`"
            class="box"
            type="checkbox"
            :checked="ticked.get(p.id) === true"
            :disabled="!canStop"
            @change="toggle(p)"
          />
        </div>
        <div class="main">
          <label class="title" :for="`left-${p.id}`"><BreakableText :text="p.command" /></label>
          <div class="route">{{ line(p) }}</div>
          <div v-if="note(p) !== ''" class="reviewer">{{ note(p) }}</div>
        </div>
        <div class="state state-plain">{{ words[p.status] }}</div>
      </div>
    </div>

    <div v-if="leftovers.open" class="footer">
      <button class="primary" type="button" :disabled="!canStop" @click="emit('stop', chosen.map((p) => p.id))">
        {{ label }}
      </button>
    </div>
    <div v-if="leftovers.open" class="hint">
      Started by {{ agent }} and still running after it exited. A ticked one is asked to exit, and stopped by force if it has not after 3
      seconds.
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

.row {
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
  outline: 2px solid var(--accent);
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
  overflow-wrap: anywhere;
}

.route {
  margin-top: 4px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  color: var(--secondary);
  overflow-wrap: anywhere;
}

.reviewer {
  margin-top: 4px;
  font-size: 16px;
  color: var(--secondary);
}

.state {
  flex-shrink: 0;
  width: 72px;
  margin-left: 16px;
  font-size: 16px;
  font-weight: 600;
  text-align: right;
}

.state-plain {
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
  color: var(--panel);
  font-family: inherit;
  font-size: 18px;
  font-weight: 600;
  cursor: pointer;
}

.primary:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.primary:disabled {
  border-color: var(--secondary);
  background-color: transparent;
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
