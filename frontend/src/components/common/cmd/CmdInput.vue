<script setup lang="ts">
import { ListboxFilter } from 'reka-ui'
import { SearchIcon } from '@lucide/vue'

defineProps<{ placeholder?: string; autoFocus?: boolean }>()
const model = defineModel<string>({ default: '' })
</script>

<template>
  <div class="border-border flex items-center gap-2 border-b px-3">
    <slot name="icon"><SearchIcon class="text-muted-foreground size-4 shrink-0" /></slot>
    <ListboxFilter
      v-model="model"
      :auto-focus="autoFocus"
      :placeholder="placeholder"
      class="placeholder:text-muted-foreground h-10 w-full min-w-0 bg-transparent text-sm outline-none"
      v-bind="$attrs"
    />
    <slot name="end" />
  </div>
</template>
