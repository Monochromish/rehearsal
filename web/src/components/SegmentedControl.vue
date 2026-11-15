<script setup lang="ts">
// A row of choices of which one is on. It is given its choices as option
// elements, each with a value, and takes v-model like a select does.
import { computed, useSlots } from 'vue'

defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const slots = useSlots()

const choices = computed(() =>
  (slots.default?.() ?? [])
    .filter((node) => node.type === 'option')
    .map((node) => {
      const props: Record<string, unknown> = node.props ?? {}
      return { value: String(props['value'] ?? ''), label: typeof node.children === 'string' ? node.children : '' }
    }),
)
</script>

<template>
  <div class="segmented" role="tablist">
    <button
      v-for="choice in choices"
      :key="choice.value"
      type="button"
      role="tab"
      :aria-selected="choice.value === modelValue"
      :class="choice.value === modelValue ? 'segment on' : 'segment'"
      @click="emit('update:modelValue', choice.value)"
    >
      {{ choice.label }}
    </button>
  </div>
</template>

<style scoped>
.segmented {
  display: inline-flex;
  flex: none;
  align-self: flex-start;
  border: 1px solid var(--line);
  border-radius: var(--r-row);
  overflow: hidden;
}
.segment {
  padding: var(--s4) var(--s12);
  border: 0;
  background: transparent;
  color: var(--sec);
  font: inherit;
  font-size: var(--fs-small);
  cursor: pointer;
}
.segment.on {
  background: var(--raise);
  color: var(--text);
}
.segment:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
</style>
