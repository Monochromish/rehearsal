<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Identifier } from '../api'
import type { Row } from '../present'
import { outcomeOf, riskWords } from '../present'

const props = defineProps<{
  row: Row
  ticked: boolean
  // false before every action is described and after the decision is sent
  canDecide: boolean
}>()

const emit = defineEmits<{ toggle: [row: Row] }>()

const a = computed(() => props.row.first)
const outcome = computed(() => outcomeOf(a.value))

// the exact text starts open for high-risk rows only
const open = ref(props.row.risk === 'high')
watch(
  () => props.row.risk,
  (risk) => {
    open.value = risk === 'high'
  },
)

const reviewer = computed(() => {
  const review = a.value.review
  if (review === null) return 'Reviewer: still reading this one.'
  const who = review.source === 'model' ? 'Reviewer (AI, on this computer)' : 'Reviewer (built-in rules, no AI)'
  return `${who}: ${review.summary} ${review.concern}`
})

const state = computed(() => {
  if (outcome.value !== null) return { word: outcome.value.word, tone: outcome.value.tone }
  if (a.value.review === null) return { word: 'Reading', tone: 'plain' }
  return { word: riskWords[props.row.risk], tone: props.row.risk === 'low' ? 'plain' : props.row.risk }
})

const boxID = computed(() => `tick-${props.row.key}`)

const carriedWords = { real: 'a detail of yours', canary: 'a planted value', secret: 'a secret' }

function carried(item: Identifier): string {
  return `Carries ${carriedWords[item.kind]}: ${item.label}`
}

// One sentence on where the destination's address came from. It states
// only what was observed.
const provenance = computed(() => {
  const p = a.value.provenance
  if (p === null) return ''
  const ago = p.secondsBefore === 1 ? '1 second' : `${p.secondsBefore} seconds`
  return `The address this is going to first appeared in a page from ${p.sourceHost}, read ${ago} earlier.`
})

function more(n: number, what: string): string {
  return n > 0 ? `and ${n} more ${what}` : ''
}

defineExpose({ toggleCommand: () => (open.value = !open.value) })
</script>

<template>
  <div class="row" :data-row="row.key">
    <div class="tick">
      <input
        v-if="outcome === null && a.review !== null"
        :id="boxID"
        class="box"
        type="checkbox"
        :checked="ticked"
        :disabled="!canDecide"
        @change="emit('toggle', row)"
      />
    </div>

    <div class="main">
      <label class="title" :for="boxID">{{ row.title }}</label>
      <div class="route">{{ row.line }}</div>
      <div class="reviewer">{{ reviewer }}</div>
      <div v-if="a.needsRealAnswer" class="reviewer">
        The agent was given a made-up answer for this. If it needed the real one, what it did next may be wrong.
      </div>
      <div v-if="a.secretFiles.length > 0" class="warning">
        Includes files that look like passwords or keys: {{ a.secretFiles.join(', ') }}
      </div>
      <div v-if="provenance !== ''" class="warning">{{ provenance }}</div>
      <div v-if="a.providerDoor" class="warning">
        This goes to the AI provider under a different account from the one this session has used, or as a file upload.
      </div>
      <div v-for="item in a.carries" :key="item.id" class="warning">{{ carried(item) }}</div>
      <div v-if="outcome !== null && a.detail !== ''" class="command">{{ a.detail }}</div>

      <button class="link" type="button" :aria-expanded="open" @click="open = !open">
        {{ open ? 'Hide command' : 'Show command' }}
      </button>
      <div v-if="open" class="command">
        <div>{{ a.exact }}</div>
        <div v-if="row.actions.length > 1">Issued {{ row.actions.length }} times.</div>
        <div v-if="a.workDir !== ''">Run in {{ a.workDir }}</div>
        <div v-if="a.size > 0">{{ a.size }} bytes {{ a.contentType }}</div>
        <div v-if="a.outsideSources.length > 0">Issued after the agent read content from: {{ a.outsideSources.join(', ') }}</div>
        <div v-if="a.commits.length > 0" class="list">
          <div>Changes it uploads:</div>
          <div v-for="(message, i) in a.commits" :key="i" class="entry">{{ message }}</div>
          <div class="entry">{{ more(a.commitsOmitted, 'changes') }}</div>
        </div>
        <div v-if="a.files.length > 0" class="list">
          <div>Files added or changed:</div>
          <div v-for="file in a.files" :key="file" class="entry">{{ file }}</div>
          <div class="entry">{{ more(a.filesOmitted, 'files') }}</div>
        </div>
      </div>
    </div>

    <div :class="`state state-${state.tone}`">{{ state.word }}</div>
  </div>
</template>

<style scoped>
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

.warning {
  margin-top: 4px;
  font-size: 16px;
  color: var(--high);
}

.link {
  align-self: flex-start;
  min-height: 24px;
  margin-top: 8px;
  padding: 0;
  border-width: 0;
  background-color: transparent;
  color: var(--text);
  font-family: inherit;
  font-size: 14px;
  text-decoration: underline;
  cursor: pointer;
}

.link:focus-visible {
  outline: 2px solid var(--text);
  outline-offset: 2px;
}

.command {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
  padding: 8px 12px;
  border-radius: 4px;
  background-color: var(--sunken);
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.list {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
}

.entry {
  padding-left: 16px;
}

.state {
  flex-shrink: 0;
  width: 72px;
  margin-left: 16px;
  font-size: 16px;
  font-weight: 600;
  text-align: right;
}

.state-high {
  color: var(--high);
}

.state-medium {
  color: var(--medium);
}

.state-allowed {
  color: var(--allowed);
}

.state-plain {
  color: var(--secondary);
}
</style>
