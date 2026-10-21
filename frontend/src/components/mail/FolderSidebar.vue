<script setup lang="ts">
import { computed, defineAsyncComponent, h, ref } from 'vue'
import {
  CircleArrowUpIcon, WifiOffIcon, ChevronRightIcon, LayersIcon, RefreshCwIcon, TagIcon, Trash2Icon, PlusIcon,
} from '@lucide/vue'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { api } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import { groupCount, openLabelSettings } from '@/lib/labelScope'
import type { Account, Folder, Label, LabelCount } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore, UNIFIED_NAMES } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { FOLDER_ICONS } from './folderIcons'
import AccountMenuTrigger from './AccountMenuTrigger.vue'
import SidebarLabel from './SidebarLabel.vue'
import FolderMenu from './FolderMenu.vue'

// The dropdown chunk loads lazily; a static copy of the trigger shows meanwhile.
const AccountMenu = defineAsyncComponent({
  loader: () => import('./AccountMenu.vue'),
  delay: 0,
  loadingComponent: { props: ['account'], render(this: { account: Account }) { return h(AccountMenuTrigger, { account: this.account }) } },
})

const accounts = useAccountsStore()
const mail = useMailStore()
const settings = useSettingsStore()
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
// Other folders, minus the ones hidden in the folder's menu or the mailbox's settings.
const hiddenFolders = computed(() => new Set(settings.settings?.hiddenFolders ?? []))
const customRows = computed(() => rows.value.filter((r) => r.kind === 'custom' && !hiddenFolders.value.has(r.folderId)))
const trashRow = computed(() => rows.value.find((r) => r.kind === 'trash'))

function isActive(r: Row) {
  const mb = mail.mailbox
  return r.folderId ? mb.folderId === r.folderId : !mb.accountId && mb.kind === r.kind
}

function go(r: Row) {
  mail.setMailbox({ accountId: account.value?.id ?? '', folderId: r.folderId, kind: r.kind })
}

// Only this mailbox's labels (one per name in the unified view, standing for that label in every
// mailbox). The list is the same in every box (imported labels show when the mailbox uses them
// anywhere); the counts are for the box on screen.
function countOf(l: Label): LabelCount | undefined {
  const c = groupCount(settings.labels, accounts.labelCounts, l)
  return c && { labelId: l.id, ...c }
}
const labelRows = computed(() =>
  settings.labelsForAccount(account.value?.id ?? '')
    .filter((l) => l.source === 'user' || !l.source || (groupCount(settings.labels, accounts.labelTotals, l)?.total ?? 0) > 0)
    .map((l) => ({ label: l, count: countOf(l) })),
)

// Background backfill of older mail: shown as a quiet suffix, never as a progress bar.
const backfill = computed(() => {
  const list = Object.values(accounts.progress).filter((p) => p.phase === 'backfill' && (!account.value || p.accountId === account.value.id))
  if (!list.length) return null
  const done = list.reduce((n, p) => n + p.done, 0)
  const total = list.reduce((n, p) => n + p.total, 0)
  return total ? Math.min(99, Math.round((done / total) * 100)) : 0
})

const syncText = computed(() => {
  const list = account.value ? [account.value] : accounts.accounts
  if (list.some((a) => accounts.syncing.has(a.id) || a.syncing)) return 'Syncing…'
  if (backfill.value !== null) return `Downloading older mail… ${backfill.value}%`
  if (settings.settings?.backfillPaused) return 'Older mail paused'
  const err = list.find((a) => a.syncError)
  if (err) return `Sync error: ${err.syncError}`
  const times = list.map((a) => a.lastSyncAt).filter(Boolean) as string[]
  if (!times.length) return 'Not synced yet'
  const oldest = times.sort()[0]
  return `Synced ${relativeTime(oldest)}`
})
const syncing = computed(() => (account.value ? accounts.syncing.has(account.value.id) : accounts.anySyncing))
const progress = computed(() => {
  const list = Object.values(accounts.progress).filter((p) => p.phase !== 'backfill' && (!account.value || p.accountId === account.value.id))
  if (!list.length) return null
  const done = list.reduce((n, p) => n + p.done, 0)
  const total = list.reduce((n, p) => n + p.total, 0)
  return { pct: total ? Math.min(100, Math.round((done / total) * 100)) : 0, phase: list[0].phase }
})

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
    <div data-drag class="px-3 pt-3 pb-2">
      <AccountMenu v-if="account" :account="account" @label-all="labelAllWithAi" />
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
          <FolderMenu :kind="r.kind" :folder-id="r.folderId" :name="r.name" :unread="r.unread">
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
          </FolderMenu>
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
              <FolderMenu :kind="r.kind" :folder-id="r.folderId" :name="r.name" :unread="r.unread">
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
              </FolderMenu>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>

      <template v-if="trashRow">
        <div class="bg-sidebar-border mx-2.5 my-1.5 h-px" />
        <FolderMenu :kind="trashRow.kind" :folder-id="trashRow.folderId" :name="trashRow.name" :unread="trashRow.unread">
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
        </FolderMenu>
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
          <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent flex size-6 items-center justify-center rounded-md" aria-label="Manage labels" @click="openLabelSettings(account?.id ?? '')">
            <PlusIcon class="size-3.5" />
          </button>
        </div>
        <CollapsibleContent>
          <ul class="space-y-px">
            <SidebarLabel v-for="{ label, count } in labelRows" :key="label.id" :label="label" :count="count" />
            <li v-if="!labelRows.length">
              <button type="button" class="text-muted-foreground hover:text-foreground flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px]" @click="openLabelSettings(account?.id ?? '')">
                <TagIcon class="size-4" /> Create a label
              </button>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
    </div>

    <!-- Footer: sync status -->
    <div v-if="progress" class="px-3 pt-2" role="progressbar" :aria-valuenow="progress.pct" aria-valuemin="0" aria-valuemax="100" :aria-label="`Syncing ${progress.phase}`">
      <div class="text-muted-foreground mb-1 flex justify-between text-[11px]"><span>Syncing {{ progress.phase.toLowerCase() }}…</span><span class="tabular-nums">{{ progress.pct }}%</span></div>
      <div class="bg-sidebar-accent h-1 overflow-hidden rounded-full"><div class="h-full rounded-full transition-[width] duration-300" :style="{ width: `${progress.pct}%`, background: 'var(--account)' }" /></div>
    </div>
    <button
      v-if="settings.updateInfo?.available"
      type="button"
      class="text-primary hover:bg-sidebar-accent mx-2 mb-1 flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs font-medium"
      :title="settings.updateInfo.notes || undefined"
      @click="api.openExternal(settings.updateInfo.url)"
    >
      <CircleArrowUpIcon class="size-3.5 shrink-0" /> <span class="flex-1 truncate">Update available: {{ settings.updateInfo.latest }}</span>
    </button>
    <div class="border-sidebar-border flex items-center gap-2 border-t px-3 py-2">
      <template v-if="!accounts.online">
        <span class="inline-flex items-center gap-1.5 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-800 dark:text-amber-400" role="status"><WifiOffIcon class="size-3" /> Offline</span>
        <span class="flex-1" />
      </template>
      <template v-else>
        <span class="size-1.5 shrink-0 rounded-full" :class="syncing ? 'animate-pulse bg-amber-500' : syncText.startsWith('Sync error') ? 'bg-destructive' : 'bg-emerald-500'" />
        <span class="text-muted-foreground min-w-0 flex-1 truncate text-xs" :title="syncText">{{ syncText }}</span>
        <!-- Pausing belongs where the download shows (e.g. on a slow or metered connection), not in Settings. -->
        <button
          v-if="backfill !== null || settings.settings?.backfillPaused"
          type="button"
          class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-medium"
          :aria-label="settings.settings?.backfillPaused ? 'Resume downloading older mail' : 'Pause downloading older mail'"
          @click="settings.update({ backfillPaused: !settings.settings?.backfillPaused }, 0)"
        >{{ settings.settings?.backfillPaused ? 'Resume' : 'Pause' }}</button>
      </template>
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
