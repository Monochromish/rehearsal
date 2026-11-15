<script setup lang="ts">
// The six icons the screens use, drawn as Lucide draws them. Lucide's
// icons are under the ISC licence (lucide.dev); portions come from Feather,
// under the MIT licence.
withDefaults(defineProps<{ lucide: string; size?: number; strokeWidth?: number }>(), { size: 16, strokeWidth: 2 })

const shapes: Record<string, string[]> = {
  plus: ['M5 12h14', 'M12 5v14'],
  x: ['M18 6 6 18', 'm6 6 12 12'],
  'arrow-left': ['m12 19-7-7 7-7', 'M19 12H5'],
  search: ['M3 11a8 8 0 1 0 16 0a8 8 0 1 0-16 0', 'm21 21-4.3-4.3'],
  shield: ['M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z'],
  folder: ['M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z'],
}
</script>

<template>
  <svg
    class="glyph"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-for="d in shapes[lucide] ?? []" :key="d" :d="d" />
  </svg>
</template>

<style scoped>
.glyph {
  flex: none;
}
</style>
