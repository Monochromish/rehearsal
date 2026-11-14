<script setup lang="ts">
import type { Server } from '../api'
import ServerCard from './ServerCard.vue'

defineProps<{
  title: string
  hint: string
  emptyText: string
  servers: Server[]
}>()
</script>

<template>
  <div class="group">
    <h2 class="title">{{ title }}</h2>
    <div class="hint">{{ hint }}</div>
    <div v-if="servers.length === 0" class="empty">{{ emptyText }}</div>
    <ServerCard v-for="server in servers" :key="server.host" :server="server" />
  </div>
</template>

<style scoped>
.group {
  display: flex;
  flex-direction: column;
  margin-bottom: 28px;
}

.title {
  margin: 0;
  font-size: 34px;
  font-weight: 700;
}

.hint {
  font-size: 22px;
  color: var(--muted);
  margin-bottom: 12px;
}

.empty {
  font-size: 22px;
  color: var(--muted);
  padding: 16px 20px;
  border-width: 2px;
  border-style: dashed;
  border-color: var(--border);
  border-radius: 12px;
}
</style>
