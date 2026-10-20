<script setup lang="ts">
import { BellIcon, BellOffIcon, PaletteIcon, PencilIcon, RefreshCwIcon, SparklesIcon, Trash2Icon } from '@lucide/vue'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { Account } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useUiStore } from '@/stores/ui'
import { useMailboxSettings } from '@/components/settings/mailboxSettings'
import AccountMenuTrigger from './AccountMenuTrigger.vue'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ labelAll: [] }>()
const accounts = useAccountsStore()
const ui = useUiStore()
const mb = useMailboxSettings()

/** Settings → this mailbox's page, at its name, color or remove confirmation. */
function open(field: 'name' | 'color' | 'remove') {
  ui.focusAccount = { id: props.account.id, field }
  ui.openSettings('accounts')
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <AccountMenuTrigger :account="account" />
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="w-56">
      <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">{{ account.email }}</DropdownMenuLabel>
      <DropdownMenuItem @click="accounts.sync(account.id)"><RefreshCwIcon /> Sync now</DropdownMenuItem>
      <DropdownMenuItem v-if="mb.isMuted(account.id)" @click="mb.setMuted(account.id, false)"><BellIcon /> Unmute mailbox</DropdownMenuItem>
      <DropdownMenuItem v-else @click="mb.setMuted(account.id, true)"><BellOffIcon /> Mute mailbox</DropdownMenuItem>
      <DropdownMenuItem @click="open('name')"><PencilIcon /> Rename…</DropdownMenuItem>
      <DropdownMenuItem @click="open('color')"><PaletteIcon /> Change color…</DropdownMenuItem>
      <DropdownMenuItem @click="emit('labelAll')"><SparklesIcon /> Label all mail with AI…</DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem variant="destructive" @click="open('remove')"><Trash2Icon /> Remove account…</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
