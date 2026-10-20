import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import type { Address, ListQuery, Message, MessageSummary } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useSettingsStore } from './settings'
import { announce } from '@/lib/announce'
import { labelInAccount } from '@/lib/labelScope'

/** accountId "" = unified across accounts (by folder kind). */
export interface Mailbox {
  accountId: string
  folderId: string
  kind: string
}

export interface LabelFilter {
  labels: string[]
  unlabeled: boolean
  /** Hide mail having ANY of these labels (absent in filters saved before it existed). */
  hidden?: string[]
}

const PAGE = 60
/** The backend's page size cap (store.MaxPageSize). */
const MAX_PAGE = 200
/** softRefresh re-fetches at most this many loaded rows. */
const REFRESH_MAX = 1000
/** The backend resolves at most this many ids for "select all" (core.MaxResolveIDs). */
export const MAX_RESOLVE = 100_000
/** Ids per backend action call: the backend takes 5000 messages per action (syncer.MaxBulk),
 *  and a conversation id can stand for several messages. */
export const ACTION_CHUNK = 1000

/** Run a backend action over ids in chunks, so "select all N" acts on all N. */
export async function inChunks(ids: string[], fn: (chunk: string[]) => Promise<unknown>): Promise<void> {
  for (let i = 0; i < ids.length; i += ACTION_CHUNK) await fn(ids.slice(i, i + ACTION_CHUNK))
}

/** An auto-advanced message (after archive/delete) counts as read after this dwell. */
const AUTO_ADVANCE_DWELL_MS = 2500
const FILTERS_KEY = 'gomail.labelFilters.v1'
const MAILBOX_KEY = 'gomail.mailbox.v1'
const LAST_BY_ACCOUNT_KEY = 'gomail.lastMailbox.v1'

export const UNIFIED_NAMES: Record<string, string> = {
  all: 'All mail', inbox: 'All inboxes', starred: 'Starred', sent: 'Sent', drafts: 'Drafts', archive: 'Archive', spam: 'Junk', trash: 'Trash',
}

export function mailboxKey(m: Mailbox): string {
  return `${m.accountId}|${m.folderId || m.kind}`
}

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

function writeJSON(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    /* ignore */
  }
}

export const useMailStore = defineStore('mail', () => {
  const accounts = useAccountsStore()
  const settings = useSettingsStore()

  const storedMailbox = readJSON<Mailbox | null>(MAILBOX_KEY, null)
  const mailbox = ref<Mailbox>(storedMailbox ?? { accountId: '', folderId: '', kind: 'inbox' })
  /** Last mailbox per account ("" = All inboxes), restored when switching accounts. */
  const lastByAccount = ref<Record<string, Mailbox>>(readJSON(LAST_BY_ACCOUNT_KEY, {}))
  /** Scroll offset per mailbox key, restored by the list. */
  const scrollPositions = new Map<string, number>()
  const search = ref('')
  const unreadOnly = ref(false)
  const filters = ref<Record<string, LabelFilter>>(readJSON(FILTERS_KEY, {}))

  const messages = shallowRef<MessageSummary[]>([])
  const nextCursor = ref('')
  const hasMoreRemote = ref(false)
  const loadingOlder = ref(false)
  const total = ref(0)
  const status = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const refreshing = ref(false)
  const loadingMore = ref(false)
  const error = ref('')

  const openId = ref<string | null>(null)
  const selection = shallowRef<Set<string>>(new Set())
  const anchorId = ref<string | null>(null)
  /** "Select all N in this view" — selection is the whole query, not just loaded rows. */
  const selectAllQuery = ref(false)

  const threadCache = new Map<string, Message[]>()
  /** Bumped when a cached thread's messages are patched in place (the reading pane re-reads them). */
  const threadRev = ref(0)
  /** Conversations (thread ids) where the user chose "Load images" this session. */
  const remoteAllowed = shallowRef<Set<string>>(new Set())
  const addressBook = shallowRef<Map<string, Address>>(new Map())

  const labelFilter = computed<Required<LabelFilter>>(() => {
    const f = filters.value[mailboxKey(mailbox.value)]
    return { labels: f?.labels ?? [], unlabeled: !!f?.unlabeled, hidden: f?.hidden ?? [] }
  })
  const hasLabelFilter = computed(() => labelFilter.value.labels.length > 0 || labelFilter.value.unlabeled || labelFilter.value.hidden.length > 0)

  const query = computed<ListQuery>(() => ({
    accountId: mailbox.value.accountId,
    folderId: mailbox.value.folderId,
    folderKind: mailbox.value.folderId || mailbox.value.kind === 'all' ? '' : mailbox.value.kind,
    labels: labelFilter.value.labels,
    unlabeled: labelFilter.value.unlabeled,
    excludeLabels: labelFilter.value.hidden,
    search: search.value.trim(),
    unreadOnly: unreadOnly.value,
    limit: PAGE,
    cursor: '',
  }))

  const byId = computed(() => {
    const m = new Map<string, MessageSummary>()
    for (const x of messages.value) m.set(x.id, x)
    return m
  })

  const indexById = computed(() => {
    const m = new Map<string, number>()
    messages.value.forEach((x, i) => m.set(x.id, i))
    return m
  })

  const openSummary = computed(() => (openId.value ? byId.value.get(openId.value) ?? null : null))
  const hasMore = computed(() => !!nextCursor.value)
  const selectionCount = computed(() => (selectAllQuery.value ? total.value : selection.value.size))
  const isUnified = computed(() => !mailbox.value.accountId)
  /** Mailboxes of the selected mail (label menus offer their labels). "All in this view" in the
   *  unified view, or rows that aren't loaded: [] (unknown, so every label). */
  const selectionAccountIds = computed<string[]>(() => {
    if (selectAllQuery.value) return mailbox.value.accountId ? [mailbox.value.accountId] : []
    const ids = new Set<string>()
    for (const id of selection.value) {
      const a = byId.value.get(id)?.accountId
      if (!a) return mailbox.value.accountId ? [mailbox.value.accountId] : []
      ids.add(a)
    }
    return [...ids]
  })

  const mailboxName = computed(() => {
    const mb = mailbox.value
    if (!mb.accountId || mb.kind === 'all') return UNIFIED_NAMES[mb.kind] ?? 'Mail'
    const f = mb.folderId ? accounts.folderMap.get(mb.folderId) : accounts.folderOf(mb.accountId, mb.kind)
    if (f) return f.kind === 'spam' ? 'Junk' : f.name
    return UNIFIED_NAMES[mb.kind] ?? 'Mail'
  })

  const mailboxAccount = computed(() => (mailbox.value.accountId ? accounts.accountMap.get(mailbox.value.accountId) ?? null : null))

  // ------------------------------------------------------------ loading
  let token = 0
  /** Bumped by every optimistic local change; a refresh that started before one is stale. */
  let mutations = 0

  function learnAddresses(list: MessageSummary[]) {
    const book = new Map(addressBook.value)
    let changed = false
    for (const m of list) {
      for (const a of [m.from, ...m.to]) {
        if (a?.email && !book.has(a.email.toLowerCase())) {
          book.set(a.email.toLowerCase(), a)
          changed = true
        }
      }
    }
    if (changed) addressBook.value = book
  }

  /** Unread count of a filtered/searched view (the folder counters don't apply then); null = use the folder's. */
  const viewUnread = ref<number | null>(null)
  /** Unread mail in the current view: the filtered count, else the folder's counter. */
  const viewUnreadCount = computed(() => viewUnread.value ?? (mailbox.value.accountId
    // folderId can be empty when the mailbox was chosen before the account's folders synced.
    ? (mailbox.value.folderId ? accounts.folderMap.get(mailbox.value.folderId) : accounts.folderOf(mailbox.value.accountId, mailbox.value.kind))?.unread ?? 0
    : accounts.unifiedUnread(mailbox.value.kind)))
  let unreadSeq = 0
  async function refreshViewUnread() {
    const seq = ++unreadSeq
    const q = query.value
    if (!q.search && !q.labels?.length && !q.unlabeled && !q.excludeLabels?.length) {
      viewUnread.value = null
      return
    }
    if (q.unreadOnly) {
      viewUnread.value = total.value
      return
    }
    try {
      const page = await api.listMessages({ ...q, unreadOnly: true, limit: 1, cursor: '' })
      if (seq === unreadSeq) viewUnread.value = page.total
    } catch {
      if (seq === unreadSeq) viewUnread.value = null
    }
  }

  async function reload(opts: { keepList?: boolean } = {}) {
    const t = ++token
    error.value = ''
    if (opts.keepList && messages.value.length) refreshing.value = true
    else {
      status.value = 'loading'
      messages.value = []
    }
    try {
      const page = await api.listMessages(query.value)
      if (t !== token) return
      messages.value = page.messages
      nextCursor.value = page.nextCursor
      hasMoreRemote.value = !!page.hasMoreRemote
      total.value = page.total
      status.value = 'ready'
      learnAddresses(page.messages)
      void refreshViewUnread()
    } catch (e) {
      if (t !== token) return
      error.value = errorMessage(e)
      status.value = 'error'
    } finally {
      if (t === token) refreshing.value = false
    }
  }

  /** Re-fetch what is loaded (after backend changes) without losing scroll/selection. */
  async function softRefresh() {
    if (status.value === 'loading') return
    const t = ++token
    const m0 = mutations
    try {
      // Re-fetch as many rows as are loaded (in backend-sized pages, up to a cap), so rows
      // loaded by scrolling don't vanish on every sync.
      const want = Math.min(Math.max(PAGE, messages.value.length), REFRESH_MAX)
      const first = await api.listMessages({ ...query.value, limit: Math.min(want, MAX_PAGE) })
      if (t !== token) return
      const page = { ...first, messages: [...first.messages] }
      while (page.messages.length < want && page.nextCursor) {
        const more = await api.listMessages({ ...query.value, limit: Math.min(want - page.messages.length, MAX_PAGE), cursor: page.nextCursor })
        if (t !== token) return
        page.messages.push(...more.messages)
        page.nextCursor = more.nextCursor
        page.hasMoreRemote = more.hasMoreRemote
      }
      if (m0 !== mutations) {
        // An optimistic update happened while this was in flight: its data predates it.
        setTimeout(() => void softRefresh(), 150)
        return
      }
      // Keep object identity for unchanged rows so only changed rows re-render.
      const prev = byId.value
      messages.value = page.messages.map((m) => {
        const old = prev.get(m.id)
        return old && JSON.stringify(old) === JSON.stringify(m) ? old : m
      })
      nextCursor.value = page.nextCursor
      hasMoreRemote.value = !!page.hasMoreRemote
      total.value = page.total
      status.value = 'ready'
      learnAddresses(page.messages)
      void refreshViewUnread()
      if (selection.value.size) {
        const ids = new Set(page.messages.map((m) => m.id))
        const next = new Set([...selection.value].filter((id) => ids.has(id)))
        if (next.size !== selection.value.size) selection.value = next
      }
      // A refresh that superseded a keep-list reload also ends its "refreshing" state.
      refreshing.value = false
    } catch {
      /* transient; next event retries */
    }
  }

  async function loadMore() {
    if (!nextCursor.value || loadingMore.value || status.value !== 'ready') return
    loadingMore.value = true
    const t = token
    try {
      const page = await api.listMessages({ ...query.value, cursor: nextCursor.value })
      if (t !== token) return
      const seen = new Set(messages.value.map((m) => m.id))
      messages.value = [...messages.value, ...page.messages.filter((m) => !seen.has(m.id))]
      nextCursor.value = page.nextCursor
      hasMoreRemote.value = !!page.hasMoreRemote
      total.value = page.total
      learnAddresses(page.messages)
    } catch (e) {
      if (t === token) toast.error('Could not load more messages', { description: errorMessage(e) })
    } finally {
      loadingMore.value = false
    }
  }

  /** Past the local cache: ask the backend to fetch older mail from the server. */
  async function loadOlder() {
    if (loadingOlder.value || !hasMoreRemote.value || !hasMethod('LoadOlder')) return
    loadingOlder.value = true
    const key = JSON.stringify(query.value)
    try {
      const added = await api.loadOlder({ ...query.value, cursor: '' })
      if (key !== JSON.stringify(query.value)) return // the view changed meanwhile
      if (!added) hasMoreRemote.value = false
      await softRefresh()
      if (nextCursor.value) await loadMore()
    } catch (e) {
      if (key !== JSON.stringify(query.value)) return
      hasMoreRemote.value = false
      toast.error('Couldn’t load older mail', { description: errorMessage(e) })
    } finally {
      loadingOlder.value = false
    }
  }

  // ------------------------------------------------------------ navigation & filters
  function setMailbox(mb: Mailbox) {
    if (mailboxKey(mb) === mailboxKey(mailbox.value) && !search.value) return
    mailbox.value = mb
    search.value = ''
    clearSelection()
    openId.value = null
    writeJSON(MAILBOX_KEY, mb)
    lastByAccount.value = { ...lastByAccount.value, [mb.accountId]: mb }
    writeJSON(LAST_BY_ACCOUNT_KEY, lastByAccount.value)
  }

  function inboxOf(accountId: string): Mailbox {
    if (!accountId) return { accountId: '', folderId: '', kind: 'inbox' }
    const f = accounts.folderOf(accountId, 'inbox')
    return { accountId, folderId: f?.id ?? '', kind: 'inbox' }
  }

  /** Switch to an account (or "" for All inboxes), restoring its last folder. */
  function switchAccount(accountId: string) {
    const last = lastByAccount.value[accountId]
    const valid = last && (!last.folderId || accounts.folderMap.has(last.folderId))
    setMailbox(valid ? last : inboxOf(accountId))
  }

  function goToKind(kind: string) {
    const acc = mailbox.value.accountId
    if (!acc) return setMailbox({ accountId: '', folderId: '', kind })
    const f = accounts.folderOf(acc, kind)
    if (f) setMailbox({ accountId: acc, folderId: f.id, kind })
  }

  /** Make sure the current mailbox exists (after load / account removal). */
  function ensureValidMailbox() {
    const mb = mailbox.value
    const list = accounts.accounts
    if (!list.length) return
    const accountOk = !mb.accountId || accounts.accountMap.has(mb.accountId)
    const folderOk = !mb.folderId || accounts.folderMap.has(mb.folderId)
    if (!storedMailbox && mb.accountId === '' && mailboxKey(mb) === '|inbox' && !lastByAccount.value['']) {
      // First launch: default to the first account, not the combined view.
      setMailbox(inboxOf(list[0].id))
    } else if (!accountOk || !folderOk) {
      setMailbox(inboxOf(list[0].id))
    }
  }

  function saveScroll(top: number) {
    scrollPositions.set(mailboxKey(mailbox.value), top)
  }

  function savedScroll(): number {
    return scrollPositions.get(mailboxKey(mailbox.value)) ?? 0
  }

  function setLabelFilter(f: LabelFilter) {
    filters.value = { ...filters.value, [mailboxKey(mailbox.value)]: { labels: [...f.labels], unlabeled: f.unlabeled, hidden: [...(f.hidden ?? [])] } }
    writeJSON(FILTERS_KEY, filters.value)
  }

  /** Show only mail with this label (toggle). A label is either shown-only or hidden, never both. */
  function toggleLabelFilter(labelId: string) {
    const f = labelFilter.value
    const labels = f.labels.includes(labelId) ? f.labels.filter((l) => l !== labelId) : [...f.labels, labelId]
    setLabelFilter({ ...f, labels, hidden: f.hidden.filter((l) => l !== labelId) })
  }

  /** Hide mail with this label (toggle). */
  function toggleHiddenLabel(labelId: string) {
    const f = labelFilter.value
    const hidden = f.hidden.includes(labelId) ? f.hidden.filter((l) => l !== labelId) : [...f.hidden, labelId]
    setLabelFilter({ ...f, hidden, labels: f.labels.filter((l) => l !== labelId) })
  }

  function toggleUnlabeledFilter() {
    setLabelFilter({ ...labelFilter.value, unlabeled: !labelFilter.value.unlabeled })
  }

  function clearLabelFilter() {
    setLabelFilter({ labels: [], unlabeled: false, hidden: [] })
  }

  // Reload when the query changes. Filters keep the old list visible while loading.
  watch(
    () => JSON.stringify(query.value),
    (now, before) => {
      const a = JSON.parse(now) as ListQuery
      const b = before ? (JSON.parse(before) as ListQuery) : null
      const sameBox = b && a.accountId === b.accountId && a.folderId === b.folderId && a.folderKind === b.folderKind
      clearSelection()
      void reload({ keepList: !!sameBox })
    },
  )

  // Drop label filters that reference deleted labels, or labels that no longer belong to this mailbox.
  watch(
    () => `${mailbox.value.accountId}|${settings.labels.map((l) => `${l.id}:${l.accountIds?.join('+') ?? ''}`).join(',')}`,
    () => {
      if (!settings.settings) return
      const valid = new Set(settings.labelsForAccount(mailbox.value.accountId).map((l) => l.id))
      const f = labelFilter.value
      if ([...f.labels, ...f.hidden].some((l) => !valid.has(l))) {
        setLabelFilter({ labels: f.labels.filter((l) => valid.has(l)), unlabeled: f.unlabeled, hidden: f.hidden.filter((l) => valid.has(l)) })
      }
    },
  )

  // ------------------------------------------------------------ open & move
  let readTimer: ReturnType<typeof setTimeout> | null = null
  function markOpenedRead(id: string) {
    const m = byId.value.get(id)
    if (!m?.unread) return
    accounts.adjustUnread([m], -1)
    patchLocal([id], { unread: false })
    api.markRead(actionIds([id]), true).catch(() => patchLocal([id], { unread: true }))
    accounts.refreshCounts()
  }

  /** Open a message (marked read per Settings → General: immediately / after 2 s / manually). `auto`: opened for the user after archiving/deleting the previous one;
   *  it only counts as read once they stay on it for a moment (triaging with e/# must not mark
   *  everything it passes as read). */
  function open(id: string | null, opts: { auto?: boolean } = {}) {
    openId.value = id
    if (readTimer) clearTimeout(readTimer)
    readTimer = null
    if (!id) return
    const mode = settings.settings?.markReadMode ?? 'immediate'
    if (mode === 'manual') return
    if (mode === 'immediate' && !opts.auto) markOpenedRead(id)
    else readTimer = setTimeout(() => openId.value === id && markOpenedRead(id), opts.auto ? AUTO_ADVANCE_DWELL_MS : 2000)
  }

  function move(delta: number) {
    const list = messages.value
    if (!list.length) return
    const idx = openId.value ? indexById.value.get(openId.value) ?? -1 : -1
    const next = Math.max(0, Math.min(list.length - 1, idx < 0 ? 0 : idx + delta))
    open(list[next].id)
    if (next >= list.length - 5) void loadMore()
  }

  // ------------------------------------------------------------ selection
  function clearSelection() {
    if (selection.value.size) selection.value = new Set()
    selectAllQuery.value = false
    anchorId.value = null
  }

  function toggleSelect(id: string, opts: { range?: boolean } = {}) {
    selectAllQuery.value = false
    const next = new Set(selection.value)
    // Shift-click with nothing selected yet extends from the open message (like Apple Mail).
    const anchor = anchorId.value && indexById.value.has(anchorId.value) ? anchorId.value : openId.value && indexById.value.has(openId.value) ? openId.value : null
    if (opts.range && anchor) {
      const a = indexById.value.get(anchor)!
      const b = indexById.value.get(id) ?? a
      const [lo, hi] = a < b ? [a, b] : [b, a]
      // The range takes the clicked row's new state: shift-click a selected row to deselect a range.
      const on = !selection.value.has(id)
      for (let i = lo; i <= hi; i++) {
        if (on) next.add(messages.value[i].id)
        else next.delete(messages.value[i].id)
      }
    } else if (next.has(id)) next.delete(id)
    else next.add(id)
    anchorId.value = id
    selection.value = next
  }

  function selectAllLoaded() {
    selection.value = new Set(messages.value.map((m) => m.id))
    selectAllQuery.value = false
  }

  /** Account of ids resolved by targetIdsForQuery in a unified view (Undo needs each one's folder). */
  const resolvedAccount = new Map<string, string>()

  /** All ids in the current view (select all, "mark all as read"). */
  async function targetIdsForQuery(): Promise<string[]> {
    const q = { ...query.value, cursor: '' }
    resolvedAccount.clear()
    let ids: string[]
    if (q.accountId || q.folderId || accounts.accounts.length < 2) ids = await api.resolveQueryIds(q, MAX_RESOLVE)
    else {
      // Unified view: resolve per account, so Undo can send rows that were never loaded back
      // to their own account's folder.
      const per = await Promise.all(accounts.accounts.map(async (a) => [a.id, await api.resolveQueryIds({ ...q, accountId: a.id }, MAX_RESOLVE)] as const))
      ids = []
      for (const [acct, list] of per) {
        for (const id of list) resolvedAccount.set(id, acct)
        ids.push(...list)
      }
      ids = ids.slice(0, MAX_RESOLVE)
    }
    if (ids.length >= MAX_RESOLVE) {
      toast.warning(`Only the newest ${MAX_RESOLVE.toLocaleString()} messages are included`, { description: 'Repeat the action for the rest.' })
    }
    return ids
  }

  /** Ids targeted by a bulk action: explicit selection, or the whole query. */
  async function targetIds(): Promise<string[]> {
    if (selectAllQuery.value) return targetIdsForQuery()
    return [...selection.value]
  }

  /** What a keyboard or palette action applies to: the selection (all N in this view when chosen),
   *  else the open message. */
  async function currentTargets(): Promise<string[]> {
    if (selectAllQuery.value) return targetIdsForQuery()
    if (selection.value.size) return [...selection.value]
    return openId.value ? [openId.value] : []
  }

  /** Run a keyboard/palette action on currentTargets(). The selection is cleared first (its rows
   *  are about to change or leave); `allInView` says it was "all N in this view". */
  async function actOnTargets(fn: (ids: string[], allInView: boolean) => unknown) {
    const allInView = selectAllQuery.value
    const ids = await currentTargets()
    if (!ids.length) return
    if (selection.value.size || selectAllQuery.value) clearSelection()
    await fn(ids, allInView)
  }

  /** Mark the targets read. "All in this view" goes through markAllRead, which also covers mail
   *  that is only on the server (like the bulk bar). */
  function markTargetsRead() {
    if (selectAllQuery.value) {
      clearSelection()
      return markAllRead()
    }
    return actOnTargets((ids) => setRead(ids, true))
  }

  /** Star or unstar? Star unless every one is already starred. "All in this view" can't see the
   *  rows that never loaded, so it stars, except in Starred itself. */
  function shouldStar(ids: string[], allInView = false): boolean {
    if (allInView) return mailbox.value.kind !== 'starred'
    return !ids.length || !ids.every((id) => byId.value.get(id)?.starred)
  }

  /** Backend ids for actions: conversation ids ("c:…") act on the whole thread. */
  function actionIds(ids: string[]): string[] {
    return [...new Set(ids.map((id) => byId.value.get(id)?.conversationId || id))]
  }

  // ------------------------------------------------------------ local patches
  function patchLocal(ids: string[], patch: Partial<MessageSummary> | ((m: MessageSummary) => Partial<MessageSummary>)) {
    const set = new Set(ids)
    let changed = false
    const next = messages.value.map((m) => {
      if (!set.has(m.id)) return m
      changed = true
      return { ...m, ...(typeof patch === 'function' ? patch(m) : patch) }
    })
    if (changed) {
      messages.value = next
      mutations++
    }
    for (const id of ids) {
      const thread = [...threadCache.values()].find((t) => t.some((x) => x.id === id))
      if (thread) {
        const i = thread.findIndex((x) => x.id === id)
        const cur = thread[i]
        thread[i] = { ...cur, ...(typeof patch === 'function' ? patch(cur) : patch) } as Message
        threadRev.value++
      }
    }
  }

  function removeLocal(ids: string[]) {
    const set = new Set(ids)
    const list = messages.value
    if (openId.value && set.has(openId.value)) {
      // Advance to the next remaining message, like most mail apps.
      const idx = indexById.value.get(openId.value) ?? -1
      const after = list.slice(idx + 1).find((m) => !set.has(m.id)) ?? list.slice(0, idx).reverse().find((m) => !set.has(m.id))
      // Through open() so the next message is marked read per Settings, like opening it by hand.
      queueMicrotask(() => open(after?.id ?? null, { auto: true }))
      openId.value = after?.id ?? null
    }
    messages.value = list.filter((m) => !set.has(m.id))
    mutations++
    total.value = Math.max(0, total.value - ids.length)
    if (selection.value.size) selection.value = new Set([...selection.value].filter((id) => !set.has(id)))
  }

  function snapshot(ids: string[]): MessageSummary[] {
    return ids.map((id) => byId.value.get(id)).filter((m): m is MessageSummary => !!m)
  }

  function restoreLocal(items: MessageSummary[]) {
    const set = new Set(messages.value.map((m) => m.id))
    const merged = [...messages.value, ...items.filter((m) => !set.has(m.id))]
    merged.sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0))
    messages.value = merged
    mutations++
  }

  /** Folder id to send a message back to on undo, decided when the action runs. */
  function originFolder(m: MessageSummary): string | undefined {
    if (mailbox.value.folderId) return mailbox.value.folderId
    if (mailbox.value.kind !== 'all') {
      const f = accounts.folderOf(m.accountId, mailbox.value.kind)
      if (f) return f.id
    }
    // Search across all mail: the folder the message is in (not the catch-all archive).
    const own = (m.folderIds ?? []).map((id) => accounts.folderMap.get(id)).filter((f) => !!f)
    return (own.find((f) => !['archive', 'trash', 'spam', 'starred'].includes(f!.kind)) ?? own[0])?.id ?? accounts.folderOf(m.accountId, 'inbox')?.id
  }

  /** Undo of archive/trash/move: put the messages back where they were, then refresh.
   *  `others`: ids beyond the loaded rows ("select all"); they go back to the folder shown. */
  function undoMove(items: MessageSummary[], others: string[] = []) {
    const key = mailboxKey(mailbox.value)
    const origins = new Map(items.map((m) => [m.id, originFolder(m)] as const))
    // Rows never loaded go back to the folder shown (in a unified view: their account's folder of that kind).
    const mb = mailbox.value
    for (const id of others) {
      const acct = mb.accountId || resolvedAccount.get(id) || ''
      const f = mb.folderId || (mb.kind !== 'all' && acct ? accounts.folderOf(acct, mb.kind)?.id : undefined)
      if (f) origins.set(id, f)
    }
    const otherSet = new Set(others)
    return async () => {
      // Rows only come back if the same list is still shown.
      if (mailboxKey(mailbox.value) === key) restoreLocal(items)
      const groups = new Map<string, string[]>()
      const add = (f: string | undefined, id: string) => {
        if (!f) return
        const list = groups.get(f)
        if (list) list.push(id)
        else groups.set(f, [id])
      }
      for (const m of items) add(origins.get(m.id), m.conversationId || m.id)
      for (const id of otherSet) add(origins.get(id), id)
      await Promise.all([...groups].map(([f, ids]) => inChunks(ids, (c) => api.moveToFolder(c, f)))).catch((e) => toast.error('Undo failed', { description: errorMessage(e) }))
      void softRefresh()
    }
  }

  function plural(n: number, word: string) {
    return `${n.toLocaleString()} ${word}${n === 1 ? '' : 's'}`
  }
  /** Rows are conversations, or single messages with Settings → conversation view off. */
  function rows(n: number) {
    return plural(n, settings.settings?.conversationView === false ? 'message' : 'conversation')
  }
  /** "Conversation archived" / "3 conversations archived". */
  function done(n: number, what: string) {
    const s = n === 1 ? rows(1).replace(/^1 /, '') : rows(n)
    return `${s[0].toUpperCase()}${s.slice(1)} ${what}`
  }

  async function archive(ids: string[]) {
    if (!ids.length) return
    const backendIds = actionIds(ids) // before the rows leave the list
    const items = snapshot(ids)
    const undo = undoMove(items, ids.filter((id) => !byId.value.has(id)))
    const removes = mailbox.value.kind !== 'archive'
    if (removes) removeLocal(ids)
    try {
      await inChunks(backendIds, api.archive)
      announce(`${rows(ids.length)} archived`)
      toast(done(ids.length, 'archived'), {
        action: { label: 'Undo', onClick: undo },
      })
    } catch (e) {
      if (removes) restoreLocal(items)
      toast.error('Archive failed', { description: errorMessage(e) })
      void softRefresh() // earlier chunks may have gone through
    }
    accounts.refreshCounts()
  }

  async function trash(ids: string[]) {
    if (!ids.length) return
    if (mailbox.value.kind === 'trash') {
      toast('Already in Trash', { description: 'Messages in Trash are deleted for good when Trash is emptied.' })
      return
    }
    const backendIds = actionIds(ids) // before the rows leave the list
    const items = snapshot(ids)
    const undo = undoMove(items, ids.filter((id) => !byId.value.has(id)))
    removeLocal(ids)
    try {
      await inChunks(backendIds, api.trash)
      announce(`${rows(ids.length)} moved to Trash`)
      toast(done(ids.length, 'moved to Trash'), {
        action: { label: 'Undo', onClick: undo },
      })
    } catch (e) {
      restoreLocal(items)
      toast.error('Delete failed', { description: errorMessage(e) })
      void softRefresh() // earlier chunks may have gone through
    }
    accounts.refreshCounts()
  }

  /** Move to each message's own account's Junk folder, with Undo (works across accounts, like Trash). */
  async function junk(ids: string[]) {
    if (!ids.length) return
    if (mailbox.value.kind === 'spam') {
      toast('Already in Junk')
      return
    }
    const backendIds = actionIds(ids) // before the rows leave the list
    const items = snapshot(ids)
    const undo = undoMove(items, ids.filter((id) => !byId.value.has(id)))
    removeLocal(ids)
    try {
      await inChunks(backendIds, api.moveToJunk)
      announce(`${rows(ids.length)} moved to Junk`)
      toast(done(ids.length, 'moved to Junk'), {
        action: { label: 'Undo', onClick: undo },
      })
    } catch (e) {
      restoreLocal(items)
      toast.error('Move to Junk failed', { description: errorMessage(e) })
      void softRefresh() // earlier chunks may have gone through
    }
    accounts.refreshCounts()
  }

  /** "Not junk": back to the Inbox of each message's own account, with Undo. */
  async function notJunk(ids: string[]) {
    const groups = new Map<string, string[]>()
    for (const id of ids) {
      const acct = byId.value.get(id)?.accountId || resolvedAccount.get(id) || mailbox.value.accountId
      const inbox = acct ? accounts.folderOf(acct, 'inbox')?.id : undefined
      if (!inbox) continue
      const list = groups.get(inbox)
      if (list) list.push(id)
      else groups.set(inbox, [id])
    }
    const moved = [...groups.values()].flat()
    if (!moved.length) return
    const backend = [...groups].map(([folder, list]) => [folder, actionIds(list)] as const) // before the rows leave the list
    const items = snapshot(moved)
    const undo = undoMove(items, moved.filter((id) => !byId.value.has(id)))
    removeLocal(moved)
    try {
      for (const [folder, list] of backend) await inChunks(list, (c) => api.moveToFolder(c, folder))
      announce(`${rows(moved.length)} moved to Inbox`)
      toast(done(moved.length, 'moved to Inbox'), { action: { label: 'Undo', onClick: undo } })
    } catch (e) {
      restoreLocal(items)
      toast.error('Move failed', { description: errorMessage(e) })
      void softRefresh() // earlier chunks may have gone through
    }
    accounts.refreshCounts()
  }

  /** Move to a folder of the messages' account, with Undo. */
  async function moveToFolder(ids: string[], folderId: string) {
    const folder = accounts.folderMap.get(folderId)
    // A folder belongs to one mailbox: mail of other accounts can't go there. Say so rather
    // than moving part of the selection silently.
    const skipped = folder ? ids.filter((id) => { const m = byId.value.get(id); return !!m && m.accountId !== folder.accountId }) : []
    if (skipped.length) ids = ids.filter((id) => !skipped.includes(id))
    if (!ids.length) {
      if (skipped.length) toast(`“${folder!.name}” is a folder of another mailbox`, { description: 'Mail can only move to folders of its own account.' })
      return
    }
    const backendIds = actionIds(ids)
    const items = snapshot(ids)
    const undo = undoMove(items, ids.filter((id) => !byId.value.has(id)))
    const name = folder ? (folder.kind === 'spam' ? 'Junk' : folder.name) : 'folder'
    removeLocal(ids)
    try {
      await inChunks(backendIds, (c) => api.moveToFolder(c, folderId))
      announce(`${rows(ids.length)} moved to ${name}`)
      toast(done(ids.length, `moved to ${name}`), {
        description: skipped.length ? `${rows(skipped.length)} from another mailbox stayed where ${skipped.length === 1 ? 'it was' : 'they were'}.` : undefined,
        action: { label: 'Undo', onClick: undo },
      })
    } catch (e) {
      restoreLocal(items)
      toast.error('Move failed', { description: errorMessage(e) })
      void softRefresh() // earlier chunks may have gone through
    }
    accounts.refreshCounts()
  }

  async function setRead(ids: string[], read: boolean) {
    if (!ids.length) return
    const snap = snapshot(ids)
    const before = snap.map((m) => [m.id, m.unread] as const)
    accounts.adjustUnread(snap.filter((m) => m.unread === read), read ? -1 : 1)
    patchLocal(ids, { unread: !read })
    try {
      await inChunks(actionIds(ids), (c) => api.markRead(c, read))
      if (ids.length > 1 || !openId.value || ids[0] !== openId.value) announce(`${rows(ids.length)} marked as ${read ? 'read' : 'unread'}`)
    } catch (e) {
      for (const [id, unread] of before) patchLocal([id], { unread })
      toast.error('Could not update', { description: errorMessage(e) })
    }
    accounts.refreshCounts()
  }

  /** The concrete folders behind the view (one per account in the unified view). */
  const viewFolderIds = computed(() => {
    if (mailbox.value.folderId) return [mailbox.value.folderId]
    if (mailbox.value.kind === 'all') return []
    return accounts.accounts.map((a) => accounts.folderOf(a.id, mailbox.value.kind)?.id).filter((x): x is string => !!x)
  })

  /** Mark everything in the view as read. A plain folder view is marked on the server, so mail
   *  that was never downloaded (older than the offline window) is included: the folder's unread
   *  counter is the server's, and resolving local ids alone would leave those unread. */
  async function markAllRead() {
    const loaded = messages.value.filter((m) => m.unread).map((m) => m.id)
    patchLocal(loaded, { unread: false })
    try {
      const folderIds = viewFolderIds.value
      if (hasMethod('MarkFolderRead') && !search.value && !hasLabelFilter.value && folderIds.length) await Promise.all(folderIds.map((id) => api.markFolderRead(id)))
      else await inChunks(actionIds(await targetIdsForQuery()), (c) => api.markRead(c, true))
      toast('Marked all as read')
    } catch (e) {
      patchLocal(loaded, { unread: true })
      toast.error('Couldn’t mark as read', { description: errorMessage(e) })
    }
    accounts.refreshCounts()
    void softRefresh()
  }

  async function setStarred(ids: string[], starred: boolean) {
    if (!ids.length) return
    const before = snapshot(ids).map((m) => [m.id, m.starred] as const)
    patchLocal(ids, { starred })
    try {
      await inChunks(actionIds(ids), (c) => api.setStarred(c, starred))
      announce(`${rows(ids.length)} ${starred ? 'starred' : 'unstarred'}`)
      if (mailbox.value.kind === 'starred' && !starred) removeLocal(ids)
    } catch (e) {
      for (const [id, was] of before) patchLocal([id], { starred: was })
      toast.error('Could not update', { description: errorMessage(e) })
    }
  }

  async function addLabel(ids: string[], labelId: string, quiet = false) {
    // Only mail of the label's mailboxes gets it (the backend refuses the rest); rows not loaded are
    // left to the backend.
    const label = settings.labelMap.get(labelId)
    const before = ids.length
    if (label) ids = ids.filter((id) => { const m = byId.value.get(id); return !m || labelInAccount(label, m.accountId) })
    const skipped = before - ids.length
    if (!ids.length) {
      if (label) toast(`“${label.name}” isn’t a label of this mailbox`, { description: 'Choose its mailboxes in Settings → Labels.' })
      return
    }
    patchLocal(ids, (m) => ({
      labels: m.labels.includes(labelId) ? m.labels : [...m.labels, labelId],
      labelDetails: [...(m.labelDetails ?? []).filter((d) => d.labelId !== labelId), { labelId, source: 'manual', confidence: 1 }],
    }))
    try {
      await inChunks(actionIds(ids), (c) => api.addLabel(c, labelId))
      const name = label?.name ?? labelId
      // Part of the selection is in a mailbox without this label: say so, never skip it silently.
      if (skipped) toast(`Label “${name}” added to ${rows(ids.length)}`, { description: `${rows(skipped)} in other mailboxes didn’t get it: “${name}” isn’t one of their labels.` })
      else if (!quiet && ids.length > 1) toast(`Label “${name}” added to ${rows(ids.length)}`)
    } catch (e) {
      toast.error('Could not add label', { description: errorMessage(e) })
      void softRefresh()
    }
    accounts.refreshCounts()
  }

  async function removeLabel(ids: string[], labelId: string, quiet = false) {
    if (!ids.length) return
    patchLocal(ids, (m) => ({
      labels: m.labels.filter((l) => l !== labelId),
      labelDetails: (m.labelDetails ?? []).filter((d) => d.labelId !== labelId),
    }))
    try {
      await inChunks(actionIds(ids), (c) => api.removeLabel(c, labelId))
      if (!quiet && ids.length > 1) toast(`Label “${settings.labelMap.get(labelId)?.name ?? labelId}” removed from ${rows(ids.length)}`)
    } catch (e) {
      toast.error('Could not remove label', { description: errorMessage(e) })
      void softRefresh()
    }
    accounts.refreshCounts()
  }

  // ------------------------------------------------------------ threads
  async function loadThread(id: string): Promise<Message[]> {
    const cached = threadCache.get(id)
    if (cached) return cached
    let thread: Message[]
    try {
      thread = await api.getThread(id)
      if (!thread.length) thread = [await api.getMessage(id)]
    } catch {
      thread = [await api.getMessage(id)]
    }
    threadCache.set(id, thread)
    if (threadCache.size > 40) threadCache.delete(threadCache.keys().next().value as string)
    learnAddresses(thread)
    return thread
  }

  /** Synchronous cache lookup (the reading pane renders cached threads without a loading frame). */
  function cachedThread(id: string): Message[] | undefined {
    return threadCache.get(id)
  }

  /** Swap in refetched bodies (e.g. with remote images) wherever these messages are cached. */
  function updateCachedBodies(list: Pick<Message, 'id' | 'bodyHtml' | 'remoteContentBlocked' | 'hasRemoteContent'>[]) {
    const byMsg = new Map(list.map((m) => [m.id, m]))
    for (const t of threadCache.values()) {
      t.forEach((m, i) => {
        const u = byMsg.get(m.id)
        if (u) t[i] = { ...m, bodyHtml: u.bodyHtml, remoteContentBlocked: u.remoteContentBlocked, hasRemoteContent: u.hasRemoteContent }
      })
    }
  }

  function invalidateThread(id?: string) {
    if (id) threadCache.delete(id)
    else threadCache.clear()
  }

  function suggestAddresses(q: string, limit = 6): Address[] {
    const s = q.trim().toLowerCase()
    if (!s) return []
    const out: Address[] = []
    const own = new Set(accounts.accounts.map((a) => a.email.toLowerCase()))
    for (const a of addressBook.value.values()) {
      if (own.has(a.email.toLowerCase())) continue
      if (a.email.toLowerCase().includes(s) || a.name.toLowerCase().includes(s)) out.push(a)
      if (out.length >= limit) break
    }
    return out
  }

  return {
    mailbox, search, unreadOnly, filters, messages, viewUnread, threadRev, nextCursor, hasMoreRemote, loadingOlder, loadOlder, total, status, refreshing, loadingMore, error,
    openId, selection, anchorId, selectAllQuery, addressBook, remoteAllowed,
    labelFilter, hasLabelFilter, viewUnreadCount, query, byId, indexById, openSummary, hasMore, selectionCount, isUnified, selectionAccountIds, mailboxName, mailboxAccount,
    lastByAccount, switchAccount, goToKind, inboxOf, ensureValidMailbox, saveScroll, savedScroll,
    reload, softRefresh, loadMore, setMailbox, setLabelFilter, toggleLabelFilter, toggleHiddenLabel, toggleUnlabeledFilter, clearLabelFilter,
    actionIds, currentTargets, actOnTargets, markTargetsRead, shouldStar, open, move, moveToFolder, clearSelection, toggleSelect, selectAllLoaded, targetIds, targetIdsForQuery, patchLocal, removeLocal,
    archive, trash, junk, notJunk, setRead, markAllRead, viewFolderIds, setStarred, addLabel, removeLabel, loadThread, cachedThread, updateCachedBodies, invalidateThread, suggestAddresses,
  }
})
