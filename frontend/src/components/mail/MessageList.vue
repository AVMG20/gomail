<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { refDebounced, useThrottleFn } from '@vueuse/core'
import {
  EyeOffIcon, InboxIcon, MailIcon, MailOpenIcon, PanelLeftIcon, RefreshCwIcon, SearchIcon, SearchXIcon, SquarePenIcon, TagIcon, XIcon,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { Toggle } from '@/components/ui/toggle'
import { ContextMenu, ContextMenuTrigger } from '@/components/ui/context-menu'
import LabelPill from '@/components/common/LabelPill.vue'
import Tip from '@/components/common/Tip.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import { dateGroup } from '@/lib/dateGroups'
import { hasMethod } from '@/lib/api'
import ReauthBanner from './ReauthBanner.vue'
import { defineAsyncComponent, h } from 'vue'
const BulkBar = defineAsyncComponent(() => import('./BulkBar.vue'))
const AutoMovesBar = defineAsyncComponent(() => import('./AutoMovesBar.vue'))
const RowContextMenu = defineAsyncComponent(() => import('./RowContextMenu.vue'))
const FolderActions = defineAsyncComponent(() => import('./FolderActions.vue'))
// Static stand-in while the (popover-heavy) filter chunk loads.
const LabelFilter = defineAsyncComponent({
  loader: () => import('./LabelFilter.vue'),
  delay: 0,
  loadingComponent: { render: () => h('span', { class: 'text-muted-foreground inline-flex h-8 items-center px-2 text-xs font-medium' }, 'Labels') },
})
import MessageRow from './MessageRow.vue'

const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()
const compose = useComposeStore()

const compact = computed(() => settings.settings?.density === 'compact')
const rowHeight = computed(() => (compact.value ? 40 : 84))
const sentView = computed(() => mail.mailbox.kind === 'sent' || mail.mailbox.kind === 'drafts')

// ---- search (debounced into the store)
const searchInput = ref<HTMLInputElement | null>(null)
const searchText = ref(mail.search)
const debouncedSearch = refDebounced(searchText, 280)
watch(debouncedSearch, (v) => (mail.search = v))
watch(() => mail.search, (v) => {
  if (v !== searchText.value) searchText.value = v
})
watch(() => ui.focusSearchTick, async () => {
  await nextTick() // the list may have just become visible (reading pane off)
  searchInput.value?.focus()
  searchInput.value?.select()
})
function onSearchKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    searchText.value = ''
    mail.search = ''
    searchInput.value?.blur()
  } else if (e.key === 'Enter') {
    mail.search = searchText.value
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    searchInput.value?.blur()
    mail.move(1)
  }
}

// Track the hovered row via delegation so only it mounts quick actions.
const hoveredId = ref<string | null>(null)
function onHover(e: MouseEvent) {
  const id = ((e.target as HTMLElement).closest('[data-id]') as HTMLElement | null)?.dataset.id ?? null
  if (id !== hoveredId.value) hoveredId.value = id
}

// Group label per message object (objects are stable across pages).
const groupCache = new WeakMap<object, string>()
function groupOf(m: { date: string }): string {
  let g = groupCache.get(m)
  if (!g) {
    g = dateGroup(m.date)
    groupCache.set(m, g)
  }
  return g
}

// ---- rows: messages interleaved with light date-group headers
type Row = { kind: 'header'; key: string; label: string } | { kind: 'msg'; key: string; index: number; group: string } | { kind: 'loader'; key: string } | { kind: 'older'; key: string } | { kind: 'beyond'; key: string }
const HEADER_H = computed(() => (compact.value ? 26 : 30))
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  let last = ''
  const list = mail.messages
  for (let i = 0; i < list.length; i++) {
    const g = groupOf(list[i])
    if (g !== last) {
      out.push({ kind: 'header', key: `h:${g}:${i}`, label: g })
      last = g
    }
    out.push({ kind: 'msg', key: list[i].id, index: i, group: g })
  }
  if (mail.hasMore) out.push({ kind: 'loader', key: 'loader' })
  else if (mail.hasMoreRemote && list.length && hasMethod('LoadOlder')) out.push({ kind: 'older', key: 'older' })
  // Past the sync depth: older mail only on request, so clearing the inbox doesn't pull in years of it.
  else if (mail.olderBeyondDepth && list.length && hasMethod('LoadOlder')) out.push({ kind: 'beyond', key: 'beyond' })
  return out
})
const rowIndexById = computed(() => {
  const m = new Map<string, number>()
  rows.value.forEach((r, i) => r.kind === 'msg' && m.set(r.key, i))
  return m
})

// ---- virtual list
const scroller = ref<HTMLElement | null>(null)
const virtualizer = useVirtualizer(computed(() => ({
  count: rows.value.length,
  getScrollElement: () => scroller.value,
  estimateSize: (i: number) => (rows.value[i]?.kind === 'header' ? HEADER_H.value : rowHeight.value),
  overscan: 10,
  getItemKey: (i: number) => rows.value[i]?.key ?? i,
})))
const items = computed(() => virtualizer.value.getVirtualItems())
const totalSize = computed(() => virtualizer.value.getTotalSize())

watch([rowHeight, HEADER_H], () => virtualizer.value.measure())

watch(items, (list) => {
  const last = list[list.length - 1]
  if (last && last.index >= rows.value.length - 12 && mail.hasMore) void mail.loadMore()
  else if (last && rows.value[last.index]?.kind === 'older') void mail.loadOlder()
})

// Sticky group label: the group of the first visible row, pushed up by the next header.
const scrollTop = ref(0)
const sticky = computed(() => {
  const list = items.value
  if (!list.length || scrollTop.value <= 0) return null
  const top = scrollTop.value
  const first = list.find((it) => it.end > top)
  if (!first) return null
  const row = rows.value[first.index]
  const label = row?.kind === 'msg' ? row.group : row?.kind === 'header' ? (first.start >= top ? null : row.label) : null
  if (!label) return null
  const next = list.find((it) => it.start > top && rows.value[it.index]?.kind === 'header')
  const push = next ? Math.min(0, next.start - top - HEADER_H.value) : 0
  return { label, push }
})

const onScroll = useThrottleFn(() => {
  // While a mailbox loads, the skeleton is short and the browser clamps scrollTop: that is
  // not the user scrolling, and saving it would overwrite the new mailbox's position.
  if (!scroller.value || mail.status !== 'ready') return
  scrollTop.value = scroller.value.scrollTop
  mail.saveScroll(scroller.value.scrollTop)
  if (scroller.value.scrollTop < 8) newCount.value = 0
}, 16)

// Restore per-mailbox scroll position after the first page loads.
watch(() => mail.status, async (st, prev) => {
  if (st === 'ready' && prev === 'loading') {
    newCount.value = 0
    await nextTick()
    if (scroller.value) scroller.value.scrollTop = mail.savedScroll()
  }
})

// Keep the opened message visible when navigating with the keyboard.
watch(() => mail.openId, (id) => {
  if (!id) return
  const idx = rowIndexById.value.get(id)
  if (idx !== undefined) virtualizer.value.scrollToIndex(idx, { align: 'auto' })
})

// New mail arrives at the top without moving what you are reading: keep the
// first visible message anchored and offer a "N new messages" pill instead.
const newCount = ref(0)
watch(rows, async (now, old) => {
  const el = scroller.value
  if (!el || el.scrollTop <= 0 || mail.status !== 'ready' || !old?.length) return
  // The virtualizer still describes the old rows here (pre-flush): anchor on them.
  const first = items.value.find((it) => it.end > el.scrollTop && old[it.index]?.kind === 'msg')
  if (!first) return
  const anchorId = old[first.index].key
  const delta = first.start - el.scrollTop
  const before = new Set(old.filter((r) => r.kind === 'msg').map((r) => r.key))
  await nextTick()
  const idx = rowIndexById.value.get(anchorId)
  if (idx === undefined || !scroller.value) return
  const added = now.slice(0, idx).filter((r) => r.kind === 'msg' && !before.has(r.key)).length
  const offset = virtualizer.value.getOffsetForIndex(idx, 'start')?.[0]
  if (offset !== undefined) {
    const target = offset - delta
    if (Math.abs(target - scroller.value.scrollTop) > 1) scroller.value.scrollTop = target
  }
  if (added > 0) newCount.value += added
})
function jumpToTop() {
  newCount.value = 0
  scroller.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

type MsgRow = Extract<Row, { kind: 'msg' }>
type HeaderRow = Extract<Row, { kind: 'header' }>

// Quick actions remove the row; keep keyboard focus in the list.
function quick(fn: () => unknown) {
  void fn()
  scroller.value?.focus({ preventScroll: true })
}

// ---- row interactions
const selectionMode = computed(() => mail.selection.size > 0 || mail.selectAllQuery)

function onRowClick(id: string, e: MouseEvent) {
  if (e.shiftKey) {
    mail.toggleSelect(id, { range: true })
    window.getSelection()?.removeAllRanges()
  } else if (e.metaKey || e.ctrlKey) mail.toggleSelect(id)
  else {
    if (selectionMode.value) mail.clearSelection()
    mail.open(id)
  }
}

function onToggle(id: string, e: MouseEvent | null) {
  mail.toggleSelect(id, { range: !!e?.shiftKey })
}

const allLoadedSelected = computed(() => mail.messages.length > 0 && mail.selection.size === mail.messages.length)
const headerCheck = computed<boolean | 'indeterminate'>(() => (allLoadedSelected.value || mail.selectAllQuery ? true : mail.selection.size ? 'indeterminate' : false))
function toggleHeaderCheck() {
  if (headerCheck.value === true) mail.clearSelection()
  else mail.selectAllLoaded()
}

// ---- context menu: one lazily loaded menu for the whole list, opened at the cursor
const ctxId = ref<string | null>(null)
function onContext(e: MouseEvent) {
  const row = (e.target as HTMLElement).closest('[data-id]') as HTMLElement | null
  ctxId.value = row?.dataset.id ?? null
}

// ---- header bits
const activeLabels = computed(() => mail.labelFilter.labels.map((id) => settings.labelMap.get(id)).filter((l) => !!l))
const hiddenLabels = computed(() => mail.labelFilter.hidden.map((id) => settings.labelMap.get(id)).filter((l) => !!l))
const subtitle = computed(() => {
  if (mail.status === 'loading') return ''
  const n = mail.total
  // A label filter or search narrows the view: count its unread, not the whole folder's.
  const unread = mail.viewUnreadCount
  const parts = [`${n.toLocaleString()} ${n === 1 ? 'message' : 'messages'}`]
  if (unread && !mail.unreadOnly) parts.push(`${unread.toLocaleString()} unread`)
  return parts.join(' · ')
})

const syncing = computed(() => (mail.mailbox.accountId ? accounts.syncing.has(mail.mailbox.accountId) : accounts.anySyncing))
async function refresh() {
  await accounts.sync(mail.mailbox.accountId).catch(() => {})
  void mail.softRefresh()
}

/** No results in this folder: run the same search over all mail of the account (or all accounts). */
function searchAllMail() {
  const q = mail.search
  mail.setMailbox({ accountId: mail.mailbox.accountId, folderId: '', kind: 'all' })
  mail.search = q
}

const emptyState = computed(() => {
  if (mail.search) return { icon: SearchXIcon, title: 'No results', text: `Nothing matches “${mail.search}”. Try fewer words or operators like from: or has:attachment.` }
  if (mail.hasLabelFilter) return { icon: TagIcon, title: 'No mail with these labels', text: 'Try another label, or clear the filter.' }
  if (mail.unreadOnly) return { icon: MailOpenIcon, title: 'No unread mail', text: 'You have read everything here.' }
  if (mail.mailbox.kind === 'inbox') return { icon: InboxIcon, title: 'You’re all caught up', text: 'New mail will show up here.' }
  return { icon: InboxIcon, title: `${mail.mailboxName} is empty`, text: '' }
})
</script>

<template>
  <section class="bg-background flex h-full min-w-0 flex-col" aria-label="Messages">
    <!-- Header -->
    <header data-drag class="border-border @container shrink-0 border-b px-3 pt-3 pb-2">
      <div class="flex h-9 items-center gap-1">
        <template v-if="mail.selection.size || mail.selectAllQuery">
          <BulkBar class="flex-1" />
        </template>
        <template v-else>
          <Tip v-if="!ui.folderSidebarOpen || ui.narrow" label="Toggle folders" shortcut="mod+b">
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground -ml-1" aria-label="Toggle folders" @click="ui.folderSidebarOpen = !ui.folderSidebarOpen">
              <PanelLeftIcon />
            </Button>
          </Tip>
          <div class="min-w-0 flex-1 pl-1">
            <h1 class="truncate text-[15px] leading-tight font-semibold tracking-tight">{{ mail.mailboxName }}</h1>
            <p class="text-muted-foreground h-4 truncate text-xs leading-4">{{ subtitle }}</p>
          </div>
          <Tip label="New message" shortcut="c">
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="New message" @click="compose.compose()">
              <SquarePenIcon />
            </Button>
          </Tip>
          <Tip label="Sync now">
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Sync now" @click="refresh">
              <RefreshCwIcon :class="syncing && 'animate-spin'" />
            </Button>
          </Tip>
          <FolderActions />
        </template>
      </div>

      <div class="mt-2 flex items-center gap-1.5">
        <Checkbox :model-value="headerCheck" class="mx-[9px]" aria-label="Select all loaded" :disabled="!mail.messages.length" @update:model-value="toggleHeaderCheck" />
        <div class="bg-muted/60 focus-within:bg-background focus-within:ring-ring/40 border-border/0 focus-within:border-border relative flex h-8 min-w-0 flex-1 items-center rounded-md border transition-colors focus-within:ring-2">
          <SearchIcon class="text-muted-foreground pointer-events-none absolute left-2.5 size-4" />
          <input
            ref="searchInput"
            v-model="searchText"
            type="search"
            placeholder="Search"
            class="placeholder:text-muted-foreground h-full w-full min-w-0 bg-transparent pr-14 pl-8 text-[13px] outline-none [&::-webkit-search-cancel-button]:hidden"
            aria-label="Search mail"
            spellcheck="false"
            @keydown="onSearchKey"
          />
          <button v-if="searchText" type="button" class="text-muted-foreground hover:text-foreground absolute right-2 rounded p-0.5" aria-label="Clear search" @click="searchText = ''; mail.search = ''">
            <XIcon class="size-3.5" />
          </button>
          <kbd v-else class="text-muted-foreground border-border pointer-events-none absolute right-2 rounded border px-1 font-sans text-[10px] leading-4">/</kbd>
        </div>
        <Tip label="Unread only">
          <Toggle v-model="mail.unreadOnly" size="sm" class="text-muted-foreground data-[state=on]:text-foreground h-8 gap-1.5 px-2 text-xs" aria-label="Unread only">
            <MailIcon class="size-4" /> <span class="hidden @[440px]:inline">Unread</span>
          </Toggle>
        </Tip>
        <LabelFilter />
      </div>

      <div v-if="mail.hasLabelFilter" class="mt-2 flex flex-wrap items-center gap-1.5 pl-8">
        <LabelPill v-for="l in activeLabels" :key="l!.id" :label="l!" size="sm" removable @remove="mail.toggleLabelFilter(l!.id)" />
        <span v-if="mail.labelFilter.unlabeled" class="border-border text-muted-foreground inline-flex h-6 items-center gap-1 rounded-full border border-dashed px-2 text-xs">
          No label
          <button type="button" class="opacity-60 hover:opacity-100" aria-label="Remove no-label filter" @click="mail.toggleUnlabeledFilter()"><XIcon class="size-3" /></button>
        </span>
        <span v-for="l in hiddenLabels" :key="`hidden-${l!.id}`" class="border-border text-muted-foreground inline-flex h-6 items-center gap-1.5 rounded-full border border-dashed px-2 text-xs" :title="`Hiding mail labeled ${l!.name}`">
          <EyeOffIcon class="size-3" />
          <span class="size-2 rounded-full" :style="{ background: l!.color }" />
          <span class="line-through">{{ l!.name }}</span>
          <button type="button" class="opacity-60 hover:opacity-100" :aria-label="`Stop hiding ${l!.name}`" @click="mail.toggleHiddenLabel(l!.id)"><XIcon class="size-3" /></button>
        </span>
        <button type="button" class="text-muted-foreground hover:text-foreground ml-1 text-xs" @click="mail.clearLabelFilter()">Clear</button>
      </div>
    </header>
    <AutoMovesBar />

    <ReauthBanner />

    <!-- The search as typed found nothing: these are close matches. -->
    <div v-if="mail.similar && mail.search && mail.messages.length" class="bg-muted/60 text-muted-foreground border-border shrink-0 border-b px-3 py-1.5 text-xs">
      No exact matches for “{{ mail.search }}”. Showing similar mail.
    </div>

    <!-- Select-all banner -->
    <div
      v-if="(allLoadedSelected && mail.total > mail.messages.length) || mail.selectAllQuery"
      class="bg-muted/60 text-muted-foreground border-border flex shrink-0 items-center justify-center gap-2 border-b px-3 py-1.5 text-xs"
    >
      <template v-if="!mail.selectAllQuery">
        <span>All {{ mail.messages.length.toLocaleString() }} loaded selected</span>
        <span>·</span>
        <button type="button" class="text-primary font-medium hover:underline" @click="mail.selectAllQuery = true">Select all {{ mail.total.toLocaleString() }} in this view</button>
      </template>
      <template v-else>
        <span>All {{ mail.total.toLocaleString() }} in this view selected</span>
        <span>·</span>
        <button type="button" class="text-primary font-medium hover:underline" @click="mail.clearSelection()">Clear selection</button>
      </template>
    </div>

    <!-- Progress line while filters reload -->
    <div class="relative h-0">
      <div v-if="mail.refreshing" class="bg-primary/70 absolute inset-x-0 top-0 z-10 h-0.5 animate-pulse" />
    </div>

    <!-- New mail pill -->
    <div class="relative h-0">
      <Transition enter-from-class="opacity-0 -translate-y-1" enter-active-class="transition duration-200" leave-to-class="opacity-0" leave-active-class="transition duration-150">
        <button
          v-if="newCount > 0"
          type="button"
          class="bg-primary text-primary-foreground absolute top-2 left-1/2 z-20 -translate-x-1/2 rounded-full px-3 py-1 text-xs font-medium shadow-md"
          @click="jumpToTop"
        >↑ {{ newCount }} new message{{ newCount === 1 ? '' : 's' }}</button>
      </Transition>
    </div>

    <!-- List -->
    <ContextMenu @update:open="(o: boolean) => !o && (ctxId = null)">
      <ContextMenuTrigger as-child>
        <div
          ref="scroller"
          class="thin-scroll @container relative min-h-0 flex-1 overflow-y-auto outline-none"
          role="listbox"
          tabindex="0"
          :aria-label="`${mail.mailboxName}, ${mail.total} messages`"
          :aria-activedescendant="mail.openId ? `row-${mail.openId}` : undefined"
          aria-multiselectable="true"
          @scroll.passive="onScroll"
          @mouseover="onHover"
          @mouseleave="hoveredId = null"
          @contextmenu="onContext"
        >
          <div v-if="mail.status === 'loading'" aria-busy="true">
            <div v-for="i in 9" :key="i" class="border-border/60 flex gap-3 border-b px-4" :style="{ height: `${rowHeight}px` }">
              <div class="flex flex-1 flex-col justify-center gap-2 pl-5">
                <div class="flex justify-between"><Skeleton class="h-3.5 w-32" /><Skeleton class="h-3 w-10" /></div>
                <Skeleton v-if="!compact" class="h-3 w-3/4" />
                <Skeleton v-if="!compact" class="h-3 w-1/2" />
              </div>
            </div>
          </div>

          <div v-else-if="mail.status === 'error'" class="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
            <p class="text-sm font-medium">Couldn’t load messages</p>
            <p class="text-muted-foreground max-w-xs text-xs">{{ mail.error }}</p>
            <Button variant="outline" size="sm" @click="mail.reload()">Try again</Button>
          </div>

          <div v-else-if="!mail.messages.length" class="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
            <div class="bg-muted text-muted-foreground mb-2 flex size-12 items-center justify-center rounded-full">
              <component :is="emptyState.icon" class="size-5" />
            </div>
            <p class="text-sm font-medium">{{ emptyState.title }}</p>
            <p v-if="emptyState.text" class="text-muted-foreground max-w-xs text-xs leading-relaxed">{{ emptyState.text }}</p>
            <!-- A way out of every filtered empty view -->
            <div v-if="mail.search || mail.hasLabelFilter || mail.unreadOnly" class="mt-2 flex flex-wrap justify-center gap-2">
              <Button v-if="mail.search && mail.mailbox.kind !== 'all'" variant="outline" size="sm" @click="searchAllMail">Search all mail</Button>
              <Button v-if="mail.search" variant="outline" size="sm" @click="searchText = ''; mail.search = ''">Clear search</Button>
              <Button v-if="mail.hasLabelFilter" variant="outline" size="sm" @click="mail.clearLabelFilter()">Clear label filter</Button>
              <Button v-if="mail.unreadOnly" variant="outline" size="sm" @click="mail.unreadOnly = false">Show read mail too</Button>
            </div>
          </div>

          <template v-else>
          <!-- Sticky date label (zero-height so it never takes layout space) -->
          <div v-if="sticky" class="pointer-events-none sticky top-0 z-10 h-0">
            <div
              class="bg-background/90 text-muted-foreground border-border/60 flex items-end border-b px-4 pb-1 pl-12 text-[11px] font-medium tracking-wide uppercase backdrop-blur"
              :style="{ height: `${HEADER_H}px`, transform: `translateY(${sticky.push}px)` }"
            >{{ sticky.label }}</div>
          </div>
          <div class="relative w-full" :style="{ height: `${totalSize}px` }">
            <div
              v-for="it in items"
              :key="String(it.key)"
              class="absolute top-0 left-0 w-full"
              :style="{ height: `${it.size}px`, transform: `translateY(${it.start}px)` }"
            >
              <template v-if="rows[it.index]?.kind === 'msg'">
                <MessageRow
                  :id="`row-${rows[it.index].key}`"
                  :message="mail.messages[(rows[it.index] as MsgRow).index]"
                  :open="mail.openId === rows[it.index].key"
                  :selected="mail.selectAllQuery || mail.selection.has(rows[it.index].key)"
                  :selection-mode="selectionMode"
                  :compact="compact"
                  :account="accounts.accountMap.get(mail.messages[(rows[it.index] as MsgRow).index].accountId)"
                  :show-account="mail.isUnified"
                  :label-map="settings.labelMap"
                  :sent-view="sentView"
                  :hovered="hoveredId === rows[it.index].key"
                  @open="(e) => onRowClick(rows[it.index].key, e)"
                  @toggle="(e) => onToggle(rows[it.index].key, e)"
                  @archive="quick(() => mail.archive([rows[it.index].key]))"
                  @trash="quick(() => mail.trash([rows[it.index].key]))"
                  @read="(r) => mail.setRead([rows[it.index].key], r)"
                  @star="(st) => mail.setStarred([rows[it.index].key], st)"
                />
              </template>
              <div v-else-if="rows[it.index]?.kind === 'header'" class="text-muted-foreground flex h-full items-end px-4 pb-1 pl-12 text-[11px] font-medium tracking-wide uppercase" role="presentation">
                {{ (rows[it.index] as HeaderRow).label }}
              </div>
              <div v-else-if="rows[it.index]?.kind === 'older'" class="text-muted-foreground flex h-full items-center justify-center gap-2 text-xs">
                <template v-if="mail.loadingOlder"><RefreshCwIcon class="size-3.5 animate-spin" /> Fetching older mail from the server…</template>
                <button v-else type="button" class="hover:text-foreground rounded px-2 py-1 font-medium" @click="mail.loadOlder()">Load older messages</button>
              </div>
              <div v-else-if="rows[it.index]?.kind === 'beyond'" class="text-muted-foreground flex h-full items-center justify-center gap-1 text-xs">
                <template v-if="mail.loadingOlder"><RefreshCwIcon class="size-3.5 animate-spin" /> Fetching older mail from the server…</template>
                <template v-else>
                  <span>Synced: last {{ settings.settings?.syncDepthDays ?? 90 }} days.</span>
                  <button type="button" class="hover:text-foreground rounded px-1.5 py-1 font-medium" @click="mail.loadOlder(true)">Load older anyway</button>
                </template>
              </div>
              <div v-else class="text-muted-foreground flex h-full items-center justify-center gap-2 text-xs">
                <RefreshCwIcon class="size-3.5 animate-spin" /> Loading more…
              </div>
            </div>
          </div>
          </template>
        </div>
      </ContextMenuTrigger>
      <RowContextMenu v-if="ctxId" :key="ctxId" :message-id="ctxId" />
    </ContextMenu>
  </section>
</template>
