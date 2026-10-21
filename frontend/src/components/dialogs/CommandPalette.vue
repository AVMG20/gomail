<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch, type Component } from 'vue'
import DOMPurify from 'dompurify'
import {
  ArchiveIcon, CheckIcon, ClockIcon, CornerUpLeftIcon, KeyboardIcon, LayersIcon, MoonIcon, PencilIcon, PlusIcon,
  RefreshCwIcon, SearchIcon, SettingsIcon, SparklesIcon, TagIcon, LoaderIcon, ForwardIcon, CornerDownLeftIcon,
  CalendarDaysIcon, CalendarPlusIcon, InboxIcon, MailCheckIcon, UsersIcon, MailIcon, MailOpenIcon, MessagesSquareIcon, OctagonAlertIcon, XIcon, PanelLeftIcon, PanelRightIcon, ReplyAllIcon, StarIcon, Trash2Icon,
} from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import CmdRoot from '@/components/common/cmd/CmdRoot.vue'
import CmdInput from '@/components/common/cmd/CmdInput.vue'
import CmdList from '@/components/common/cmd/CmdList.vue'
import CmdGroup from '@/components/common/cmd/CmdGroup.vue'
import CmdItem from '@/components/common/cmd/CmdItem.vue'
import KeyCaps from '@/components/common/KeyCaps.vue'
import LabelPill from '@/components/common/LabelPill.vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { FOLDER_ICONS } from '@/components/mail/folderIcons'
import { api, hasMethod } from '@/lib/api'
import { fuzzyFilter } from '@/lib/fuzzy'
import { openLabelSettings } from '@/lib/labelScope'
import { applyCompletion, hasOperator, operatorCompletions, type Completion } from '@/lib/searchQuery'
import { displayName, listDate } from '@/lib/format'
import { hasMod, shortcutLabel } from '@/lib/platform'
import { currentIsDark } from '@/lib/theme'
import type { Label, MessageSummary, SearchResult } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useCleanupStore } from '@/stores/cleanup'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const compose = useComposeStore()
const ai = useAiStore()
const cleanup = useCleanupStore()

const RECENT_KEY = 'gomail.recentSearches.v1'
const query = ref('')
const scopeAll = ref(true)
const results = shallowRef<SearchResult[]>([])
const searching = ref(false)
const recent = ref<string[]>(loadRecent())

function loadRecent(): string[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]') as string[]
  } catch {
    return []
  }
}
function pushRecent(q: string) {
  const s = q.trim()
  if (!s) return
  recent.value = [s, ...recent.value.filter((x) => x !== s)].slice(0, 6)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recent.value))
  } catch {
    /* ignore */
  }
}

watch(() => ui.paletteOpen, (open) => {
  if (open) {
    query.value = ''
    results.value = []
    // Search the mailbox on screen; every mailbox only from the unified view.
    scopeAll.value = !mail.mailbox.accountId
  }
})

const scopeAccount = computed(() => (scopeAll.value ? '' : mail.mailbox.accountId))
const currentAccount = computed(() => mail.mailboxAccount)

// ---- live server search (debounced, stale responses dropped)
let token = 0
let timer: ReturnType<typeof setTimeout> | null = null
const searchTick = ref(0)
const onSearchUpdated = () => searchTick.value++
window.addEventListener('gomail:search-updated', onSearchUpdated)
onBeforeUnmount(() => window.removeEventListener('gomail:search-updated', onSearchUpdated))
watch([query, scopeAccount, searchTick], () => {
  if (timer) clearTimeout(timer)
  const q = query.value.trim()
  if (!q || /^\S+:$/.test(q)) {
    token++
    results.value = []
    searching.value = false
    return
  }
  searching.value = true
  timer = setTimeout(async () => {
    const t = ++token
    try {
      // Searches all mail: the list's label filter doesn't apply here (use label: to narrow).
      const res = await api.searchMessages({ query: q, accountId: scopeAccount.value, labels: [], limit: 20 })
      if (t === token) results.value = oneOfEachConversation(res)
    } catch {
      if (t === token) results.value = []
    } finally {
      if (t === token) searching.value = false
    }
  }, 120)
})

/** Like the list in conversation view: one row per conversation, its best match (results come ranked). */
function oneOfEachConversation(res: SearchResult[]): SearchResult[] {
  if (settings.settings?.conversationView === false) return res
  const seen = new Set<string>()
  return res.filter((r) => {
    const key = r.message.threadId ? `${r.message.accountId}|${r.message.threadId}` : r.message.id
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const MATCH_FIELDS: Record<string, string> = { subject: 'subject', from: 'sender', to: 'recipients', snippet: 'preview', body: 'message body' }
/** Explain the hit when the highlighted excerpt doesn't show it (or it was typo-tolerant). */
function matchHint(r: SearchResult): string {
  const field = MATCH_FIELDS[r.matchedIn]
  if (r.fuzzy) return field ? `Similar match in ${field}` : 'Similar match'
  if (field && r.matchedIn !== 'subject' && !r.highlight.includes('<mark>')) return `Matched in ${field}`
  return ''
}

function safeHighlight(html: string) {
  return DOMPurify.sanitize(html, { ALLOWED_TAGS: ['mark'], ALLOWED_ATTR: [] })
}

// ---- operator completions (label:, from:, is:, has:, in:)
const completions = computed<Completion[]>(() => operatorCompletions(query.value, { labels: settings.labelsForAccount(scopeAccount.value), contacts: [...mail.addressBook.values()] }))

function complete(c: Completion) {
  query.value = applyCompletion(query.value, c.value)
}

// ---- local fuzzy sections
const q = computed(() => query.value.trim())
// Label items filter the list shown: only that mailbox's labels (every label in the unified view).
const labelItems = computed<Label[]>(() => (q.value.includes(':') ? [] : fuzzyFilter(q.value, settings.labelsForAccount(mail.mailbox.accountId), (l) => l.name, q.value ? 3 : 0)))

interface MailboxItem { key: string; title: string; subtitle: string; icon?: Component; accountId: string; folderId: string; kind: string; isAccount?: boolean }
const mailboxItems = computed<MailboxItem[]>(() => {
  const list: MailboxItem[] = []
  if (accounts.accounts.length > 1) list.push({ key: 'all', title: 'All inboxes', subtitle: 'Combined view', icon: LayersIcon, accountId: '', folderId: '', kind: 'inbox' })
  for (const a of accounts.accounts) {
    list.push({ key: a.id, title: `Switch to ${a.displayName || a.email}`, subtitle: a.email, accountId: a.id, folderId: '', kind: '', isAccount: true })
    for (const f of accounts.folders[a.id] ?? []) {
      list.push({ key: f.id, title: f.kind === 'spam' ? 'Junk' : f.name, subtitle: a.displayName || a.email, icon: FOLDER_ICONS[f.kind] ?? FOLDER_ICONS.custom, accountId: a.id, folderId: f.id, kind: f.kind })
    }
  }
  if (!q.value || q.value.includes(':')) return q.value ? [] : list.filter((x) => x.isAccount || x.key === 'all')
  return fuzzyFilter(q.value, list, (x) => `${x.title} ${x.subtitle}`, 6)
})

interface Action { id: string; title: string; icon: Component; shortcut?: string; run: () => void; keep?: boolean }
const actions = computed<Action[]>(() => {
  const open = mail.openSummary
  const list: Action[] = [
    { id: 'compose', title: 'Compose new message', icon: PencilIcon, shortcut: 'c', run: () => compose.compose() },
    { id: 'sync', title: 'Sync now', icon: RefreshCwIcon, run: () => void accounts.sync(mail.mailbox.accountId).catch(() => {}) },
    { id: 'settings', title: 'Open settings', icon: SettingsIcon, shortcut: 'mod+,', run: () => ui.openSettings() },
    { id: 'labels', title: 'Manage labels', icon: TagIcon, run: () => openLabelSettings(mail.mailbox.accountId) },
    { id: 'theme', title: currentIsDark() ? 'Switch to light theme' : 'Switch to dark theme', icon: MoonIcon, run: () => settings.update({ theme: currentIsDark() ? 'light' : 'dark' }) },
    { id: 'shortcuts', title: 'Keyboard shortcuts', icon: KeyboardIcon, shortcut: '?', run: () => (ui.shortcutsOpen = true) },
    { id: 'add', title: 'Add account', icon: PlusIcon, run: () => (ui.addAccountOpen = true) },
    { id: 'welcome', title: 'Show welcome guide', icon: SparklesIcon, run: () => ui.welcomeTick++ },
    ...(hasMethod('ListCalendars')
      ? [
          ui.mainView === 'calendar'
            ? { id: 'to-mail', title: 'Go to mail', icon: MailIcon, shortcut: 'g m', run: () => (ui.mainView = 'mail') }
            : { id: 'to-cal', title: 'Open calendar', icon: CalendarDaysIcon, shortcut: 'g c', run: () => (ui.mainView = 'calendar') },
          { id: 'new-event', title: 'New event', icon: CalendarPlusIcon, shortcut: ui.mainView === 'calendar' ? 'n' : undefined, run: () => { ui.mainView = 'calendar'; ui.newEventPending = true } },
        ]
      : []),
    { id: 'read-all', title: `Mark all as read in ${mail.mailboxName}`, icon: MailCheckIcon, run: () => folderAction('markAllRead') },
    { id: 'pane', title: settings.settings?.readingPane === 'off' ? 'Show reading pane' : 'Hide reading pane', icon: PanelRightIcon, run: () => settings.update({ readingPane: settings.settings?.readingPane === 'off' ? 'right' : 'off' }) },
    { id: 'conv', title: settings.settings?.conversationView === false ? 'Group by conversation' : 'Show individual messages', icon: MessagesSquareIcon, run: () => settings.update({ conversationView: settings.settings?.conversationView === false }) },
    { id: 'folders', title: 'Toggle folders', icon: PanelLeftIcon, shortcut: 'mod+b', run: () => (ui.folderSidebarOpen = !ui.folderSidebarOpen) },
  ]
  if (hasMethod('TopSenders')) {
    list.push({ id: 'cleanup', title: 'Clean up inbox by sender', icon: UsersIcon, run: () => { ui.mainView = 'mail'; cleanup.show(mail.mailbox.accountId) } })
  }
  if (mail.mailbox.kind === 'trash' || mail.mailbox.kind === 'spam') {
    list.push({ id: 'empty', title: `Empty ${mail.mailbox.kind === 'spam' ? 'Junk' : 'Trash'}…`, icon: Trash2Icon, run: () => folderAction('empty') })
  }
  if (settings.aiReady) {
    list.push({ id: 'ai-all', title: 'Label all mail with AI…', icon: SparklesIcon, run: () => ai.requestBulk({ query: { accountId: '' }, ids: [], title: 'All mail in all accounts' }) })
    list.push({ id: 'ai-view', title: `Label ${mail.mailboxName} with AI…`, icon: SparklesIcon, run: () => folderAction('labelAi') })
  }
  const inJunk = mail.mailbox.kind === 'spam'
  if (mail.selection.size || mail.selectAllQuery) {
    // Mail is selected: the actions apply to the selection, like their shortcuts and the bulk bar.
    const n = `${mail.selectionCount.toLocaleString()} selected`
    const ids = [...mail.selection]
    const star = mail.shouldStar(ids, mail.selectAllQuery)
    const unread = mail.selectAllQuery ? mail.viewUnreadCount > 0 : ids.some((id) => mail.byId.get(id)?.unread)
    list.unshift(
      { id: 'sel-archive', title: `Archive ${n}`, icon: ArchiveIcon, shortcut: 'e', run: () => mail.actOnTargets(mail.archive) },
      { id: 'sel-delete', title: `Delete ${n}`, icon: Trash2Icon, shortcut: '#', run: () => mail.actOnTargets(mail.trash) },
      unread
        ? { id: 'sel-read', title: `Mark ${n} as read`, icon: MailOpenIcon, shortcut: 'shift+i', run: () => void mail.markTargetsRead() }
        : { id: 'sel-unread', title: `Mark ${n} as unread`, icon: MailIcon, shortcut: 'u', run: () => mail.actOnTargets((x) => mail.setRead(x, false)) },
      { id: 'sel-star', title: `${star ? 'Star' : 'Unstar'} ${n}`, icon: StarIcon, shortcut: 's', run: () => mail.actOnTargets((x) => mail.setStarred(x, star)) },
      { id: 'sel-label', title: `Add a label to ${n}…`, icon: TagIcon, shortcut: 'l', run: () => setTimeout(() => (ui.bulkLabelOpen = true), 50) },
      inJunk
        ? { id: 'sel-notjunk', title: `Not junk: move ${n} to Inbox`, icon: InboxIcon, shortcut: '!', run: () => mail.actOnTargets(mail.notJunk) }
        : { id: 'sel-junk', title: `Move ${n} to Junk`, icon: OctagonAlertIcon, shortcut: '!', run: () => mail.actOnTargets(mail.junk) },
      { id: 'sel-clear', title: 'Clear selection', icon: XIcon, shortcut: 'esc', run: () => mail.clearSelection() },
    )
    if (settings.aiReady) list.splice(4, 0, { id: 'sel-ai', title: `Label ${n} with AI`, icon: SparklesIcon, run: () => void ai.labelSelection() })
  } else if (open) {
    const noun = settings.settings?.conversationView === false ? 'message' : 'conversation'
    list.unshift(
      { id: 'reply', title: 'Reply', icon: CornerUpLeftIcon, shortcut: 'r', run: () => replyTo(open, 'reply') },
      { id: 'forward', title: 'Forward', icon: ForwardIcon, shortcut: 'f', run: () => replyTo(open, 'forward') },
      { id: 'archive', title: `Archive ${noun}`, icon: ArchiveIcon, shortcut: 'e', run: () => mail.archive([open.id]) },
      { id: 'reply-all', title: 'Reply all', icon: ReplyAllIcon, shortcut: 'a', run: () => replyTo(open, 'replyAll') },
      { id: 'delete', title: `Delete ${noun}`, icon: Trash2Icon, shortcut: '#', run: () => mail.trash([open.id]) },
      { id: 'star', title: open.starred ? 'Unstar' : 'Star', icon: StarIcon, shortcut: 's', run: () => mail.setStarred([open.id], !open.starred) },
      open.unread
        ? { id: 'read', title: 'Mark as read', icon: MailOpenIcon, shortcut: 'shift+i', run: () => mail.setRead([open.id], true) }
        : { id: 'unread', title: 'Mark as unread', icon: MailIcon, shortcut: 'u', run: () => mail.setRead([open.id], false) },
      { id: 'label', title: 'Labels…', icon: TagIcon, shortcut: 'l', run: () => setTimeout(() => (ui.labelPopoverOpen = true), 50) },
      inJunk
        ? { id: 'notjunk', title: 'Not junk: move to Inbox', icon: InboxIcon, shortcut: '!', run: () => mail.notJunk([open.id]) }
        : { id: 'junk', title: `Move ${noun} to Junk`, icon: OctagonAlertIcon, shortcut: '!', run: () => mail.junk([open.id]) },
    )
    if (settings.aiReady) list.splice(3, 0, { id: 'ai-one', title: 'Label this mail with AI', icon: SparklesIcon, run: () => ai.labelMessages([open.id]) })
  }
  if (!q.value) return list.slice(0, 6)
  if (q.value.includes(':')) return []
  return fuzzyFilter(q.value, list, (a) => a.title, 5)
})

function folderAction(what: 'markAllRead' | 'empty' | 'labelAi') {
  // Handled by FolderActions in the list header (it owns the confirm dialog).
  setTimeout(() => window.dispatchEvent(new CustomEvent('gomail:folder-action', { detail: what })), 50)
}

function replyTo(m: MessageSummary, mode: 'reply' | 'replyAll' | 'forward') {
  void compose.respondToThread(mode, m.id)
}

// ---- selection handlers
function close() {
  ui.paletteOpen = false
}

function openResult(r: SearchResult) {
  pushRecent(query.value)
  ui.mainView = 'mail' // also when the palette was opened from the calendar
  const m = r.message
  const folder = m.folderIds.map((id) => accounts.folderMap.get(id)).find((f) => f && f.kind !== 'custom') ?? accounts.folderOf(m.accountId, 'inbox')
  if (mail.byId.has(m.id)) mail.open(m.id)
  // Shown right away; the list loads around it (marked read, and j/k go on, from its row).
  else void mail.reveal(m.id, { accountId: m.accountId, folderId: folder?.id ?? '', kind: folder?.kind ?? 'inbox' })
  close()
}

function showAll() {
  const text = query.value.trim()
  if (!text) return
  pushRecent(text)
  ui.mainView = 'mail'
  mail.setMailbox({ accountId: scopeAccount.value, folderId: '', kind: 'all' })
  mail.search = text
  close()
}

function pickMailbox(x: MailboxItem) {
  ui.mainView = 'mail'
  if (x.isAccount) mail.switchAccount(x.accountId)
  else if (x.key === 'all') mail.switchAccount('')
  else mail.setMailbox({ accountId: x.accountId, folderId: x.folderId, kind: x.kind })
  close()
}

function runAction(a: Action) {
  close()
  setTimeout(a.run, 0)
}

/** Enter (without arrowing to an item) puts the search in the mail list. */
const enterSearches = computed(() => !!q.value && (hasOperator(query.value) || (completions.value.length > 0 && !/(^|\s)\w+:$/.test(q.value))))
/** The user moved the highlight with the arrow keys since the query last changed. */
let navigated = false
watch(query, () => (navigated = false))

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') navigated = true
  if (e.key === 'Enter' && hasMod(e)) {
    e.preventDefault()
    e.stopPropagation()
    showAll()
    return
  }
  // Enter searches the mail list when the query is a search ("from:asana invoice") or has suggestions
  // (Tab completes those). An item picked with the arrow keys still opens.
  if (e.key === 'Enter' && !e.shiftKey && !e.altKey && !navigated && enterSearches.value) {
    e.preventDefault()
    e.stopPropagation()
    showAll()
    return
  }
  // Tab completes like Enter on a suggestion: the highlighted one, else the first.
  if (e.key === 'Tab' && !e.shiftKey && !hasMod(e) && !e.altKey && completions.value.length) {
    const hl = (e.currentTarget as HTMLElement | null)?.querySelector<HTMLElement>('[data-highlighted][data-complete]')
    const c = completions.value[Number(hl?.dataset.complete ?? 0)] ?? completions.value[0]
    e.preventDefault()
    e.stopPropagation()
    complete(c)
  }
}

// Strong local matches (an action/mailbox name) beat purely typo-tolerant mail hits.
const order = computed(() => {
  const weakMail = results.value.length > 0 && results.value.every((r) => r.fuzzy)
  // A command with a word starting with the typed text ("mark" → "Mark as read", "dark" → "Switch to dark theme") beats mail hits.
  const word = q.value.toLowerCase()
  const actionHit = word.length >= 3 && actions.value.some((a) => a.title.toLowerCase().split(/\s+/).some((_w, i, all) => all.slice(i).join(' ').startsWith(word)))
  return weakMail || actionHit || !results.value.length ? ['actions', 'labels', 'mailboxes', 'mail'] : ['labels', 'mail', 'mailboxes', 'actions']
})

const nothing = computed(() => q.value && !searching.value && !results.value.length && !labelItems.value.length && !mailboxItems.value.length && !actions.value.length && !completions.value.length)
const accountOf = (id: string) => accounts.accountMap.get(id)
const tipKeys = shortcutLabel('mod+enter')
</script>

<template>
  <Dialog v-model:open="ui.paletteOpen">
    <DialogContent class="top-[12vh] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-[640px]" :show-close-button="false" @keydown.capture="onKeydown">
      <DialogTitle class="sr-only">Search and commands</DialogTitle>
      <DialogDescription class="sr-only">Search all mail, jump to mailboxes, toggle labels or run actions.</DialogDescription>
      <CmdRoot>
        <CmdInput v-model="query" placeholder="Search mail, labels, mailboxes, actions…" auto-focus class="h-12 text-[15px]">
          <template #icon>
            <LoaderIcon v-if="searching" class="text-muted-foreground size-4 shrink-0 animate-spin" />
            <SearchIcon v-else class="text-muted-foreground size-4 shrink-0" />
          </template>
        </CmdInput>

        <!-- Scope chips -->
        <div class="border-border flex flex-wrap items-center gap-1.5 border-b px-3 py-2">
          <button
            type="button"
            :class="['inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors', scopeAll ? 'bg-foreground text-background border-transparent' : 'border-border text-muted-foreground hover:text-foreground']"
            @click="scopeAll = true"
          >
            <LayersIcon class="size-3" /> All accounts
          </button>
          <button
            v-if="currentAccount"
            type="button"
            :class="['inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors', !scopeAll ? 'bg-foreground text-background border-transparent' : 'border-border text-muted-foreground hover:text-foreground']"
            @click="scopeAll = false"
          >
            <span class="size-2 rounded-full" :style="{ background: currentAccount.color }" /> {{ currentAccount.displayName || currentAccount.email }}
          </button>
        </div>

        <CmdList class="max-h-[min(460px,60vh)]">
          <!-- Empty input: recent searches -->
          <CmdGroup v-if="!q && recent.length" heading="Recent searches">
            <CmdItem v-for="r in recent" :key="r" :value="`recent:${r}`" @select="query = r">
              <ClockIcon class="text-muted-foreground size-4" />
              <span class="flex-1 truncate">{{ r }}</span>
            </CmdItem>
          </CmdGroup>

          <CmdGroup v-if="completions.length" heading="Suggestions">
            <CmdItem v-for="(c, i) in completions" :key="c.value" :value="`complete:${c.value}`" :data-complete="i" @select="complete(c)">
              <span v-if="c.color" class="size-2.5 rounded-full" :style="{ background: c.color }" />
              <SearchIcon v-else class="text-muted-foreground size-4" />
              <span class="truncate">{{ c.label }}</span>
              <span v-if="c.hint" class="text-muted-foreground truncate text-xs">{{ c.hint }}</span>
            </CmdItem>
          </CmdGroup>

          <template v-for="section in order" :key="section">
            <template v-if="section === 'labels'">
            <CmdGroup v-if="labelItems.length" heading="Labels">
              <CmdItem v-for="l in labelItems" :key="l.id" :value="`label:${l.id}`" @select="ui.mainView = 'mail'; mail.toggleLabelFilter(l.id)">
                <span class="size-2.5 rounded-full" :style="{ background: l.color }" />
                <span class="flex-1 truncate">{{ l.name }}</span>
                <span class="text-muted-foreground text-xs">{{ mail.labelFilter.labels.includes(l.id) ? 'Filtering' : 'Filter list' }}</span>
                <CheckIcon class="size-4" :class="mail.labelFilter.labels.includes(l.id) ? 'opacity-100' : 'opacity-0'" />
              </CmdItem>
            </CmdGroup>
            </template>
            <template v-if="section === 'mail'">
            <!-- Server results (backend ranking preserved) -->
            <CmdGroup v-if="results.length" heading="Mail">
              <CmdItem v-for="r in results" :key="r.message.id" :value="`mail:${r.message.id}`" class="items-start py-2" @select="openResult(r)">
                <span class="mt-1.5 size-2 shrink-0 rounded-full" :style="{ background: accountOf(r.message.accountId)?.color ?? 'var(--muted-foreground)' }" :title="accountOf(r.message.accountId)?.email" />
                <span class="min-w-0 flex-1">
                  <span class="flex items-center gap-2">
                    <span :class="['truncate text-[13px]', r.message.unread ? 'font-semibold' : 'font-medium']">{{ displayName(r.message.from) }}</span>
                    <span class="text-muted-foreground ml-auto shrink-0 text-[11px]">{{ listDate(r.message.date) }}</span>
                  </span>
                  <span class="block truncate text-[13px]">{{ r.message.subject || '(no subject)' }}</span>
                  <!-- eslint-disable-next-line vue/no-v-html -- sanitized to <mark> only -->
                  <span class="text-muted-foreground line-clamp-1 text-xs" v-html="safeHighlight(r.highlight)" />
                  <span v-if="matchHint(r)" class="text-muted-foreground mt-0.5 block text-[10.5px]">{{ matchHint(r) }}</span>
                </span>
                <span v-if="r.message.labels.length" class="mt-0.5 flex shrink-0 gap-1">
                  <template v-for="id in r.message.labels.slice(0, 2)" :key="id">
                    <LabelPill v-if="settings.labelMap.get(id)" :label="settings.labelMap.get(id)!" />
                  </template>
                </span>
              </CmdItem>
              <CmdItem :value="'mail:all'" @select="showAll">
                <SearchIcon class="text-muted-foreground size-4" />
                <span class="flex-1">Show all results for “{{ q }}”</span>
                <KeyCaps combo="mod+enter" />
              </CmdItem>
            </CmdGroup>
            </template>
            <template v-if="section === 'mailboxes'">
            <CmdGroup v-if="mailboxItems.length" heading="Mailboxes">
              <CmdItem v-for="(x, i) in mailboxItems" :key="x.key" :value="`mb:${x.key}`" @select="pickMailbox(x)">
                <AccountAvatar v-if="x.isAccount && accountOf(x.accountId)" :account="accountOf(x.accountId)!" :size="18" :glyph="false" />
                <component :is="x.icon" v-else class="text-muted-foreground size-4" />
                <span class="truncate">{{ x.title }}</span>
                <span class="text-muted-foreground truncate text-xs">{{ x.subtitle }}</span>
                <span class="ml-auto" />
                <KeyCaps v-if="x.isAccount && !q && i < 10" :combo="`mod+${accounts.accounts.findIndex((a) => a.id === x.accountId) + 1}`" />
                <KeyCaps v-else-if="x.key === 'all' && !q" combo="mod+shift+0" />
              </CmdItem>
            </CmdGroup>
            </template>
            <template v-if="section === 'actions'">
            <CmdGroup v-if="actions.length" heading="Actions">
              <CmdItem v-for="a in actions" :key="a.id" :value="`act:${a.id}`" @select="runAction(a)">
                <component :is="a.icon" class="text-muted-foreground size-4" :class="a.icon === SparklesIcon && 'text-violet-500'" />
                <span class="flex-1 truncate">{{ a.title }}</span>
                <KeyCaps v-if="a.shortcut" :combo="a.shortcut" />
              </CmdItem>
            </CmdGroup>
            </template>
          </template>

          <div v-if="nothing" class="px-6 py-10 text-center">
            <p class="text-sm font-medium">No results for “{{ q }}”</p>
            <p class="text-muted-foreground mx-auto mt-2 max-w-sm text-xs leading-relaxed">
              Try <code class="bg-muted rounded px-1">from:alex</code>, <code class="bg-muted rounded px-1">label:finance</code>,
              <code class="bg-muted rounded px-1">is:unread</code>, <code class="bg-muted rounded px-1">has:attachment</code> or a <code class="bg-muted rounded px-1">"quoted phrase"</code>.
            </p>
          </div>
          <div v-if="!q && !recent.length" class="text-muted-foreground px-3 pt-1 pb-2 text-xs">
            Tip: search across every account. Use <code class="bg-muted rounded px-1">from:</code>, <code class="bg-muted rounded px-1">label:</code> and <code class="bg-muted rounded px-1">is:unread</code> to narrow down; several <code class="bg-muted rounded px-1">label:</code> terms find mail that has all of them.
          </div>
        </CmdList>

        <div class="border-border text-muted-foreground flex items-center gap-4 border-t px-3 py-2 text-[11px]">
          <span class="flex items-center gap-1"><KeyCaps combo="up" /><KeyCaps combo="down" /> navigate</span>
          <span class="flex items-center gap-1"><CornerDownLeftIcon class="size-3" /> {{ enterSearches ? 'search mail' : 'open' }}</span>
          <span v-if="completions.length" class="flex items-center gap-1"><KeyCaps combo="tab" /> complete</span>
          <span class="flex items-center gap-1">{{ tipKeys }} show all</span>
          <span class="ml-auto flex items-center gap-1"><KeyCaps combo="esc" /> close</span>
        </div>
      </CmdRoot>
    </DialogContent>
  </Dialog>
</template>
