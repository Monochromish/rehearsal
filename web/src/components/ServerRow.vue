<script setup lang="ts">
import type { Server } from '../api'
import IdentifierTag from './IdentifierTag.vue'

defineProps<{ server: Server }>()
</script>

<template>
  <div class="row">
    <div class="main">
      <div class="host">{{ server.host }}</div>
      <div v-if="server.received.length === 0" class="nothing">Received none of your details.</div>
      <IdentifierTag v-for="item in server.received" :key="item.id" :item="item" />
    </div>
    <div class="count">{{ server.requests === 1 ? '1 request' : `${server.requests} requests` }}</div>
    <div :class="server.flagged ? 'state state-noted' : 'state'">{{ server.flagged ? 'Noted' : 'Normal' }}</div>
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

.main {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  min-width: 0;
}

.host {
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 16px;
  overflow-wrap: anywhere;
}

.nothing {
  font-size: 16px;
  color: var(--secondary);
}

.count {
  flex-shrink: 0;
  width: 112px;
  margin-left: 16px;
  font-size: 16px;
  color: var(--secondary);
  text-align: right;
}

.state {
  flex-shrink: 0;
  width: 72px;
  margin-left: 16px;
  font-size: 16px;
  font-weight: 600;
  color: var(--secondary);
  text-align: right;
}

.state-noted {
  color: var(--text);
}
</style>
