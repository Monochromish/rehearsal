<script setup lang="ts">
defineProps<{
  // tool name, wrapped command and what the session is doing
  meta: string
  // the one sentence that says where things stand
  status: string
}>()
</script>

<template>
  <div class="summary">
    <div class="meta">{{ meta }}</div>
    <h1 class="status">{{ status }}</h1>
  </div>
</template>

<style scoped>
.summary {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
}

.meta {
  margin-bottom: 8px;
  font-size: 14px;
  color: var(--secondary);
}

.status {
  margin: 0;
  font-size: 32px;
  font-weight: 600;
  line-height: 1.2;
}
</style>
