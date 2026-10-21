<script setup lang="ts">
// Settings has two parts: "App" pages for settings that apply everywhere, and "Mailboxes":
// the mailbox list, labels & rules, and one page per mailbox with everything set per mailbox.
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { BellIcon, CalendarDaysIcon, RefreshCwIcon, InfoIcon, MailIcon, PaletteIcon, PlusIcon, SlidersHorizontalIcon, SparklesIcon, UsersIcon, XIcon, CheckIcon, LoaderIcon } from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import type { Account } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore, type SettingsTab } from '@/stores/ui'
import AboutTab from './AboutTab.vue'
import AccountsTab from './AccountsTab.vue'
import AiTab from './AiTab.vue'
import AppearanceTab from './AppearanceTab.vue'
import CalendarTab from './CalendarTab.vue'
import { hasMethod } from '@/lib/api'
import GeneralTab from './GeneralTab.vue'
import MailboxPage from './MailboxPage.vue'
import MailTab from './MailTab.vue'
import NotificationsTab from './NotificationsTab.vue'
import SyncTab from './SyncTab.vue'
import ErrorBoundary from '@/components/common/ErrorBoundary.vue'

const ui = useUiStore()
const settings = useSettingsStore()
const accounts = useAccountsStore()

interface Page { id: SettingsTab; label: string; icon?: Component; account?: Account; comp: Component; props?: Record<string, unknown> }

const appPages: Page[] = [
  { id: 'general', label: 'General', icon: SlidersHorizontalIcon, comp: GeneralTab },
  { id: 'appearance', label: 'Appearance', icon: PaletteIcon, comp: AppearanceTab },
  { id: 'mail', label: 'Mail', icon: MailIcon, comp: MailTab },
  { id: 'notifications', label: 'Notifications', icon: BellIcon, comp: NotificationsTab },
  ...(hasMethod('ListCalendars') ? [{ id: 'calendar' as SettingsTab, label: 'Calendar', icon: CalendarDaysIcon, comp: CalendarTab }] : []),
  { id: 'ai', label: 'AI', icon: SparklesIcon, comp: AiTab },
  ...(hasMethod('GetDeviceSyncStatus') ? [{ id: 'sync' as SettingsTab, label: 'Sync', icon: RefreshCwIcon, comp: SyncTab }] : []),
  { id: 'about', label: 'About', icon: InfoIcon, comp: AboutTab },
]
const mailboxPages = computed<Page[]>(() => [
  { id: 'accounts', label: 'All mailboxes', icon: UsersIcon, comp: AccountsTab },
  ...accounts.accounts.map((a) => ({ id: `mailbox:${a.id}` as SettingsTab, label: a.displayName || a.email, account: a, comp: MailboxPage, props: { accountId: a.id } })),
])
const groups = computed(() => [
  { id: 'app', label: 'App', pages: appPages },
  { id: 'mailboxes', label: 'Mailboxes', pages: mailboxPages.value },
])
const pages = computed(() => [...appPages, ...mailboxPages.value])
// A removed mailbox's page falls back to the mailbox list.
const current = computed(() => pages.value.find((t) => t.id === ui.settingsTab) ?? (ui.settingsTab.startsWith('mailbox:') ? mailboxPages.value[0] : appPages[0]))

const scrolled = ref(false)
const panel = ref<HTMLElement | null>(null)
const nav = ref<HTMLElement | null>(null)

// A new page starts at its top.
watch(() => current.value.id, () => {
  scrolled.value = false
  if (panel.value) panel.value.scrollTop = 0
})

/** ↑/↓ (and Home/End) move between pages across both groups, like arrow keys in a sidebar. */
function onTabKey(e: KeyboardEvent) {
  const list = pages.value
  const i = list.findIndex((t) => t.id === current.value.id)
  const next = e.key === 'ArrowDown' ? i + 1 : e.key === 'ArrowUp' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : null
  if (next === null) return
  e.preventDefault()
  const j = (next + list.length) % list.length
  ui.settingsTab = list[j].id
  void nextTick(() => nav.value?.querySelector<HTMLElement>(`[data-settings-tab="${CSS.escape(list[j].id)}"]`)?.focus())
}
const tabDomId = (id: string) => `settings-tab-${id.replace(/[^\w-]/g, '_')}`
const panelDomId = (id: string) => `settings-panel-${id.replace(/[^\w-]/g, '_')}`
</script>

<template>
  <Dialog v-model:open="ui.settingsOpen">
    <DialogContent class="flex h-[min(800px,calc(100vh-2.5rem))] max-h-[calc(100vh-2.5rem)] gap-0 overflow-hidden p-0 sm:max-w-[min(980px,calc(100vw-2.5rem))]" :show-close-button="false">
      <DialogTitle class="sr-only">Settings</DialogTitle>
      <DialogDescription class="sr-only">Configure gomail. Use the up and down arrow keys to move between pages.</DialogDescription>
      <nav ref="nav" class="bg-sidebar border-border thin-scroll flex w-52 shrink-0 flex-col overflow-y-auto border-r p-3" aria-label="Settings" @keydown="onTabKey">
        <template v-for="g in groups" :key="g.id">
          <div :id="`settings-group-${g.id}`" class="text-muted-foreground px-2 pt-1 pb-1.5 text-xs font-medium" :class="g.id !== 'app' && 'mt-4'">{{ g.label }}</div>
          <div role="tablist" aria-orientation="vertical" :aria-labelledby="`settings-group-${g.id}`" class="flex flex-col gap-0.5">
            <button
              v-for="t in g.pages"
              :id="tabDomId(t.id)"
              :key="t.id"
              type="button"
              role="tab"
              :data-settings-tab="t.id"
              :aria-selected="current.id === t.id"
              :aria-controls="panelDomId(t.id)"
              :tabindex="current.id === t.id ? 0 : -1"
              :title="t.account?.email"
              :class="['flex h-8 min-w-0 items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring', current.id === t.id ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60']"
              @click="ui.settingsTab = t.id"
            >
              <AccountAvatar v-if="t.account" :account="t.account" :size="16" :glyph="false" />
              <component :is="t.icon" v-else class="size-4 shrink-0" aria-hidden="true" />
              <span class="min-w-0 flex-1 truncate text-left">{{ t.label }}</span>
              <span v-if="t.account?.needsReauth" class="size-1.5 shrink-0 rounded-full bg-amber-500" aria-hidden="true" />
              <span v-if="t.account?.needsReauth" class="sr-only">, sign-in expired</span>
            </button>
          </div>
        </template>
        <button
          type="button"
          class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent/60 focus-visible:ring-ring mt-0.5 flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2"
          @click="ui.addAccountOpen = true"
        >
          <PlusIcon class="size-4" aria-hidden="true" /> Add account
        </button>
        <div class="min-h-4 flex-1" />
        <div class="text-muted-foreground flex h-6 shrink-0 items-center gap-1.5 px-2 text-[11px]" aria-live="polite">
          <template v-if="settings.saving"><LoaderIcon class="size-3 animate-spin" aria-hidden="true" /> Saving…</template>
          <template v-else><CheckIcon class="size-3" aria-hidden="true" /> Changes save automatically</template>
        </div>
      </nav>
      <div class="flex min-w-0 flex-1 flex-col">
        <header :class="['flex h-14 shrink-0 items-center justify-between gap-4 border-b px-8 transition-colors', scrolled ? 'border-border' : 'border-transparent']">
          <h2 class="min-w-0 truncate text-base font-semibold">{{ current.label }}</h2>
          <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring shrink-0 rounded-md p-1.5 outline-none focus-visible:ring-2" aria-label="Close settings" @click="ui.settingsOpen = false"><XIcon class="size-4" /></button>
        </header>
        <div
          :id="panelDomId(current.id)"
          ref="panel"
          role="tabpanel"
          :aria-labelledby="tabDomId(current.id)"
          class="thin-scroll min-h-0 flex-1 overflow-y-auto px-8 pt-2 pb-10"
          @scroll.passive="(e: Event) => (scrolled = (e.target as HTMLElement).scrollTop > 4)"
        >
          <ErrorBoundary name="settings page" :reset-key="current.id"><KeepAlive><component :is="current.comp" :key="current.id" v-bind="current.props" /></KeepAlive></ErrorBoundary>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
