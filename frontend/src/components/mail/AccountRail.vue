<script setup lang="ts">
import { computed } from 'vue'
import { PlusIcon, SettingsIcon, LayersIcon } from '@lucide/vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import Tip from '@/components/common/Tip.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'

const accounts = useAccountsStore()
const mail = useMailStore()
const ui = useUiStore()

const active = computed(() => mail.mailbox.accountId)
const totalUnread = computed(() => accounts.unifiedUnread('inbox'))

function badge(n: number) {
  return n > 99 ? '99+' : String(n)
}
</script>

<template>
  <nav class="bg-sidebar border-sidebar-border flex w-14 shrink-0 flex-col items-center gap-1 border-r py-3" aria-label="Accounts">
    <Tip v-if="accounts.accounts.length > 1" label="All inboxes" shortcut="mod+0" side="right">
      <button
        type="button"
        class="group relative flex size-11 items-center justify-center rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="All inboxes"
        @click="mail.switchAccount('')"
      >
        <span
          class="absolute top-1/2 -left-1.5 w-1 -translate-y-1/2 rounded-r-full bg-foreground transition-all"
          :class="active === '' ? 'h-6 opacity-100' : 'h-2 opacity-0 group-hover:opacity-40'"
        />
        <span
          class="text-muted-foreground flex size-9 items-center justify-center rounded-full border border-dashed transition-colors"
          :class="active === '' ? 'border-foreground/40 text-foreground bg-background' : 'border-border group-hover:text-foreground'"
        >
          <LayersIcon class="size-4" />
        </span>
        <span v-if="totalUnread" class="bg-foreground text-background absolute -top-0.5 -right-0.5 min-w-4 rounded-full px-1 text-center text-[10px] leading-4 font-semibold">{{ badge(totalUnread) }}</span>
      </button>
    </Tip>
    <div v-if="accounts.accounts.length > 1" class="bg-border my-1 h-px w-6" />

    <Tip v-for="(acc, i) in accounts.accounts" :key="acc.id" side="right" :shortcut="i < 9 ? `mod+${i + 1}` : ''">
      <template #content>
        <span class="flex flex-col">
          <span class="font-medium">{{ acc.displayName || acc.email }}</span>
          <span class="opacity-70">{{ acc.email }}</span>
        </span>
      </template>
      <button
        type="button"
        class="group relative flex size-11 items-center justify-center rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :aria-label="`${acc.displayName} (${acc.email})`"
        :aria-current="active === acc.id ? 'page' : undefined"
        @click="mail.switchAccount(acc.id)"
      >
        <span
          class="absolute top-1/2 -left-1.5 w-1 -translate-y-1/2 rounded-r-full transition-all"
          :class="active === acc.id ? 'h-6 opacity-100' : 'h-2 opacity-0 group-hover:opacity-50'"
          :style="{ background: acc.color }"
        />
        <AccountAvatar :account="acc" :size="34" :active="active === acc.id" class="transition-transform group-active:scale-95" />
        <span v-if="acc.unreadCount" class="absolute -top-0.5 -right-0.5 min-w-4 rounded-full px-1 text-center text-[10px] leading-4 font-semibold text-white ring-2 ring-sidebar" :style="{ background: '#ef4444' }">{{ badge(acc.unreadCount) }}</span>
        <span v-if="accounts.syncing.has(acc.id)" class="absolute -bottom-0.5 left-1/2 h-0.5 w-5 -translate-x-1/2 overflow-hidden rounded-full bg-border">
          <span class="block h-full w-full animate-pulse rounded-full" :style="{ background: acc.color }" />
        </span>
      </button>
    </Tip>

    <Tip label="Add account" side="right">
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent mt-1 flex size-9 items-center justify-center rounded-full border border-dashed transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="Add account"
        @click="ui.addAccountOpen = true"
      >
        <PlusIcon class="size-4" />
      </button>
    </Tip>

    <div class="flex-1" />
    <Tip label="Settings" shortcut="mod+," side="right">
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent flex size-9 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="Settings"
        @click="ui.openSettings()"
      >
        <SettingsIcon class="size-[18px]" />
      </button>
    </Tip>
  </nav>
</template>
