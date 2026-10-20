<script setup lang="ts">
// A small card anchored next to a rectangle (a calendar slot or event chip).
// Placed to the right of the anchor, flipping left/up to stay inside the window.
// Closes on Escape and outside pointer-down; focus moves into the card.
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

export interface AnchorRect { left: number; top: number; width: number; height: number }

const props = withDefaults(defineProps<{ anchor: AnchorRect; width?: number; label: string }>(), { width: 340 })
const emit = defineEmits<{ close: [] }>()
const el = ref<HTMLElement | null>(null)
const pos = ref({ left: 0, top: 0 })

function place() {
  const card = el.value
  if (!card) return
  const gap = 8
  const vw = window.innerWidth
  const vh = window.innerHeight
  const h = card.offsetHeight
  const a = props.anchor
  let left = a.left + a.width + gap
  if (left + props.width > vw - 8) left = a.left - props.width - gap
  if (left < 8) left = Math.min(Math.max(8, a.left), vw - props.width - 8)
  let top = a.top
  if (top + h > vh - 8) top = Math.max(8, vh - h - 8)
  pos.value = { left, top }
}

function onDown(e: PointerEvent) {
  const t = e.target as Element | null
  if (!el.value || el.value.contains(t)) return
  // Popovers/menus opened from inside the card (selects, date pickers) live in portals.
  if (t?.closest('[data-reka-popper-content-wrapper], [role="listbox"], [role="menu"], [role="dialog"]:not([data-floating-card])')) return
  emit('close')
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  }
}

onMounted(async () => {
  await nextTick()
  place()
  // Focus a text field marked autofocus, else the card itself (no stray focus ring on a button).
  const field = el.value?.querySelector<HTMLElement>('input[autofocus], textarea[autofocus]')
  if (field) field.focus()
  else el.value?.focus()
  // Defer so the click that opened the card doesn't close it.
  setTimeout(() => document.addEventListener('pointerdown', onDown, true), 0)
  window.addEventListener('resize', place)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDown, true)
  window.removeEventListener('resize', place)
})
watch(() => props.anchor, () => nextTick(place), { deep: true })
defineExpose({ place })
</script>

<template>
  <Teleport to="body">
    <div
      ref="el"
      role="dialog"
      data-floating-card
      tabindex="-1"
      :aria-label="label"
      class="bg-popover text-popover-foreground border-border animate-in fade-in-0 zoom-in-95 fixed z-50 rounded-xl border shadow-xl outline-none"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px`, width: `${width}px` }"
      @keydown="onKey"
    >
      <slot />
    </div>
  </Teleport>
</template>
