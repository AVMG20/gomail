<script setup lang="ts">
// One mailbox's labels that send mail to Junk: when Jev or a rule gives mail one of them, it's
// marked read and moved to Junk. Junk itself is in the list by default.
import { computed } from 'vue'
import { PlusIcon, XIcon } from '@lucide/vue'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import type { Account } from '@/lib/types'
import { useMailboxSettings } from './mailboxSettings'

const props = defineProps<{ account: Account }>()
const mb = useMailboxSettings()
const options = computed(() => mb.junkOptions(props.account.id))
const selected = computed(() => new Set(mb.junkList(props.account.id)))
const chosen = computed(() => options.value.filter((l) => selected.value.has(l.id)))
</script>

<template>
  <div class="flex min-w-0 flex-wrap items-center gap-1.5">
    <span v-for="l in chosen" :key="l.id" class="bg-muted inline-flex h-6 items-center gap-1.5 rounded-full pr-1 pl-2 text-xs">
      <span class="size-2 rounded-full" :style="{ background: l.color }" aria-hidden="true" />
      {{ l.name }}
      <button
        type="button"
        class="hover:bg-background focus-visible:ring-ring flex size-4 items-center justify-center rounded-full focus-visible:ring-2 focus-visible:outline-none"
        :aria-label="`Stop sending ${l.name} to Junk in ${account.email}`"
        @click="mb.setGoesToJunk(account.id, l, false)"
      >
        <XIcon class="size-3" />
      </button>
    </span>
    <span v-if="!chosen.length" class="text-muted-foreground text-xs">None: labeled mail stays where it is.</span>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <button
          type="button"
          class="text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring inline-flex h-6 items-center gap-1 rounded-full border border-dashed px-2 text-xs focus-visible:ring-2 focus-visible:outline-none"
          :aria-label="`Choose the labels that go to Junk in ${account.email}`"
        >
          <PlusIcon class="size-3" aria-hidden="true" /> Label
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="max-h-72 w-52 overflow-y-auto">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Go to Junk</DropdownMenuLabel>
        <DropdownMenuCheckboxItem
          v-for="l in options"
          :key="l.id"
          :model-value="selected.has(l.id)"
          :disabled="l.then === 'trash'"
          @select.prevent
          @update:model-value="(v: boolean) => mb.setGoesToJunk(account.id, l, v)"
        >
          <span class="size-2 rounded-full" :style="{ background: l.color }" aria-hidden="true" /> {{ l.name }}
          <!-- Set to Trash on the label itself (Jev sends those to Junk): changed there, not here. -->
          <span v-if="l.then === 'trash'" class="text-muted-foreground ml-auto text-[11px]">Goes to Trash</span>
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
