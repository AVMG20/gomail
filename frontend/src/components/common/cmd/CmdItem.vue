<script setup lang="ts">
import { ListboxItem } from 'reka-ui'
import type { AcceptableValue } from 'reka-ui'

defineProps<{ value: AcceptableValue; disabled?: boolean; class?: string }>()
const emit = defineEmits<{ select: [e: CustomEvent] }>()
</script>

<template>
  <ListboxItem
    :value="value"
    :disabled="disabled"
    :class="[
      'data-highlighted:bg-muted data-highlighted:text-foreground relative flex cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 text-sm outline-none select-none data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:shrink-0',
      $props.class,
    ]"
    @select="(e: CustomEvent) => { e.preventDefault(); emit('select', e) }"
  >
    <slot />
  </ListboxItem>
</template>
