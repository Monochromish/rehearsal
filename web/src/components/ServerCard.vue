<script setup lang="ts">
import type { Server } from '../api'
import IdentifierTag from './IdentifierTag.vue'

defineProps<{ server: Server }>()

function requestCount(n: number): string {
  return n === 1 ? '1 request' : `${n} requests`
}
</script>

<template>
  <div :class="server.flagged ? 'card card-flag' : 'card'">
    <div class="row">
      <div class="host">{{ server.host }}</div>
      <div class="count">{{ requestCount(server.requests) }}</div>
    </div>
    <div v-if="server.flagged" class="warning">
      Check this one: it is not the model provider, and it received your details.
    </div>
    <div v-if="server.received.length === 0" class="nothing">Received none of your details.</div>
    <div v-else class="received">
      <div class="received-label">Received:</div>
      <div class="tags">
        <IdentifierTag v-for="item in server.received" :key="item.id" :item="item" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  margin-bottom: 12px;
  padding: 16px 20px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 12px;
  background-color: var(--surface);
}

.card-flag {
  border-width: 4px;
  border-color: var(--flag-border);
}

.row {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
}

.host {
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 28px;
  font-weight: 700;
  margin-right: 20px;
  overflow-wrap: anywhere;
}

.count {
  font-size: 24px;
  color: var(--muted);
}

.warning {
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  background-color: var(--flag-bg);
  color: var(--flag-text);
  font-size: 22px;
  font-weight: 600;
}

.nothing {
  margin-top: 8px;
  font-size: 22px;
  color: var(--muted);
}

.received {
  display: flex;
  flex-direction: column;
  margin-top: 10px;
}

.received-label {
  font-size: 22px;
  font-weight: 600;
  margin-bottom: 6px;
}

.tags {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
}
</style>
