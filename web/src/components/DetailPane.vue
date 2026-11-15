<script setup lang="ts">
import Glyph from './Glyph.vue'
import { computed } from 'vue'
import type { Item } from '../model'
import { evidence, headline, outcome, tilde } from '../model'

const props = defineProps<{
  item: Item
  home: string | undefined
  ticked: boolean
  // the text of a change: undefined while it is being fetched
  text: string | undefined
  // shown in place of the list, with a way back
  alone: boolean
}>()

const emit = defineEmits<{ close: [] }>()

const heading = computed(() => {
  const i = props.item
  if (i.action !== undefined) return headline(i.action, props.home)
  if (i.claim !== undefined) return 'The agent said this, and a command it ran says otherwise.'
  const p = i.part
  if (p === undefined) return i.title
  if (p.old_path !== undefined && p.old_path !== '') return `${p.old_path} moved to ${p.path}`
  return p.line !== undefined && p.line > 0 ? `${p.path}, from line ${p.line}` : p.path
})

// risk and what will happen to it
const fate = computed(() => {
  const a = props.item.action
  if (a === undefined) return null
  const done = outcome(a)
  if (done !== null) return { text: `${done.word}${a.detail !== '' ? `: ${a.detail}` : ''}`, tone: done.tone }
  if (a.review === null) return { text: 'Reading…', tone: 'plain' }
  const risk = { high: 'High', medium: 'Medium', low: 'Low' }[a.review.risk]
  return { text: `${risk} risk, ${props.ticked ? 'will be allowed' : 'will be refused'}`, tone: a.review.risk === 'high' ? 'high' : 'plain' }
})

const reviewer = computed(() => {
  const r = props.item.action?.review
  if (r === undefined || r === null) return null
  return { text: `${r.summary} ${r.concern}`, by: r.source === 'model' ? 'Written by an AI model on this computer.' : 'Written by the built-in rules, with no AI.' }
})

const flags = computed(() => {
  const a = props.item.action
  const out: string[] = []
  if (a === undefined) return out
  if (a.provenance !== undefined) {
    out.push(`The address first appeared in content read from ${a.provenance.source_host}, ${a.provenance.seconds_before} seconds earlier.`)
  }
  if (a.provider_door === true) out.push('This goes to the AI provider under a different account from the one this session has used, or as a file upload.')
  for (const c of a.carries) out.push(`Carries ${{ real: 'a detail of yours', canary: 'a planted value', secret: 'a secret' }[c.kind]}: ${c.label}.`)
  if ((a.secret_files ?? []).length > 0) out.push(`Includes files that look like passwords or keys: ${(a.secret_files ?? []).join(', ')}.`)
  return out
})

const note = computed(() => {
  const a = props.item.action
  if (a === undefined || outcome(a) !== null) return ''
  return a.needs_real_answer === true
    ? 'The agent was given a made-up answer for this. Nothing has been sent.'
    : 'The agent was told this worked. Nothing has happened yet.'
})

const code = computed(() => (props.item.action !== undefined ? evidence(props.item.action, props.home) : ''))

// each line of a diff with what kind of line it is
const lines = computed(() => {
  if (props.item.part === undefined || props.text === undefined) return []
  return props.text
    .replace(/\n$/, '')
    .split('\n')
    .map((l) => ({ text: l === '' ? ' ' : l, kind: l.startsWith('@@') ? 'h' : l.startsWith('+') ? 'a' : l.startsWith('-') ? 'd' : '' }))
})

const where = computed(() => {
  const a = props.item.action
  return a !== undefined && a.work_dir !== undefined && a.work_dir !== '' ? `Run in ${tilde(a.work_dir, props.home)}` : ''
})
</script>

<template>
  <div :class="alone ? 'detail alone' : 'detail'">
    <div class="top">
      <div v-if="alone" class="back" @click="emit('close')">
        <Glyph lucide="arrow-left" :size="16" :stroke-width="1.5" />
        <span>Back to the list</span>
      </div>
      <div v-else class="close" title="Close" @click="emit('close')">
        <Glyph lucide="x" :size="16" :stroke-width="1.5" />
      </div>
    </div>
    <h1 class="heading">{{ heading }}</h1>
    <span v-if="fate !== null" :class="`fate ${fate.tone}`">{{ fate.text }}</span>
    <p v-if="reviewer !== null" class="para">
      {{ reviewer.text }} <span class="sec">{{ reviewer.by }}</span>
    </p>
    <p v-if="item.part !== undefined && (item.part.sentence !== undefined || item.part.pending === true)" class="para reserve">
      {{ item.part.sentence ?? 'Reading…' }} <span v-if="item.part.sentence !== undefined" class="sec">Written by an AI model on this computer.</span>
    </p>
    <p v-if="item.part !== undefined" class="para sec">
      {{ item.second }}. Sorted by the built-in rules, with no AI.{{ item.part.raised === true ? ' The rules said to skim it. The model said to read it, and a model can only move a part up.' : '' }}
    </p>
    <p v-for="f in flags" :key="f" class="para flag">{{ f }}</p>

    <template v-if="item.claim !== undefined">
      <div class="kv"><span class="k">Claimed</span><span class="v">"{{ item.claim.claim }}"</span></div>
      <div class="kv"><span class="k">Observed</span><span class="v mono">{{ item.claim.command }}</span></div>
      <p class="para sec">Compared by an AI model on this computer, from the agent's last message and the commands it was seen to run.</p>
    </template>

    <div v-if="code !== ''" class="code">
      <span class="mono">{{ code }}</span>
      <span v-if="where !== ''" class="mono sec">{{ where }}</span>
    </div>
    <div v-if="item.part !== undefined" class="diff">
      <div v-if="text === undefined" class="line"><span class="sec">Reading the change…</span></div>
      <div v-else-if="lines.length === 0" class="line"><span class="sec">Nothing to show: the file moved and its contents are the same.</span></div>
      <div v-for="(l, i) in lines" :key="i" :class="`line ${l.kind}`"><span :class="l.kind === 'h' ? 'mono sec' : 'mono'">{{ l.text }}</span></div>
    </div>
    <p v-if="note !== ''" class="para sec">{{ note }}</p>
  </div>
</template>

<style scoped>
/* room for the reviewer's sentence before it arrives, so nothing below moves when it does */
.reserve {
  min-height: var(--s48);
}
.detail {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  padding: var(--s8) var(--s32) var(--s32);
  overflow: auto;
}
.detail.alone {
  padding: var(--s8) var(--s20) var(--s32);
}
.top {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  justify-content: flex-end;
}
.alone .top {
  justify-content: flex-start;
}
.close,
.back {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: center;
  gap: var(--s8);
  min-width: 44px;
  min-height: 44px;
  border-radius: var(--r-row);
  color: var(--sec);
}
.back {
  padding: 0 var(--s12);
}
.close:hover,
.back:hover {
  background: var(--raise);
}
.heading {
  flex-shrink: 0;
  margin: var(--s8) 0 var(--s8);
  max-width: 560px;
  font-size: var(--fs-h1);
  font-weight: 400;
  line-height: 1.25;
  letter-spacing: -0.015em;
}
.fate {
  flex-shrink: 0;
  color: var(--sec);
}
.fate.flag {
  font-weight: 500;
}
.fate.ok {
  color: var(--ok);
}
.fate.medium {
  color: var(--med);
}
.para {
  flex-shrink: 0;
  margin: var(--s12) 0 0;
  max-width: 560px;
}
.sec {
  color: var(--sec);
}
.high {
  color: var(--high);
}
.kv {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  gap: var(--s12);
  padding: var(--s4) 0;
  margin-top: var(--s8);
}
.k {
  flex-shrink: 0;
  width: 96px;
  color: var(--sec);
}
.v {
  flex: 1;
}
.code {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  gap: var(--s8);
  margin-top: var(--s16);
  padding: var(--s12) var(--s16);
  border-radius: var(--r-row);
  background: var(--raise);
}
.diff {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  margin-top: var(--s16);
  padding: var(--s8) 0;
  border-radius: var(--r-row);
  background: var(--raise);
}
.line {
  flex-shrink: 0;
  padding: 0 var(--s16);
}
.line.a {
  background: var(--add);
}
.line.d {
  background: var(--del);
}
.line.h {
  padding-bottom: var(--s4);
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
}
</style>
