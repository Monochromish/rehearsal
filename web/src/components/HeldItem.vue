<script setup lang="ts">
import { computed, ref } from 'vue'
import type { HeldAction } from '../api'
import IdentifierTag from './IdentifierTag.vue'

const props = defineProps<{
  action: HeldAction
  ticked: boolean
  // false once the decision has been sent, or before every action is described
  canDecide: boolean
}>()

const emit = defineEmits<{ toggle: [id: string] }>()

const open = ref(false)

const risk = computed(() => props.action.review?.risk ?? 'medium')
const riskLabel = computed(() => {
  if (props.action.review === null) return 'BEING READ'
  if (risk.value === 'high') return 'HIGH RISK'
  if (risk.value === 'medium') return 'NEEDS A LOOK'
  return 'ROUTINE'
})

const writtenBy = computed(() => {
  const review = props.action.review
  if (review === null) return ''
  if (review.source === 'model') return 'Explanation written by an AI model on this computer. Check it against the exact text below.'
  return `Explanation written by Rehearsal's built-in rules, because ${review.note}.`
})

const exactLabel = computed(() => (props.action.kind === 'http' ? 'Exact request' : 'Exact command'))

const result = computed(() => {
  const a = props.action
  if (a.outcome === 'applied') return { text: 'Done. This was performed.', bad: false }
  if (a.outcome === 'failed') return { text: 'Failed. This was not completed.', bad: true }
  if (a.outcome === 'not_applied') return { text: 'Not attempted, because an earlier action failed.', bad: true }
  if (a.decision === 'refused') return { text: 'Refused. This was not performed.', bad: false }
  if (a.decision === 'approved') return { text: 'Approved. Waiting its turn.', bad: false }
  return null
})

function more(n: number): string {
  return n > 0 ? `and ${n} more not shown` : ''
}
</script>

<template>
  <div :class="`item item-${risk}`">
    <div class="head">
      <div :class="`risk risk-${risk}`">{{ riskLabel }}</div>
      <div v-if="action.needsRealAnswer" class="unreal">THE AGENT WAS GIVEN A MADE-UP ANSWER</div>
    </div>

    <div v-if="action.review !== null" class="summary">{{ action.review.summary }}</div>
    <div v-else class="summary summary-pending">Working out what this would do…</div>
    <div v-if="action.review !== null" class="concern">What could go wrong: {{ action.review.concern }}</div>
    <div v-if="writtenBy !== ''" class="written-by">{{ writtenBy }}</div>

    <div class="exact-label">{{ exactLabel }}</div>
    <div class="exact">{{ action.exact }}</div>

    <div v-if="action.needsRealAnswer" class="note">
      The agent would normally use the real result of this action (for example the number of the thing it created). It
      was given a stand-in answer, so what it did afterwards may be wrong.
    </div>
    <div v-if="action.secretFiles.length > 0" class="note note-bad">
      Includes files that look like passwords or keys: {{ action.secretFiles.join(', ') }}
    </div>

    <div v-if="result !== null" :class="result.bad ? 'result result-bad' : 'result'">
      <div>{{ result.text }}</div>
      <div v-if="action.detail !== ''" class="result-detail">{{ action.detail }}</div>
    </div>

    <div class="controls">
      <button
        v-if="result === null && action.review !== null"
        class="tick"
        type="button"
        role="checkbox"
        :aria-checked="ticked"
        :aria-label="`Perform this action: ${action.exact}`"
        :disabled="!canDecide"
        @click="emit('toggle', action.id)"
      >
        <span class="box">{{ ticked ? '✓' : '' }}</span>
        <span>{{ ticked ? 'Ticked: this will be performed' : 'Not ticked: this will be skipped' }}</span>
      </button>
      <button class="more" type="button" :aria-expanded="open" @click="open = !open">
        {{ open ? 'Hide details' : 'Show details' }}
      </button>
    </div>

    <div v-if="open" class="details">
      <div class="row">
        <div class="key">Goes to</div>
        <div class="value">{{ action.destination }}</div>
      </div>
      <div v-if="action.workDir !== ''" class="row">
        <div class="key">Run from folder</div>
        <div class="value">{{ action.workDir }}</div>
      </div>
      <div v-if="action.subject !== ''" class="row">
        <div class="key">What it sends</div>
        <div class="value">{{ action.subject }}</div>
      </div>
      <div v-if="action.size > 0" class="row">
        <div class="key">Size of the data</div>
        <div class="value">{{ action.size }} bytes {{ action.contentType }}</div>
      </div>
      <div v-if="action.commits.length > 0" class="row">
        <div class="key">Changes being uploaded</div>
        <div class="value">
          <div v-for="(message, i) in action.commits" :key="i">{{ message }}</div>
          <div>{{ more(action.commitsOmitted) }}</div>
        </div>
      </div>
      <div v-if="action.files.length > 0" class="row">
        <div class="key">Files added or changed</div>
        <div class="value">
          <div v-for="file in action.files" :key="file">{{ file }}</div>
          <div>{{ more(action.filesOmitted) }}</div>
        </div>
      </div>
      <div class="row">
        <div class="key">Details of yours it carries</div>
        <div v-if="action.carries.length === 0" class="value">None found.</div>
        <div v-else class="tags">
          <IdentifierTag v-for="item in action.carries" :key="item.id" :item="item" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.item {
  display: flex;
  flex-direction: column;
  margin-bottom: 14px;
  padding: 16px 20px;
  border-width: 3px;
  border-style: solid;
  border-radius: 12px;
  background-color: var(--surface);
}

.item-high {
  border-color: var(--flag-border);
}

.item-medium {
  border-color: var(--real-border);
}

.item-low {
  border-color: var(--border);
}

.head {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.risk {
  margin-right: 10px;
  margin-bottom: 4px;
  padding: 2px 10px;
  border-radius: 6px;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 1px;
}

.risk-high {
  background-color: var(--flag-bg);
  color: var(--flag-text);
}

.risk-medium {
  background-color: var(--real-bg);
  color: var(--real-text);
}

.risk-low {
  background-color: var(--bg);
  color: var(--muted);
}

.unreal {
  margin-bottom: 4px;
  padding: 2px 10px;
  border-radius: 6px;
  background-color: var(--canary-bg);
  color: var(--canary-text);
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 1px;
}

.summary {
  font-size: 28px;
  font-weight: 700;
}

.summary-pending {
  color: var(--muted);
}

.concern {
  margin-top: 6px;
  font-size: 22px;
}

.written-by {
  margin-top: 6px;
  font-size: 18px;
  color: var(--muted);
}

.exact-label {
  margin-top: 12px;
  font-size: 18px;
  font-weight: 700;
  color: var(--muted);
}

.exact {
  padding: 8px 12px;
  border-radius: 8px;
  background-color: var(--bg);
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 20px;
  overflow-wrap: anywhere;
}

.note {
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  background-color: var(--canary-bg);
  color: var(--canary-text);
  font-size: 20px;
}

.note-bad {
  background-color: var(--flag-bg);
  color: var(--flag-text);
  font-weight: 600;
}

.result {
  display: flex;
  flex-direction: column;
  margin-top: 12px;
  padding: 8px 12px;
  border-radius: 8px;
  background-color: var(--bg);
  font-size: 22px;
  font-weight: 700;
}

.result-bad {
  background-color: var(--flag-bg);
  color: var(--flag-text);
}

.result-detail {
  margin-top: 4px;
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 18px;
  font-weight: 400;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.controls {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  margin-top: 12px;
}

.tick {
  display: flex;
  flex-direction: row;
  align-items: center;
  min-height: 48px;
  margin-right: 12px;
  margin-bottom: 6px;
  padding: 6px 14px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 8px;
  background-color: var(--surface);
  color: var(--text);
  font-family: inherit;
  font-size: 22px;
  font-weight: 600;
  cursor: pointer;
}

.box {
  display: flex;
  justify-content: center;
  align-items: center;
  width: 30px;
  height: 30px;
  margin-right: 10px;
  border-width: 3px;
  border-style: solid;
  border-color: var(--text);
  border-radius: 6px;
  font-size: 24px;
  line-height: 1;
}

.more {
  min-height: 48px;
  margin-bottom: 6px;
  padding: 6px 14px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 8px;
  background-color: var(--surface);
  color: var(--text);
  font-family: inherit;
  font-size: 20px;
  cursor: pointer;
}

.details {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
  padding-top: 8px;
  border-top-width: 2px;
  border-top-style: solid;
  border-top-color: var(--border);
}

.row {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.key {
  flex-basis: 260px;
  flex-shrink: 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--muted);
}

.value {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  flex-basis: 300px;
  min-width: 0;
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 20px;
  overflow-wrap: anywhere;
}

.tags {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  flex-grow: 1;
  flex-basis: 300px;
}
</style>
