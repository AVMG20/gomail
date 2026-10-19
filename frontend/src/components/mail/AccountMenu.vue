<script setup lang="ts">
import { PaletteIcon, PencilIcon, RefreshCwIcon, SparklesIcon, Trash2Icon } from '@lucide/vue'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { Account } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import AccountMenuTrigger from './AccountMenuTrigger.vue'

defineProps<{ account: Account }>()
const emit = defineEmits<{ labelAll: [] }>()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <AccountMenuTrigger :account="account" />
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="w-56">
      <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">{{ account.email }}</DropdownMenuLabel>
      <DropdownMenuItem @click="accounts.sync(account.id)"><RefreshCwIcon /> Sync now</DropdownMenuItem>
      <DropdownMenuItem @click="ui.openSettings('accounts')"><PencilIcon /> Rename…</DropdownMenuItem>
      <DropdownMenuItem @click="ui.openSettings('accounts')"><PaletteIcon /> Change color…</DropdownMenuItem>
      <DropdownMenuItem v-if="settings.aiReady" @click="emit('labelAll')"><SparklesIcon /> Label all mail with AI…</DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem variant="destructive" @click="ui.openSettings('accounts')"><Trash2Icon /> Remove account…</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
