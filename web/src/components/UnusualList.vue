<script setup lang="ts">
import { computed } from 'vue'
import type { Held, Ledger } from '../api'
import { plainTitle, route } from '../present'

const props = defineProps<{
  ledger: Ledger
  held: Held
  agent: string
}>()

interface Line {
  key: string
  title: string
  route: string
  word: 'Held' | 'Noted'
}

// Held: an action kept back for a decision. Noted: something that already
// happened and is worth knowing, a server outside the model provider that
// received one of your details, or a write to a file that decides what
// runs on this computer.
const lines = computed<Line[]>(() => [
  ...props.ledger.servers
    .filter((s) => s.flagged)
    .map(
      (s): Line => ({
        key: `server-${s.host}`,
        title: `${s.host} received: ${s.received.map((r) => r.label).join(', ')}`,
        route: `${s.requests === 1 ? '1 request' : `${s.requests} requests`}, ${props.agent} -> ${s.host}`,
        word: 'Noted',
      }),
    ),
  ...props.ledger.sensitive.map(
    (w): Line => ({
      key: `write-${w.path}`,
      title: `${props.agent} wrote to ${w.path}`,
      route: w.real ? `${w.reason} This one has already taken effect.` : `${w.reason} It is waiting in the sandbox for your decision.`,
      word: 'Noted',
    }),
  ),
  ...props.held.actions.map(
    (a): Line => ({ key: a.id, title: plainTitle(a), route: route(a, props.agent), word: 'Held' }),
  ),
])
</script>

<template>
  <div v-if="lines.length > 0" class="group">
    <h2 class="heading">Unusual</h2>
    <div v-for="line in lines" :key="line.key" class="row">
      <div class="main">
        <div class="title">{{ line.title }}</div>
        <div class="route">{{ line.route }}</div>
      </div>
      <div :class="line.word === 'Noted' ? 'state state-high' : 'state state-medium'">{{ line.word }}</div>
    </div>
  </div>
</template>

<style scoped>
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
  animation: appear 150ms ease-out;
}

@keyframes appear {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
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
</style>
