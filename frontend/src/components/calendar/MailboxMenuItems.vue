<script setup lang="ts">
// The mailbox actions of the calendar sidebar, rendered inside either the
// right-click ContextMenu or the hover "⋯" DropdownMenu (same items, same order).
import { computed } from 'vue'
import { BellIcon, BellOffIcon, CalendarCheckIcon, CalendarOffIcon, EyeIcon, RefreshCwIcon, SettingsIcon } from '@lucide/vue'
import { ContextMenuItem, ContextMenuSeparator } from '@/components/ui/context-menu'
import { DropdownMenuItem, DropdownMenuSeparator } from '@/components/ui/dropdown-menu'
import type { Account } from '@/lib/types'
import { useCalendarStore } from '@/stores/calendar'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{ account: Account; kind: 'context' | 'dropdown' }>()
const cal = useCalendarStore()
const ui = useUiStore()
const Item = computed(() => (props.kind === 'context' ? ContextMenuItem : DropdownMenuItem))
const Sep = computed(() => (props.kind === 'context' ? ContextMenuSeparator : DropdownMenuSeparator))
const enabled = computed(() => props.account.calendarEnabled !== false)
const muted = computed(() => !!props.account.calendarMuted)
</script>

<template>
  <component :is="Item" v-if="enabled" @select="cal.setAccountEnabled(account.id, false)"><CalendarOffIcon /> Disable calendar</component>
  <component :is="Item" v-else @select="cal.setAccountEnabled(account.id, true)"><CalendarCheckIcon /> Enable calendar</component>
  <component :is="Item" :disabled="!enabled" @select="cal.setAccountMuted(account.id, !muted)">
    <template v-if="muted"><BellIcon /> Unmute notifications</template>
    <template v-else><BellOffIcon /> Mute notifications</template>
  </component>
  <component :is="Sep" />
  <component :is="Item" :disabled="!enabled" @select="cal.soloAccount(account.id)"><EyeIcon /> Show only this mailbox</component>
  <component :is="Item" :disabled="!enabled" @select="cal.syncAccount(account.id)"><RefreshCwIcon /> Sync now</component>
  <component :is="Sep" />
  <component :is="Item" @select="ui.openSettings('calendar')"><SettingsIcon /> Mailbox settings…</component>
</template>
