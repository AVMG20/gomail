<script setup lang="ts">
// One mailbox's quiet labels: new mail with these labels arrives without a notification.
// Junk never notifies, so it's shown as a fixed chip.
import { PlusIcon, XIcon } from '@lucide/vue'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import type { Account } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'
import { useMailboxSettings } from './mailboxSettings'

defineProps<{ account: Account }>()
const settings = useSettingsStore()
const mb = useMailboxSettings()
const label = (id: string) => settings.labelMap.get(id)
</script>

<template>
  <div class="flex min-w-0 flex-wrap items-center gap-1.5">
    <span class="bg-muted text-muted-foreground inline-flex h-6 items-center gap-1.5 rounded-full px-2 text-xs" title="Junk never notifies">
      <span class="size-2 rounded-full" :style="{ background: mb.junkLabel.value.color }" aria-hidden="true" />
      {{ mb.junkLabel.value.name }}
    </span>
    <span v-for="id in mb.quietList(account.id)" :key="id" class="bg-muted inline-flex h-6 items-center gap-1.5 rounded-full pr-1 pl-2 text-xs">
      <span class="size-2 rounded-full" :style="{ background: label(id)?.color }" aria-hidden="true" />
      {{ label(id)?.name }}
      <button
        type="button"
        class="hover:bg-background focus-visible:ring-ring flex size-4 items-center justify-center rounded-full focus-visible:ring-2 focus-visible:outline-none"
        :aria-label="`Notify again for ${label(id)?.name} in ${account.email}`"
        @click="mb.setQuiet(account.id, id, false)"
      >
        <XIcon class="size-3" />
      </button>
    </span>
    <DropdownMenu v-if="mb.quietOptions(account.id).length">
      <DropdownMenuTrigger as-child>
        <button
          type="button"
          class="text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring inline-flex h-6 items-center gap-1 rounded-full border border-dashed px-2 text-xs focus-visible:ring-2 focus-visible:outline-none"
          :aria-label="`Add a quiet label for ${account.email}`"
        >
          <PlusIcon class="size-3" aria-hidden="true" /> Label
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="max-h-72 w-52 overflow-y-auto">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Arrive quietly</DropdownMenuLabel>
        <DropdownMenuCheckboxItem
          v-for="l in mb.quietOptions(account.id)"
          :key="l.id"
          :model-value="mb.quietList(account.id).includes(l.id)"
          @select.prevent
          @update:model-value="(v: boolean) => mb.setQuiet(account.id, l.id, v)"
        >
          <span class="size-2 rounded-full" :style="{ background: l.color }" aria-hidden="true" /> {{ l.name }}
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
