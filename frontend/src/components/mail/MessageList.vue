<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { refDebounced, useThrottleFn } from '@vueuse/core'
import {
  ArchiveIcon, CornerUpLeftIcon, ForwardIcon, InboxIcon, MailIcon, MailOpenIcon, PanelLeftIcon, RefreshCwIcon, SearchIcon,
  SearchXIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon, XIcon, ReplyAllIcon,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { Toggle } from '@/components/ui/toggle'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuSub,
  ContextMenuSubContent, ContextMenuSubTrigger, ContextMenuTrigger, ContextMenuCheckboxItem,
} from '@/components/ui/context-menu'
import LabelPill from '@/components/common/LabelPill.vue'
import Tip from '@/components/common/Tip.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import BulkBar from './BulkBar.vue'
import LabelFilter from './LabelFilter.vue'
import MessageRow from './MessageRow.vue'

const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()
const ai = useAiStore()
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
watch(() => ui.focusSearchTick, () => {
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

// ---- virtual list
const scroller = ref<HTMLElement | null>(null)
const count = computed(() => mail.messages.length + (mail.hasMore ? 1 : 0))
const virtualizer = useVirtualizer(computed(() => ({
  count: count.value,
  getScrollElement: () => scroller.value,
  estimateSize: () => rowHeight.value,
  overscan: 8,
  getItemKey: (i: number) => mail.messages[i]?.id ?? `loader-${i}`,
})))
const items = computed(() => virtualizer.value.getVirtualItems())
const totalSize = computed(() => virtualizer.value.getTotalSize())

watch(rowHeight, () => virtualizer.value.measure())

watch(items, (list) => {
  const last = list[list.length - 1]
  if (last && last.index >= mail.messages.length - 10 && mail.hasMore) void mail.loadMore()
})

const onScroll = useThrottleFn(() => {
  if (scroller.value) mail.saveScroll(scroller.value.scrollTop)
}, 150)

// Restore per-mailbox scroll position after the first page loads.
watch(() => mail.status, async (s, prev) => {
  if (s === 'ready' && prev === 'loading') {
    await nextTick()
    if (scroller.value) scroller.value.scrollTop = mail.savedScroll()
  }
})

// Keep the opened message visible when navigating with the keyboard.
watch(() => mail.openId, (id) => {
  if (!id) return
  const idx = mail.indexById.get(id)
  if (idx !== undefined) virtualizer.value.scrollToIndex(idx, { align: 'auto' })
})

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

// ---- context menu (one for the whole list; the row is found from the event target)
const ctxId = ref<string | null>(null)
const ctxMsg = computed(() => (ctxId.value ? mail.byId.get(ctxId.value) ?? null : null))
function onContext(e: MouseEvent) {
  const row = (e.target as HTMLElement).closest('[data-id]') as HTMLElement | null
  ctxId.value = row?.dataset.id ?? null
  if (!ctxId.value) e.preventDefault()
}
const ctxIds = computed(() => (ctxId.value && mail.selection.has(ctxId.value) ? [...mail.selection] : ctxId.value ? [ctxId.value] : []))

async function respond(mode: 'reply' | 'replyAll' | 'forward') {
  if (!ctxId.value) return
  const thread = await mail.loadThread(ctxId.value)
  const m = thread.find((x) => x.id === ctxId.value) ?? thread[thread.length - 1]
  if (m) compose.respond(mode, m)
}

function toggleCtxLabel(labelId: string) {
  const m = ctxMsg.value
  if (!m) return
  if (m.labels.includes(labelId)) void mail.removeLabel(ctxIds.value, labelId)
  else void mail.addLabel(ctxIds.value, labelId)
}

// ---- header bits
const activeLabels = computed(() => mail.labelFilter.labels.map((id) => settings.labelMap.get(id)).filter((l) => !!l))
const subtitle = computed(() => {
  if (mail.status === 'loading') return ''
  const n = mail.total
  const unread = mail.mailbox.accountId
    ? (mail.mailbox.folderId ? accounts.folderMap.get(mail.mailbox.folderId)?.unread : 0) ?? 0
    : accounts.unifiedUnread(mail.mailbox.kind)
  const parts = [`${n.toLocaleString()} ${n === 1 ? 'message' : 'messages'}`]
  if (unread && !mail.unreadOnly) parts.push(`${unread.toLocaleString()} unread`)
  return parts.join(' · ')
})

const syncing = computed(() => (mail.mailbox.accountId ? accounts.syncing.has(mail.mailbox.accountId) : accounts.anySyncing))
async function refresh() {
  await accounts.sync(mail.mailbox.accountId).catch(() => {})
  void mail.softRefresh()
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
    <header class="border-border shrink-0 border-b px-3 pt-3 pb-2">
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
          <Tip label="Sync now">
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Sync now" @click="refresh">
              <RefreshCwIcon :class="syncing && 'animate-spin'" />
            </Button>
          </Tip>
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
            <MailIcon class="size-4" /> Unread
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
        <button type="button" class="text-muted-foreground hover:text-foreground ml-1 text-xs" @click="mail.clearLabelFilter()">Clear</button>
      </div>
    </header>

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

    <!-- List -->
    <ContextMenu>
      <ContextMenuTrigger as-child>
        <div ref="scroller" class="thin-scroll relative min-h-0 flex-1 overflow-y-auto" role="listbox" aria-label="Message list" @scroll.passive="onScroll" @contextmenu="onContext">
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
            <Button v-if="mail.hasLabelFilter" variant="outline" size="sm" class="mt-2" @click="mail.clearLabelFilter()">Clear label filter</Button>
          </div>

          <div v-else class="relative w-full" :style="{ height: `${totalSize}px` }">
            <div
              v-for="row in items"
              :key="String(row.key)"
              class="absolute top-0 left-0 w-full"
              :style="{ height: `${row.size}px`, transform: `translateY(${row.start}px)` }"
            >
              <MessageRow
                v-if="mail.messages[row.index]"
                :message="mail.messages[row.index]"
                :open="mail.openId === mail.messages[row.index].id"
                :selected="mail.selectAllQuery || mail.selection.has(mail.messages[row.index].id)"
                :selection-mode="selectionMode"
                :compact="compact"
                :account="accounts.accountMap.get(mail.messages[row.index].accountId)"
                :show-account="mail.isUnified"
                :label-map="settings.labelMap"
                :sent-view="sentView"
                @open="(e) => onRowClick(mail.messages[row.index].id, e)"
                @toggle="(e) => onToggle(mail.messages[row.index].id, e)"
                @archive="mail.archive([mail.messages[row.index].id])"
                @trash="mail.trash([mail.messages[row.index].id])"
                @read="(r) => mail.setRead([mail.messages[row.index].id], r)"
                @star="(s) => mail.setStarred([mail.messages[row.index].id], s)"
              />
              <div v-else class="text-muted-foreground flex h-full items-center justify-center gap-2 text-xs">
                <RefreshCwIcon class="size-3.5 animate-spin" /> Loading more…
              </div>
            </div>
          </div>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent v-if="ctxMsg" class="w-56">
        <ContextMenuItem @click="respond('reply')"><CornerUpLeftIcon /> Reply <ContextMenuShortcut>R</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="respond('replyAll')"><ReplyAllIcon /> Reply all <ContextMenuShortcut>A</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="respond('forward')"><ForwardIcon /> Forward <ContextMenuShortcut>F</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem @click="mail.archive(ctxIds)"><ArchiveIcon /> Archive <ContextMenuShortcut>E</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="mail.trash(ctxIds)"><Trash2Icon /> Delete <ContextMenuShortcut>#</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="mail.setRead(ctxIds, ctxMsg.unread)">
          <component :is="ctxMsg.unread ? MailOpenIcon : MailIcon" /> {{ ctxMsg.unread ? 'Mark as read' : 'Mark as unread' }} <ContextMenuShortcut>U</ContextMenuShortcut>
        </ContextMenuItem>
        <ContextMenuItem @click="mail.setStarred(ctxIds, !ctxMsg.starred)"><StarIcon /> {{ ctxMsg.starred ? 'Unstar' : 'Star' }} <ContextMenuShortcut>S</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuSub>
          <ContextMenuSubTrigger><TagIcon class="size-4" /> Labels</ContextMenuSubTrigger>
          <ContextMenuSubContent class="w-52">
            <ContextMenuCheckboxItem
              v-for="l in settings.labels"
              :key="l.id"
              :model-value="ctxMsg.labels.includes(l.id)"
              @select="(e: Event) => { e.preventDefault(); toggleCtxLabel(l.id) }"
            >
              <span class="size-2.5 rounded-full" :style="{ background: l.color }" /> {{ l.name }}
            </ContextMenuCheckboxItem>
          </ContextMenuSubContent>
        </ContextMenuSub>
        <ContextMenuItem :disabled="!settings.aiReady" @click="ai.labelMessages(ctxIds)"><SparklesIcon class="text-violet-500" /> Label with AI</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  </section>
</template>
