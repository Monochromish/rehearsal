<script setup lang="ts">
import type { Identifier } from '../api'

// requests is set only when the line is about something a server received
defineProps<{ item: Identifier & { requests?: number } }>()
</script>

<template>
  <div class="item">
    <span class="label">{{ item.label }}</span>
    <span class="kind">{{ item.kind === 'canary' ? 'planted by Rehearsal' : 'yours' }}</span>
    <span class="value">{{ item.value }}</span>
    <span v-if="item.requests !== undefined" class="kind">
      {{ item.requests === 1 ? 'in 1 request' : `in ${item.requests} requests` }}
    </span>
  </div>
</template>

<style scoped>
.item {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: baseline;
  font-size: 16px;
}

.label {
  margin-right: 8px;
}

.kind {
  margin-right: 8px;
  color: var(--secondary);
}

.value {
  margin-right: 8px;
  font-family: ui-monospace, 'Cascadia Mono', 'SF Mono', Menlo, Consolas, monospace;
  font-size: 14px;
  color: var(--secondary);
  overflow-wrap: anywhere;
}
</style>
