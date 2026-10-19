<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ChevronDownIcon, ChevronRightIcon, LayersIcon, PaletteIcon, PencilIcon, RefreshCwIcon, TagIcon, Trash2Icon, PlusIcon, SparklesIcon,
} from '@lucide/vue'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { relativeTime } from '@/lib/format'
import type { Folder } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore, UNIFIED_NAMES } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import { FOLDER_ICONS } from './folderIcons'

const accounts = useAccountsStore()
const mail = useMailStore()
const settings = useSettingsStore()
const ui = useUiStore()
const ai = useAiStore()

const account = computed(() => mail.mailboxAccount)
const moreOpen = ref(false)
const labelsOpen = ref(true)

interface Row { key: string; name: string; kind: string; unread: number; total: number; folderId: string }

const MAIN_KINDS = ['inbox', 'starred', 'sent', 'drafts', 'archive', 'spam']

const rows = computed<Row[]>(() => {
  if (!account.value) {
    return [...MAIN_KINDS, 'trash'].map((kind) => ({
      key: kind, name: UNIFIED_NAMES[kind] ?? kind, kind, folderId: '',
      unread: kind === 'inbox' || kind === 'spam' ? accounts.unifiedUnread(kind) : kind === 'drafts' ? 0 : 0,
      total: 0,
    }))
  }
  const list: Folder[] = accounts.folders[account.value.id] ?? []
  return list.map((f) => ({ key: f.id, name: f.kind === 'spam' ? 'Junk' : f.name, kind: f.kind, unread: f.unread, total: f.total, folderId: f.id }))
})
const mainRows = computed(() => rows.value.filter((r) => MAIN_KINDS.includes(r.kind)))
const customRows = computed(() => rows.value.filter((r) => r.kind === 'custom'))
const trashRow = computed(() => rows.value.find((r) => r.kind === 'trash'))

function isActive(r: Row) {
  const mb = mail.mailbox
  return r.folderId ? mb.folderId === r.folderId : !mb.accountId && mb.kind === r.kind
}

function go(r: Row) {
  mail.setMailbox({ accountId: account.value?.id ?? '', folderId: r.folderId, kind: r.kind })
}

const labelRows = computed(() =>
  settings.labels.map((l) => ({ label: l, count: accounts.labelCounts.get(l.id) })).filter((x) => x.label.source === 'user' || !x.label.source || (x.count?.total ?? 0) > 0),
)

const syncText = computed(() => {
  const list = account.value ? [account.value] : accounts.accounts
  if (list.some((a) => accounts.syncing.has(a.id) || a.syncing)) return 'Syncing…'
  const err = list.find((a) => a.syncError)
  if (err) return `Sync error: ${err.syncError}`
  const times = list.map((a) => a.lastSyncAt).filter(Boolean) as string[]
  if (!times.length) return 'Not synced yet'
  const oldest = times.sort()[0]
  return `Synced ${relativeTime(oldest)}`
})
const syncing = computed(() => (account.value ? accounts.syncing.has(account.value.id) : accounts.anySyncing))

function labelAllWithAi() {
  ai.requestBulk({ query: { accountId: account.value?.id ?? '' }, ids: [], title: account.value ? `All mail in ${account.value.displayName || account.value.email}` : 'All mail' })
}

function formatCount(n: number) {
  return n > 999 ? `${Math.floor(n / 1000)}k` : String(n)
}
</script>

<template>
  <aside class="bg-sidebar text-sidebar-foreground border-sidebar-border flex h-full w-full flex-col border-r" aria-label="Folders">
    <!-- Header -->
    <div class="relative px-3 pt-3 pb-2">
      <div class="absolute inset-x-0 top-0 h-0.5" :style="{ background: account ? 'var(--account)' : 'transparent', opacity: 0.85 }" />
      <DropdownMenu v-if="account">
        <DropdownMenuTrigger as-child>
          <button type="button" class="hover:bg-sidebar-accent flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <AccountAvatar :account="account" :size="28" :glyph="false" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-[13px] font-semibold">{{ account.displayName || account.email }}</span>
              <span class="text-muted-foreground block truncate text-xs">{{ account.email }}</span>
            </span>
            <ChevronDownIcon class="text-muted-foreground size-4 shrink-0" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="w-56">
          <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">{{ account.email }}</DropdownMenuLabel>
          <DropdownMenuItem @click="accounts.sync(account.id)"><RefreshCwIcon /> Sync now</DropdownMenuItem>
          <DropdownMenuItem @click="ui.openSettings('accounts')"><PencilIcon /> Rename…</DropdownMenuItem>
          <DropdownMenuItem @click="ui.openSettings('accounts')"><PaletteIcon /> Change color…</DropdownMenuItem>
          <DropdownMenuItem v-if="settings.aiReady" @click="labelAllWithAi"><SparklesIcon /> Label all mail with AI…</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" @click="ui.openSettings('accounts')"><Trash2Icon /> Remove account…</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <div v-else class="flex items-center gap-2.5 px-2 py-1.5">
        <span class="bg-background text-foreground flex size-7 items-center justify-center rounded-full border"><LayersIcon class="size-3.5" /></span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[13px] font-semibold">All accounts</span>
          <span class="text-muted-foreground block truncate text-xs">{{ accounts.accounts.length }} mailboxes</span>
        </span>
      </div>
    </div>

    <div class="thin-scroll flex-1 overflow-y-auto px-2 pb-2">
      <!-- Folders -->
      <ul class="space-y-px">
        <li v-for="r in mainRows" :key="r.key">
          <button
            type="button"
            :class="[
              'flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
              isActive(r) ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/85 hover:bg-sidebar-accent/60',
            ]"
            @click="go(r)"
          >
            <component :is="FOLDER_ICONS[r.kind] ?? FOLDER_ICONS.custom" class="size-4 shrink-0" :class="isActive(r) ? 'text-[color:var(--account)]' : 'text-muted-foreground'" />
            <span class="flex-1 truncate text-left">{{ r.name }}</span>
            <span v-if="r.unread && r.kind !== 'sent'" class="text-xs tabular-nums" :class="isActive(r) ? 'text-foreground font-semibold' : 'text-muted-foreground font-medium'">{{ formatCount(r.unread) }}</span>
          </button>
        </li>
      </ul>

      <Collapsible v-if="customRows.length" v-model:open="moreOpen" class="mt-1">
        <CollapsibleTrigger as-child>
          <button type="button" class="text-muted-foreground hover:text-foreground flex h-7 w-full items-center gap-1.5 rounded-md px-2.5 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronRightIcon class="size-3.5 transition-transform" :class="moreOpen && 'rotate-90'" />
            More
            <span class="ml-auto tabular-nums">{{ customRows.length }}</span>
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <ul class="space-y-px">
            <li v-for="r in customRows" :key="r.key">
              <button
                type="button"
                :class="[
                  'flex h-8 w-full items-center gap-2.5 rounded-md pr-2.5 pl-5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  isActive(r) ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/85 hover:bg-sidebar-accent/60',
                ]"
                @click="go(r)"
              >
                <component :is="FOLDER_ICONS.custom" class="text-muted-foreground size-4 shrink-0" />
                <span class="flex-1 truncate text-left">{{ r.name }}</span>
                <span v-if="r.unread" class="text-muted-foreground text-xs tabular-nums">{{ formatCount(r.unread) }}</span>
              </button>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>

      <template v-if="trashRow">
        <div class="bg-sidebar-border mx-2.5 my-1.5 h-px" />
        <button
          type="button"
          :class="[
            'flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
            isActive(trashRow) ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/85 hover:bg-sidebar-accent/60',
          ]"
          @click="go(trashRow)"
        >
          <Trash2Icon class="text-muted-foreground size-4 shrink-0" />
          <span class="flex-1 truncate text-left">{{ trashRow.name }}</span>
        </button>
      </template>

      <!-- Labels -->
      <Collapsible v-model:open="labelsOpen" class="mt-4">
        <div class="flex items-center pr-1">
          <CollapsibleTrigger as-child>
            <button type="button" class="text-muted-foreground hover:text-foreground flex h-7 flex-1 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <ChevronRightIcon class="size-3.5 transition-transform" :class="labelsOpen && 'rotate-90'" />
              Labels
            </button>
          </CollapsibleTrigger>
          <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent flex size-6 items-center justify-center rounded-md" aria-label="Manage labels" @click="ui.openSettings('labels')">
            <PlusIcon class="size-3.5" />
          </button>
        </div>
        <CollapsibleContent>
          <ul class="space-y-px">
            <li v-for="{ label, count } in labelRows" :key="label.id">
              <button
                type="button"
                :class="[
                  'flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  mail.labelFilter.labels.includes(label.id) ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/85 hover:bg-sidebar-accent/60',
                ]"
                :aria-pressed="mail.labelFilter.labels.includes(label.id)"
                @click="mail.toggleLabelFilter(label.id)"
              >
                <span class="flex size-4 shrink-0 items-center justify-center">
                  <span class="size-2.5 rounded-full" :style="{ background: label.color, boxShadow: mail.labelFilter.labels.includes(label.id) ? `0 0 0 3px color-mix(in oklab, ${label.color} 25%, transparent)` : undefined }" />
                </span>
                <span class="flex-1 truncate text-left">{{ label.name }}</span>
                <span v-if="count?.unread" class="text-foreground/80 text-xs font-medium tabular-nums">{{ formatCount(count.unread) }}</span>
                <span v-else-if="count?.total" class="text-muted-foreground/70 text-xs tabular-nums">{{ formatCount(count.total) }}</span>
              </button>
            </li>
            <li v-if="!labelRows.length">
              <button type="button" class="text-muted-foreground hover:text-foreground flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px]" @click="ui.openSettings('labels')">
                <TagIcon class="size-4" /> Create a label
              </button>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
    </div>

    <!-- Footer: sync status -->
    <div class="border-sidebar-border flex items-center gap-2 border-t px-3 py-2">
      <span class="size-1.5 shrink-0 rounded-full" :class="syncing ? 'animate-pulse bg-amber-500' : syncText.startsWith('Sync error') ? 'bg-destructive' : 'bg-emerald-500'" />
      <span class="text-muted-foreground min-w-0 flex-1 truncate text-xs" :title="syncText">{{ syncText }}</span>
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent flex size-6 items-center justify-center rounded-md"
        aria-label="Sync now"
        @click="accounts.sync(account?.id ?? '')"
      >
        <RefreshCwIcon class="size-3.5" :class="syncing && 'animate-spin'" />
      </button>
    </div>
  </aside>
</template>
