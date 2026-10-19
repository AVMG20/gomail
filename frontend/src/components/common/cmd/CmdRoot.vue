<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ListboxRoot } from 'reka-ui'

const props = withDefaults(defineProps<{ class?: string; autoHighlight?: boolean }>(), { autoHighlight: true })
const root = ref<unknown>(null)
let mo: MutationObserver | null = null
let raf = 0

// cmdk-style: keep the first item highlighted when the visible items change,
// so Enter always acts on the top result.
function sync() {
  cancelAnimationFrame(raf)
  raf = requestAnimationFrame(() => {
    const r = root.value as unknown as { highlightedElement?: HTMLElement | null; highlightFirstItem?: () => void } | null
    // Items changed (new query / results): start again from the top, like cmdk.
    r?.highlightFirstItem?.()
  })
}

onMounted(() => {
  if (!props.autoHighlight) return
  const el = (root.value as unknown as { $el?: HTMLElement } | null)?.$el
  if (!el || typeof MutationObserver === 'undefined') return
  mo = new MutationObserver(sync)
  mo.observe(el, { childList: true, subtree: true })
  sync()
})
onBeforeUnmount(() => {
  mo?.disconnect()
  cancelAnimationFrame(raf)
})

defineExpose({ highlightFirst: () => (root.value as unknown as { highlightFirstItem?: () => void } | null)?.highlightFirstItem?.() })
</script>

<template>
  <!-- Listbox primitives with our own filtering (shadcn Command filters by text and clears on select). -->
  <ListboxRoot ref="root" :highlight-on-hover="true" :class="['text-popover-foreground flex size-full flex-col overflow-hidden outline-none', props.class]">
    <slot />
  </ListboxRoot>
</template>
