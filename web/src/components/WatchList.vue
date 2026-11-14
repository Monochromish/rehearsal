<script setup lang="ts">
import type { Identifier } from '../api'
import IdentifierTag from './IdentifierTag.vue'

defineProps<{ identifiers: Identifier[] }>()
</script>

<template>
  <div v-if="identifiers.length > 0" class="watch">
    <h2 class="heading">What Rehearsal watches for</h2>
    <div class="note">
      A planted value has no reason to leave this computer, so one turning up on a server means your data was sent
      there.
    </div>
    <IdentifierTag v-for="item in identifiers" :key="item.id" :item="item" />
  </div>
</template>

<style scoped>
.watch {
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

.note {
  margin-bottom: 8px;
  font-size: 16px;
  color: var(--secondary);
}
</style>
