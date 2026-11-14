<script setup lang="ts">
import type { Identifier } from '../api'
import IdentifierTag from './IdentifierTag.vue'

defineProps<{ identifiers: Identifier[] }>()
</script>

<template>
  <div class="watch">
    <h2 class="title">What Rehearsal is watching for</h2>
    <div class="hint">
      Your own details, plus fake values planted for this session. A planted value has no reason to
      leave your computer, so seeing one on a server means your data was sent there.
    </div>
    <div class="tags">
      <IdentifierTag v-for="item in identifiers" :key="item.id" :item="item" />
    </div>
  </div>
</template>

<style scoped>
.watch {
  display: flex;
  flex-direction: column;
  padding-top: 20px;
  border-top-width: 2px;
  border-top-style: solid;
  border-top-color: var(--border);
}

.title {
  margin: 0;
  font-size: 28px;
  font-weight: 700;
}

.hint {
  font-size: 20px;
  color: var(--muted);
  margin-bottom: 12px;
}

.tags {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
}
</style>
