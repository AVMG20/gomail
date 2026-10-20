<script setup lang="ts">
// Shared label checklist for dropdown/context sub-menus: the labels of the given mailboxes
// (a mixed selection offers the labels of any of them).
import { computed } from 'vue'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { useSettingsStore } from '@/stores/settings'

const props = defineProps<{ active?: string[]; accountIds?: string[] }>()
defineEmits<{ pick: [labelId: string] }>()
const settings = useSettingsStore()
const labels = computed(() => settings.labelsForAccounts(props.accountIds ?? []))
</script>

<template>
  <DropdownMenuItem v-for="l in labels" :key="l.id" @click="$emit('pick', l.id)">
    <span class="size-2.5 rounded-full" :style="{ background: l.color }" />
    <span class="flex-1 truncate">{{ l.name }}</span>
  </DropdownMenuItem>
  <DropdownMenuItem v-if="!labels.length" disabled>No labels yet</DropdownMenuItem>
</template>
