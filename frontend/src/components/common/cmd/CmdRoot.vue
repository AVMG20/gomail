<script setup lang="ts">
import { ListboxRoot } from 'reka-ui'
defineProps<{ class?: string }>()
</script>

<template>
  <!-- Listbox primitives with our own filtering (shadcn Command filters by text and clears on select). -->
  <ListboxRoot :highlight-on-hover="true" :class="['text-popover-foreground flex size-full flex-col overflow-hidden outline-none', $props.class]">
    <slot />
  </ListboxRoot>
</template>
