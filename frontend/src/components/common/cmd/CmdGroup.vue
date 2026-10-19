<script setup lang="ts">
import { ListboxGroup, ListboxGroupLabel } from 'reka-ui'
defineProps<{ heading?: string }>()
</script>

<template>
  <ListboxGroup class="py-1">
    <ListboxGroupLabel v-if="heading || $slots.heading" class="text-muted-foreground flex items-center px-2 py-1.5 text-[11px] font-medium tracking-wide uppercase">
      <slot name="heading">{{ heading }}</slot>
    </ListboxGroupLabel>
    <slot />
  </ListboxGroup>
</template>
