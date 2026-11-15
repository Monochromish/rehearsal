<script setup lang="ts">
import type { Item } from '../model'

defineProps<{
  item: Item
  selected: boolean
  // a tick box is shown, and whether it is ticked
  tickable: boolean
  ticked: boolean
  disabled: boolean
}>()

const emit = defineEmits<{ select: [id: string]; tick: [id: string, on: boolean] }>()

function changed(id: string, e: Event): void {
  const box = e.target
  emit('tick', id, box !== null && 'checked' in box && box.checked === true)
}
</script>

<template>
  <div :class="selected ? 'row sel' : 'row'" @click="emit('select', item.id)">
    <div v-if="item.group === 'out'" class="tick">
      <input
        v-if="tickable"
        type="checkbox"
        :checked="ticked"
        :disabled="disabled"
        :title="`Allow: ${item.title}`"
        @change="changed(item.id, $event)"
      />
    </div>
    <div class="body">
      <span :class="item.code ? 't mono' : 't'">{{ item.title }}</span>
      <span :class="`s ${item.tone}`">{{ item.second }}</span>
    </div>
    <span v-if="item.bundle !== undefined" class="edge plain">{{ item.unfolded === true ? 'Hide' : 'Show' }}</span>
    <span v-else :class="`edge ${item.edgeTone} ${item.part !== undefined ? 'mono' : ''}`">{{ item.edge }}</span>
  </div>
</template>

<style scoped>
.row {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  align-items: flex-start;
  gap: var(--s12);
  min-height: 44px;
  padding: var(--s8) var(--s12);
  border-radius: var(--r-row);
}
.row:hover {
  background: var(--raise);
}
.row.sel {
  background: var(--raise);
}
.tick {
  display: flex;
  flex-shrink: 0;
  width: 20px;
  /* the box sits on the first line of text, not the row's centre */
  padding-top: 3px;
}
.body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}
.t {
  color: var(--text);
}
.s {
  font-size: var(--fs-small);
  color: var(--sec);
}
.edge {
  flex-shrink: 0;
  min-width: 72px;
  margin-top: 2px;
  font-size: var(--fs-small);
  color: var(--sec);
  text-align: right;
}
.edge.high {
  color: var(--high);
}
.edge.medium {
  color: var(--med);
}
.edge.ok {
  color: var(--ok);
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
}
.edge.mono {
  font-size: var(--fs-tiny);
}
</style>
