<script setup lang="ts">
import type { Server } from '../api'
import ServerRow from './ServerRow.vue'

defineProps<{
  title: string
  servers: Server[]
}>()
</script>

<template>
  <div v-if="servers.length > 0" class="group">
    <h2 class="heading">{{ title }}</h2>
    <ServerRow v-for="server in servers" :key="server.host" :server="server" />
  </div>
</template>

<style scoped>
.group {
  display: flex;
  flex-direction: column;
  margin-bottom: 24px;
}

.heading {
  margin: 0;
  margin-bottom: 8px;
  font-size: 18px;
  font-weight: 600;
}
</style>
