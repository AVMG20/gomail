<script setup lang="ts">
import { computed } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, BellIcon, BellOffIcon, CalendarDaysIcon, PlusIcon, RefreshCwIcon, SettingsIcon, LayersIcon } from '@lucide/vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import Tip from '@/components/common/Tip.vue'
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu'
import { useMailboxSettings } from '@/components/settings/mailboxSettings'
import { useDragSort } from '@/composables/useDragSort'
import { hasMethod, isWails } from '@/lib/api'
import type { Account } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const accounts = useAccountsStore()
const mail = useMailStore()
const ui = useUiStore()
const settings = useSettingsStore()
const mb = useMailboxSettings()
// Drag a mailbox up or down to reorder (also changes mod+1…9).
const sort = useDragSort((from, to) => void accounts.moveAccount(from, to).catch(() => {}))
const mockMode = !isWails()

// In the calendar no mailbox is "active": the calendar spans all of them.
const active = computed(() => (ui.mainView === 'calendar' ? null : mail.mailbox.accountId))
const hasCalendar = computed(() => hasMethod('ListCalendars'))

function openMailbox(accountId: string) {
  ui.mainView = 'mail'
  mail.switchAccount(accountId)
}
const totalUnread = computed(() => accounts.unifiedUnread('inbox', settings.mutedMailboxes))

function tipLabel(acc: Account) {
  return `${acc.displayName || acc.email}\n${acc.email}${mb.isMuted(acc.id) ? ' · muted' : ''}`
}

function badge(n: number) {
  return n > 99 ? '99+' : String(n)
}
</script>

<template>
  <nav data-drag class="bg-sidebar border-sidebar-border flex w-14 shrink-0 flex-col items-center gap-1 border-r pt-3 pb-3" aria-label="Accounts">
    <Tip v-if="accounts.accounts.length > 1" label="All inboxes" shortcut="mod+shift+0" side="right">
      <button
        type="button"
        class="group relative flex size-11 items-center justify-center rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :aria-label="totalUnread ? `All inboxes, ${totalUnread} unread` : 'All inboxes'"
        @click="openMailbox('')"
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
        <span v-if="totalUnread" aria-hidden="true" class="bg-foreground text-background absolute -top-0.5 -right-0.5 min-w-4 rounded-full px-1 text-center text-[10px] leading-4 font-semibold">{{ badge(totalUnread) }}</span>
      </button>
    </Tip>
    <div v-if="accounts.accounts.length > 1" class="bg-border my-1 h-px w-6" />

    <ContextMenu v-for="(acc, i) in accounts.accounts" :key="acc.id">
      <ContextMenuTrigger as-child>
        <Tip side="right" :label="tipLabel(acc)" :shortcut="i < 9 ? `mod+${i + 1}` : ''">
          <button
            type="button"
            class="group relative flex size-11 items-center justify-center rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :class="sort.dragIndex.value === i && 'opacity-40'"
            :aria-current="active === acc.id ? 'page' : undefined"
            v-bind="sort.bind(i)"
            @click="openMailbox(acc.id)"
          >
            <span v-if="sort.lineAt(i)" class="bg-primary pointer-events-none absolute left-1.5 right-1.5 h-0.5 rounded-full" :class="sort.lineAt(i) === 'before' ? '-top-0.5' : '-bottom-0.5'" aria-hidden="true" />
            <span
              class="absolute top-1/2 -left-1.5 w-1 -translate-y-1/2 rounded-r-full transition-all"
              :class="active === acc.id ? 'h-6 opacity-100' : 'h-2 opacity-0 group-hover:opacity-50'"
              :style="{ background: acc.color }"
            />
            <span class="sr-only">{{ acc.displayName || acc.email }} ({{ acc.email }}){{ acc.needsReauth ? ', sign-in expired' : mb.isMuted(acc.id) ? ', muted' : acc.unreadCount ? `, ${acc.unreadCount} unread` : '' }}</span>
            <AccountAvatar :account="acc" :size="34" :active="active === acc.id" class="transition-transform group-active:scale-95" />
            <span v-if="acc.needsReauth" class="absolute -top-0.5 -right-0.5 flex size-4 items-center justify-center rounded-full bg-amber-500 text-[10px] font-bold text-white ring-2 ring-sidebar" aria-hidden="true">!</span>
            <span v-else-if="acc.unreadCount && !mb.isMuted(acc.id)" aria-hidden="true" class="absolute -top-0.5 -right-0.5 min-w-4 rounded-full px-1 text-center text-[10px] leading-4 font-semibold text-white ring-2 ring-sidebar" :style="{ background: '#dc2626' }">{{ badge(acc.unreadCount) }}</span>
            <span v-if="accounts.syncing.has(acc.id)" class="absolute -bottom-0.5 left-1/2 h-0.5 w-5 -translate-x-1/2 overflow-hidden rounded-full bg-border">
              <span class="block h-full w-full animate-pulse rounded-full" :style="{ background: acc.color }" />
            </span>
          </button>
        </Tip>
      </ContextMenuTrigger>
      <ContextMenuContent class="w-52">
        <ContextMenuLabel class="text-muted-foreground truncate text-xs font-normal">{{ acc.email }}</ContextMenuLabel>
        <ContextMenuItem v-if="mb.isMuted(acc.id)" @select="mb.setMuted(acc.id, false)"><BellIcon /> Unmute mailbox</ContextMenuItem>
        <ContextMenuItem v-else @select="mb.setMuted(acc.id, true)"><BellOffIcon /> Mute mailbox</ContextMenuItem>
        <ContextMenuItem @select="accounts.sync(acc.id)"><RefreshCwIcon /> Sync now</ContextMenuItem>
        <ContextMenuItem :disabled="i === 0" @select="accounts.moveAccount(i, i - 1)"><ArrowUpIcon /> Move up</ContextMenuItem>
        <ContextMenuItem :disabled="i === accounts.accounts.length - 1" @select="accounts.moveAccount(i, i + 1)"><ArrowDownIcon /> Move down</ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem @select="ui.openSettings(`mailbox:${acc.id}`)"><SettingsIcon /> Mailbox settings…</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>

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
    <Tip v-if="mockMode" label="Mock data: running in a browser without the Go backend" side="right">
      <span class="mb-1 rounded-md border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[9px] font-semibold tracking-wide text-amber-800 uppercase select-none dark:text-amber-400" role="status">Mock</span>
    </Tip>
    <Tip v-if="hasCalendar" :label="ui.mainView === 'calendar' ? 'Back to mail' : 'Calendar'" :shortcut="ui.mainView === 'calendar' ? 'g m' : 'g c'" side="right">
      <button
        type="button"
        :class="['relative mb-0.5 flex size-9 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring', ui.mainView === 'calendar' ? 'bg-sidebar-accent text-foreground' : 'text-muted-foreground hover:text-foreground hover:bg-sidebar-accent']"
        :aria-label="ui.mainView === 'calendar' ? 'Back to mail' : 'Calendar'"
        :aria-pressed="ui.mainView === 'calendar'"
        @click="ui.mainView = ui.mainView === 'calendar' ? 'mail' : 'calendar'"
      >
        <span v-if="ui.mainView === 'calendar'" class="bg-foreground absolute top-1/2 -left-[11px] h-6 w-1 -translate-y-1/2 rounded-r-full" />
        <CalendarDaysIcon class="size-[18px]" />
      </button>
    </Tip>
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
