<script setup lang="ts">
// A card anchored next to a rectangle (a calendar slot or event chip): shadcn's
// Popover on a virtual anchor. It places and flips the card, and closes it on
// Escape or a click outside; a click while a menu from the card is open only
// closes that menu.
import { computed } from 'vue'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'

export interface AnchorRect { left: number; top: number; width: number; height: number }

const props = withDefaults(defineProps<{ anchor: AnchorRect; width?: number; label: string }>(), { width: 340 })
const emit = defineEmits<{ close: []; key: [KeyboardEvent] }>()

const reference = computed(() => {
  const a = props.anchor
  return { getBoundingClientRect: () => new DOMRect(a.left, a.top, a.width, a.height) }
})

// Where focus was before the card opened (an event chip, a slot, a button).
const opener = typeof document !== 'undefined' ? (document.activeElement as HTMLElement | null) : null

// Focus a text field marked autofocus, else the card itself (no stray focus ring on a button).
function onOpenFocus(e: Event) {
  e.preventDefault()
  // The event comes from the focus scope's wrapper, which can't take focus itself.
  const scope = e.target as HTMLElement
  const card = scope.querySelector<HTMLElement>('[data-slot="popover-content"]') ?? scope.closest<HTMLElement>('[data-slot="popover-content"]') ?? scope
  ;(card.querySelector<HTMLElement>('input[autofocus], textarea[autofocus]') ?? card).focus()
}
// Closed with Escape or a button: focus goes back. Not after a click elsewhere,
// which may have opened another card that focus moving away would close.
let clickedOutside = false
const onClickOutside = () => { clickedOutside = true }
function onCloseFocus(e: Event) {
  e.preventDefault()
  const a = document.activeElement
  if (!clickedOutside && opener?.isConnected && opener !== document.body && (!a || a === document.body)) opener.focus({ preventScroll: true })
}
</script>

<template>
  <Popover :open="true" @update:open="(v: boolean) => !v && emit('close')">
    <PopoverAnchor :reference="reference" />
    <PopoverContent
      side="right"
      align="start"
      :side-offset="8"
      :collision-padding="8"
      role="dialog"
      :aria-label="label"
      class="gap-0 rounded-xl p-0 shadow-xl"
      :style="{ width: `${width}px` }"
      @open-auto-focus="onOpenFocus"
      @close-auto-focus="onCloseFocus"
      @pointer-down-outside="onClickOutside"
      @keydown="emit('key', $event)"
    >
      <slot />
    </PopoverContent>
  </Popover>
</template>
