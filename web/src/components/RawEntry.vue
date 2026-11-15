<script setup lang="ts">
// One entry of a record as it was stored, set out one field to a line so
// that it can be read: its number, type and time, each field of its data
// with the one that supports the statement marked, then its hash. Nothing
// is left out and nothing is reworded.
import { computed } from 'vue'

const props = defineProps<{
  cite: string
  type: string
  time: string
  // the entry's data exactly as stored
  data: string
  hash: string
  prev?: string
  // text inside the stored data that supports what is being said
  mark?: string
  problem?: string
}>()

interface Field {
  before: string
  marked: string
  after: string
}

/** A field as it is shown, cut at the part that supports the statement. */
function cut(shown: string, compact: string, mark: string): Field {
  if (mark === '') return { before: shown, marked: '', after: '' }
  for (const m of [mark, mark.replace('":', '": ')]) {
    const i = shown.indexOf(m)
    if (i >= 0) return { before: shown.slice(0, i), marked: m, after: shown.slice(i + m.length) }
  }
  // the mark runs over the whole field, or over more than this field
  return compact.includes(mark) || mark.includes(compact) ? { before: '', marked: shown, after: '' } : { before: shown, marked: '', after: '' }
}

const fields = computed<Field[] | null>(() => {
  let parsed: unknown
  try {
    parsed = JSON.parse(props.data)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null
  const all = Object.entries(parsed)
  return all.map(([key, value], i) => {
    const text = JSON.stringify(value)
    const comma = i < all.length - 1 ? ',' : ''
    return cut(`${JSON.stringify(key)}: ${text}${comma}`, `${JSON.stringify(key)}:${text}`, props.mark ?? '')
  })
})
</script>

<template>
  <div class="entry">
    <div class="head">
      <span class="mono strong">{{ cite }}</span>
      <span class="mono grow">{{ type }}</span>
      <span class="mono dim">{{ time.slice(0, 19) }}Z</span>
    </div>
    <template v-if="fields !== null">
      <span class="mono brace">{</span>
      <span v-for="(f, i) in fields" :key="i" class="mono field"
        >{{ f.before }}<strong>{{ f.marked }}</strong
        >{{ f.after }}</span
      >
      <span class="mono brace">}</span>
    </template>
    <span v-else class="mono field">{{ data }}</span>
    <span class="mono dim hash">hash {{ hash }}</span>
    <span v-if="prev" class="mono dim">prev {{ prev }}</span>
    <span v-if="problem" class="problem">Cannot be relied on: {{ problem }}.</span>
  </div>
</template>

<style scoped>
/* code, on the tone of a code block */
.entry {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  background: var(--raise);
  border-radius: var(--r-row);
  padding: var(--s12);
  margin-bottom: var(--s8);
}
.head {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s12);
  margin-bottom: var(--s8);
}
.grow {
  flex: 1;
  min-width: 0;
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
  line-height: 1.5;
}
.strong {
  font-weight: 500;
}
.dim {
  color: var(--sec);
}
/* a long value wraps inside its own line, under its name */
.field {
  margin-left: var(--s16);
}
.hash {
  margin-top: var(--s8);
}
/* an entry that fails its check cannot be trusted: that is a risk, and is coloured as one */
.problem {
  font-size: var(--fs-small);
  font-weight: 500;
  color: var(--high);
  margin-top: var(--s8);
}
</style>
