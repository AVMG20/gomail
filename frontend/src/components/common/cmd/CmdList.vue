<script setup lang="ts">
import { ListboxContent } from 'reka-ui'
defineProps<{ class?: string }>()
</script>

<template>
  <ListboxContent :class="['thin-scroll overflow-x-hidden overflow-y-auto p-1 outline-none', $props.class]">
    <slot />
  </ListboxContent>
</template>
