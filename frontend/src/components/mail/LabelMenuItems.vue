<script setup lang="ts">
// Shared label checklist for dropdown/context sub-menus.
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { useSettingsStore } from '@/stores/settings'

defineProps<{ active?: string[] }>()
defineEmits<{ pick: [labelId: string] }>()
const settings = useSettingsStore()
</script>

<template>
  <DropdownMenuItem v-for="l in settings.labels" :key="l.id" @click="$emit('pick', l.id)">
    <span class="size-2.5 rounded-full" :style="{ background: l.color }" />
    <span class="flex-1 truncate">{{ l.name }}</span>
  </DropdownMenuItem>
  <DropdownMenuItem v-if="!settings.labels.length" disabled>No labels yet</DropdownMenuItem>
</template>
