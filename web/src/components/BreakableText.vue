<script setup lang="ts">
import { computed } from 'vue'

// Text that may hold a long path or address. A line may end after a slash,
// or after the ?, & or = of an address, so it never has to end in the
// middle of a word. With dots, it may also end after a dot or a colon,
// which is where a server's name can be divided.
const props = defineProps<{ text: string; dots?: boolean }>()

const parts = computed(() => props.text.split(props.dots ? /(?<=[/?&=.:])(?=[^/\s.:])/ : /(?<=[/?&=])(?=[^/\s])/))
</script>

<template>
  <span><template v-for="(part, i) in parts" :key="i"><wbr v-if="i > 0" />{{ part }}</template></span>
</template>
