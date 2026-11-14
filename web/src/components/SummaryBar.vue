<script setup lang="ts">
import type { Totals } from '../api'

defineProps<{ totals: Totals }>()
</script>

<template>
  <div class="bar">
    <div class="tile">
      <div class="number">{{ totals.servers }}</div>
      <div class="label">servers contacted</div>
    </div>
    <div class="tile">
      <div class="number">{{ totals.requests }}</div>
      <div class="label">requests sent</div>
    </div>
    <div class="tile">
      <div class="number">{{ totals.identifiers_sent }}</div>
      <div class="label">times a server got one of your details</div>
    </div>
    <div :class="totals.flagged_servers > 0 ? 'tile tile-flag' : 'tile'">
      <div class="number">{{ totals.flagged_servers }}</div>
      <div class="label">unexpected servers that got your details</div>
    </div>
  </div>
</template>

<style scoped>
.bar {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.tile {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  flex-basis: 200px;
  box-sizing: border-box;
  margin-right: 12px;
  margin-bottom: 12px;
  padding: 16px 20px;
  border-width: 2px;
  border-style: solid;
  border-color: var(--border);
  border-radius: 12px;
  background-color: var(--surface);
}

.tile-flag {
  border-color: var(--flag-border);
  background-color: var(--flag-bg);
  color: var(--flag-text);
}

.number {
  font-size: 60px;
  font-weight: 700;
  line-height: 1.1;
}

.label {
  font-size: 22px;
}
</style>
