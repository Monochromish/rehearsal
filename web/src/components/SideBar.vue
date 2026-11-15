<script setup lang="ts">
import Glyph from './Glyph.vue'
import { computed } from 'vue'
import type { SessionInfo } from '../types'

const props = defineProps<{
  sessions: SessionInfo[]
  // the session being shown
  shown: string
  view: 'session' | 'investigate' | 'policy'
}>()

const emit = defineEmits<{ open: [name: string]; view: [view: 'session' | 'investigate' | 'policy'] }>()

// projects in the order of their newest session, each with its sessions newest first
const projects = computed(() => {
  const out: { name: string; sessions: SessionInfo[] }[] = []
  for (const s of props.sessions) {
    const p = out.find((x) => x.name === s.project)
    if (p === undefined) out.push({ name: s.project, sessions: [s] })
    else p.sessions.push(s)
  }
  return out
})

/** How long ago, in the shortest form that is still clear. */
function ago(time: string): string {
  const minutes = Math.floor((Date.now() - new Date(time).getTime()) / 60000)
  if (Number.isNaN(minutes) || minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  if (minutes < 60 * 24) return `${Math.floor(minutes / 60)}h`
  return `${Math.floor(minutes / (60 * 24))}d`
}

/** A session is named by what was asked in it. The program and the time are for a record that does not say. */
function label(s: SessionInfo): string {
  if (s.title !== undefined && s.title !== '') return s.title
  const at = new Date(s.started_at)
  if (Number.isNaN(at.getTime())) return s.command
  const two = (n: number): string => String(n).padStart(2, '0')
  return `${s.command}, ${two(at.getHours())}:${two(at.getMinutes())}`
}
</script>

<template>
  <div class="side">
    <span class="brand">Rehearsal</span>
    <div class="nav new" title="Run rehearse with an agent in a terminal to start one">
      <div class="marker"></div>
      <Glyph lucide="plus" :size="16" :stroke-width="1.5" />
      <span class="t">New session</span>
    </div>
    <div :class="view === 'investigate' ? 'nav current' : 'nav'" @click="emit('view', 'investigate')">
      <div :class="view === 'investigate' ? 'marker on' : 'marker'"></div>
      <Glyph lucide="search" :size="16" :stroke-width="1.5" />
      <span class="t">Investigate</span>
    </div>
    <div :class="view === 'policy' ? 'nav current' : 'nav'" @click="emit('view', 'policy')">
      <div :class="view === 'policy' ? 'marker on' : 'marker'"></div>
      <Glyph lucide="shield" :size="16" :stroke-width="1.5" />
      <span class="t">Policy</span>
    </div>
    <span class="label">PROJECTS</span>
    <div v-if="sessions.length === 0" class="nav"><span class="t sec">Sessions appear here once one has run.</span></div>
    <template v-for="p in projects" :key="p.name">
      <div class="nav project">
        <div class="marker"></div>
        <Glyph lucide="folder" :size="16" :stroke-width="1.5" />
        <span class="t mono">{{ p.name }}</span>
      </div>
      <div
        v-for="s in p.sessions"
        :key="s.name"
        :class="view === 'session' && s.name === shown ? 'nav session current' : 'nav session'"
        @click="emit('open', s.name)"
      >
        <div :class="view === 'session' && s.name === shown ? 'marker on' : 'marker'"></div>
        <span class="t">{{ label(s) }}</span>
        <span class="when">{{ ago(s.started_at) }}</span>
      </div>
    </template>
  </div>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: var(--side-width);
  padding: var(--s12) var(--s8);
  overflow: auto;
}
.brand {
  flex-shrink: 0;
  margin: var(--s4) var(--s12) var(--s12);
  font-weight: 500;
  letter-spacing: -0.01em;
}
.nav {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s12);
  min-height: 44px;
  padding: 0 var(--s12);
  border-radius: var(--r-row);
}
.nav:hover {
  background: var(--bg);
}
.nav.new {
  margin-bottom: var(--s8);
}
/* the mark of the selected row: 2px of the accent at its leading edge */
.marker {
  flex-shrink: 0;
  width: 2px;
  height: var(--s16);
  margin-left: calc(0px - var(--s8));
  border-radius: 1px;
}
.marker.on {
  background: var(--accent);
}
.nav.current {
  background: var(--bg);
}
.nav.project:hover {
  background: var(--chrome);
}
.nav.project {
  color: var(--sec);
}
.session {
  padding: var(--s8) var(--s12) var(--s8) var(--s40);
  align-items: flex-start;
}
.session .t {
  line-height: 1.35;
}
.session .marker {
  margin-top: var(--s4);
}
.session .when {
  margin-top: 2px;
}
.t {
  flex: 1;
}
.when {
  font-size: var(--fs-small);
  color: var(--sec);
}
.label {
  flex-shrink: 0;
  margin: var(--s20) var(--s12) var(--s8);
  font-size: var(--fs-tiny);
  letter-spacing: 0.06em;
  color: var(--sec);
}
.sec {
  color: var(--sec);
  font-size: var(--fs-small);
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
}
</style>
