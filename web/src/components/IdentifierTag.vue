<script setup lang="ts">
import { computed } from 'vue'
import type { Identifier } from '../api'

// requests is set only when the tag shows something a server received
const props = defineProps<{ item: Identifier & { requests?: number } }>()

const isCanary = computed(() => props.item.kind === 'canary')
const times = computed(() => {
  const n = props.item.requests
  if (n === undefined) return ''
  return n === 1 ? 'in 1 request' : `in ${n} requests`
})
</script>

<template>
  <div :class="isCanary ? 'tag tag-canary' : 'tag tag-real'">
    <div class="line">
      <div class="badge">{{ isCanary ? 'PLANTED TEST VALUE' : 'YOURS' }}</div>
      <div class="label">{{ item.label }}</div>
    </div>
    <div class="value">{{ item.value }}</div>
    <div v-if="times !== ''" class="times">{{ times }}</div>
  </div>
</template>

<style scoped>
.tag {
  display: flex;
  flex-direction: column;
  margin-right: 10px;
  margin-bottom: 10px;
  padding: 8px 14px;
  border-width: 3px;
  border-radius: 10px;
}

.tag-real {
  border-style: solid;
  border-color: var(--real-border);
  background-color: var(--real-bg);
  color: var(--real-text);
}

.tag-canary {
  border-style: dashed;
  border-color: var(--canary-border);
  background-color: var(--canary-bg);
  color: var(--canary-text);
}

.line {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: baseline;
}

.badge {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 1px;
  margin-right: 10px;
}

.label {
  font-size: 22px;
  font-weight: 600;
}

.value {
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 18px;
  overflow-wrap: anywhere;
}

.times {
  font-size: 18px;
}
</style>
