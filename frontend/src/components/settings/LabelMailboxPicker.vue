<script setup lang="ts">
// Settings → Labels: which mailboxes a label belongs to ("Mailboxes: All / alex@… / 2 mailboxes").
// At least one mailbox stays checked.
import { computed } from 'vue'
import { ChevronDownIcon } from '@lucide/vue'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { labelAccounts, toggleLabelAccount } from '@/lib/labelScope'
import type { Label } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'

const props = defineProps<{ label: Label }>()
const emit = defineEmits<{ change: [accountIds: string[]] }>()
const accounts = useAccountsStore()

const all = computed(() => accounts.accounts.map((a) => a.id))
const chosen = computed(() => labelAccounts(props.label, all.value))
const summary = computed(() => {
  if (chosen.value.length >= all.value.length) return 'All mailboxes'
  if (chosen.value.length === 1) return accounts.accountMap.get(chosen.value[0])?.email ?? '1 mailbox'
  return `${chosen.value.length} mailboxes`
})
function toggle(accountId: string, on: boolean) {
  emit('change', toggleLabelAccount(props.label, accountId, on, all.value))
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring inline-flex h-7 max-w-56 min-w-0 items-center gap-1 rounded-md px-2 text-xs focus-visible:ring-2 focus-visible:outline-none"
        :aria-label="`Mailboxes for ${label.name}: ${summary}`"
      >
        <span class="shrink-0">Mailboxes:</span>
        <span class="text-foreground truncate font-medium">{{ summary }}</span>
        <ChevronDownIcon class="size-3 shrink-0" aria-hidden="true" />
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="w-64">
      <DropdownMenuLabel class="text-muted-foreground text-xs">Use “{{ label.name }}” in</DropdownMenuLabel>
      <DropdownMenuCheckboxItem
        v-for="a in accounts.accounts"
        :key="a.id"
        :model-value="chosen.includes(a.id)"
        :disabled="chosen.length === 1 && chosen[0] === a.id"
        @select.prevent
        @update:model-value="(v: boolean) => toggle(a.id, v)"
      >
        <AccountAvatar :account="a" :size="16" :glyph="false" />
        <span class="truncate" :title="a.email">{{ a.email }}</span>
      </DropdownMenuCheckboxItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
