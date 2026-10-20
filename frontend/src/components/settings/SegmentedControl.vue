<script setup lang="ts" generic="T extends string">
// A small pill of 2–3 choices (Reading pane: Right / Off). Behaves as a radio group:
// one tab stop, arrow keys move the choice.
import { ref } from 'vue'

const props = defineProps<{ modelValue: T; options: { value: T; label: string }[]; ariaLabel?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const root = ref<HTMLElement | null>(null)

function onKey(e: KeyboardEvent) {
  const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const i = props.options.findIndex((o) => o.value === props.modelValue)
  const next = (i + step + props.options.length) % props.options.length
  emit('update:modelValue', props.options[next].value)
  root.value?.querySelectorAll<HTMLElement>('[role="radio"]')[next]?.focus()
}
</script>

<template>
  <div ref="root" role="radiogroup" :aria-label="ariaLabel" class="bg-muted inline-flex rounded-lg p-0.5" @keydown="onKey">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="modelValue === o.value"
      :tabindex="modelValue === o.value ? 0 : -1"
      :class="['focus-visible:ring-ring rounded-md px-3 py-1 text-xs font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-2', modelValue === o.value ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']"
      @click="emit('update:modelValue', o.value)"
    >{{ o.label }}</button>
  </div>
</template>
