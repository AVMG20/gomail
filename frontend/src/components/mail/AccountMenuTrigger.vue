<script setup lang="ts">
import { ChevronDownIcon } from '@lucide/vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import type { Account } from '@/lib/types'

defineProps<{ account: Account }>()
</script>

<template>
  <button
    type="button"
    class="hover:bg-sidebar-accent flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
  >
    <span class="sr-only">Account menu:</span>
    <AccountAvatar :account="account" :size="28" :glyph="false" />
    <span class="min-w-0 flex-1">
      <span class="block truncate text-[13px] font-semibold">{{ account.displayName || account.email }}</span>
      <span class="text-muted-foreground block truncate text-xs">{{ account.email }}</span>
    </span>
    <ChevronDownIcon class="text-muted-foreground size-4 shrink-0" />
  </button>
</template>
