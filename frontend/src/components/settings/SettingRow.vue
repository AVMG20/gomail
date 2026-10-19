<script setup lang="ts">
import { onMounted, onUpdated, ref, useId } from 'vue'

defineProps<{ title: string; description?: string; stacked?: boolean }>()

// Give the row's control (switch, select, slider, input) the row title as its
// accessible name unless it already has one, so every setting is announced.
const id = useId()
const control = ref<HTMLElement | null>(null)
function label() {
  const root = control.value
  if (!root) return
  const els = root.querySelectorAll<HTMLElement>('button, input, textarea, [role="switch"], [role="combobox"], [role="slider"]')
  els.forEach((el) => {
    if (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby')) return
    if (el.tagName === 'BUTTON' && el.textContent?.trim() && el.getAttribute('role') !== 'combobox' && el.getAttribute('role') !== 'switch') return
    el.setAttribute('aria-labelledby', `${id}-t`)
    if (el.getAttribute('role') === 'combobox') el.setAttribute('aria-labelledby', `${id}-t ${el.id || ''}`.trim())
  })
}
onMounted(label)
onUpdated(label)
</script>

<template>
  <div :class="['py-3.5', stacked ? 'space-y-2.5' : 'flex items-center justify-between gap-6']">
    <div class="min-w-0">
      <div :id="`${id}-t`" class="text-[13px] font-medium">{{ title }}</div>
      <div v-if="description || $slots.description" class="text-muted-foreground mt-0.5 text-xs leading-relaxed"><slot name="description">{{ description }}</slot></div>
    </div>
    <div ref="control" :class="stacked ? '' : 'shrink-0'"><slot /></div>
  </div>
</template>
