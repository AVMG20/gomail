import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage } from '@/lib/api'
import type { Address, ListQuery, Message, MessageSummary } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useSettingsStore } from './settings'

/** accountId "" = unified across accounts (by folder kind). */
export interface Mailbox {
  accountId: string
  folderId: string
  kind: string
}

export interface LabelFilter {
  labels: string[]
  unlabeled: boolean
}

const PAGE = 60
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
  const addressBook = shallowRef<Map<string, Address>>(new Map())

  const labelFilter = computed<LabelFilter>(() => filters.value[mailboxKey(mailbox.value)] ?? { labels: [], unlabeled: false })
  const hasLabelFilter = computed(() => labelFilter.value.labels.length > 0 || labelFilter.value.unlabeled)

  const query = computed<ListQuery>(() => ({
    accountId: mailbox.value.accountId,
    folderId: mailbox.value.folderId,
    folderKind: mailbox.value.folderId || mailbox.value.kind === 'all' ? '' : mailbox.value.kind,
    labels: labelFilter.value.labels,
    unlabeled: labelFilter.value.unlabeled,
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
      total.value = page.total
      status.value = 'ready'
      learnAddresses(page.messages)
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
    try {
      const limit = Math.min(Math.max(PAGE, messages.value.length), 500)
      const page = await api.listMessages({ ...query.value, limit })
      if (t !== token) return
      // Keep object identity for unchanged rows so only changed rows re-render.
      const prev = byId.value
      messages.value = page.messages.map((m) => {
        const old = prev.get(m.id)
        return old && JSON.stringify(old) === JSON.stringify(m) ? old : m
      })
      nextCursor.value = page.nextCursor
      total.value = page.total
      status.value = 'ready'
      learnAddresses(page.messages)
      if (selection.value.size) {
        const ids = new Set(page.messages.map((m) => m.id))
        const next = new Set([...selection.value].filter((id) => ids.has(id)))
        if (next.size !== selection.value.size) selection.value = next
      }
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
      total.value = page.total
      learnAddresses(page.messages)
    } catch (e) {
      toast.error('Could not load more messages', { description: errorMessage(e) })
    } finally {
      loadingMore.value = false
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
    filters.value = { ...filters.value, [mailboxKey(mailbox.value)]: { labels: [...f.labels], unlabeled: f.unlabeled } }
    writeJSON(FILTERS_KEY, filters.value)
  }

  function toggleLabelFilter(labelId: string) {
    const f = labelFilter.value
    const labels = f.labels.includes(labelId) ? f.labels.filter((l) => l !== labelId) : [...f.labels, labelId]
    setLabelFilter({ labels, unlabeled: f.unlabeled })
  }

  function toggleUnlabeledFilter() {
    setLabelFilter({ labels: labelFilter.value.labels, unlabeled: !labelFilter.value.unlabeled })
  }

  function clearLabelFilter() {
    setLabelFilter({ labels: [], unlabeled: false })
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

  // Drop label filters that reference deleted labels.
  watch(
    () => settings.labels.map((l) => l.id).join(','),
    () => {
      if (!settings.settings) return
      const valid = new Set(settings.labels.map((l) => l.id))
      const f = labelFilter.value
      if (f.labels.some((l) => !valid.has(l))) setLabelFilter({ labels: f.labels.filter((l) => valid.has(l)), unlabeled: f.unlabeled })
    },
  )

  // ------------------------------------------------------------ open & move
  function open(id: string | null) {
    openId.value = id
    if (!id) return
    const m = byId.value.get(id)
    if (m?.unread) {
      patchLocal([id], { unread: false })
      api.markRead([id], true).catch(() => patchLocal([id], { unread: true }))
      accounts.refreshCounts()
    }
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
    if (opts.range && anchorId.value && indexById.value.has(anchorId.value)) {
      const a = indexById.value.get(anchorId.value)!
      const b = indexById.value.get(id) ?? a
      const [lo, hi] = a < b ? [a, b] : [b, a]
      const on = !selection.value.has(id) || true
      for (let i = lo; i <= hi; i++) {
        if (on) next.add(messages.value[i].id)
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

  /** Ids targeted by a bulk action: explicit selection, or the whole query. */
  async function targetIds(): Promise<string[]> {
    if (selectAllQuery.value) return api.resolveQueryIds({ ...query.value, cursor: '' }, 10000)
    return [...selection.value]
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
    if (changed) messages.value = next
    for (const id of ids) {
      const thread = [...threadCache.values()].find((t) => t.some((x) => x.id === id))
      if (thread) {
        const i = thread.findIndex((x) => x.id === id)
        const cur = thread[i]
        thread[i] = { ...cur, ...(typeof patch === 'function' ? patch(cur) : patch) } as Message
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
      openId.value = after?.id ?? null
    }
    messages.value = list.filter((m) => !set.has(m.id))
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
  }

  /** Folder id to send messages back to on undo. */
  function originFolder(m: MessageSummary): string | undefined {
    if (mailbox.value.folderId) return mailbox.value.folderId
    return accounts.folderOf(m.accountId, mailbox.value.kind)?.id ?? accounts.folderOf(m.accountId, 'inbox')?.id
  }

  async function moveBack(items: MessageSummary[]) {
    const groups = new Map<string, string[]>()
    for (const m of items) {
      const f = originFolder(m)
      if (!f) continue
      groups.set(f, [...(groups.get(f) ?? []), m.id])
    }
    await Promise.all([...groups].map(([f, ids]) => api.moveToFolder(ids, f)))
  }

  function plural(n: number, word: string) {
    return `${n} ${word}${n === 1 ? '' : 's'}`
  }

  async function archive(ids: string[]) {
    if (!ids.length) return
    const items = snapshot(ids)
    const removes = mailbox.value.kind !== 'archive'
    if (removes) removeLocal(ids)
    try {
      await api.archive(ids)
      toast(`${ids.length === 1 ? 'Conversation' : plural(ids.length, 'conversation')} archived`, {
        action: {
          label: 'Undo',
          onClick: async () => {
            restoreLocal(items)
            await moveBack(items).catch((e) => toast.error('Undo failed', { description: errorMessage(e) }))
            void softRefresh()
          },
        },
      })
    } catch (e) {
      if (removes) restoreLocal(items)
      toast.error('Archive failed', { description: errorMessage(e) })
    }
    accounts.refreshCounts()
  }

  async function trash(ids: string[]) {
    if (!ids.length) return
    const items = snapshot(ids)
    const removes = mailbox.value.kind !== 'trash'
    if (removes) removeLocal(ids)
    try {
      await api.trash(ids)
      toast(`${ids.length === 1 ? 'Moved' : plural(ids.length, 'message') + ' moved'} to Trash`, {
        action: {
          label: 'Undo',
          onClick: async () => {
            restoreLocal(items)
            await moveBack(items).catch((e) => toast.error('Undo failed', { description: errorMessage(e) }))
            void softRefresh()
          },
        },
      })
    } catch (e) {
      if (removes) restoreLocal(items)
      toast.error('Delete failed', { description: errorMessage(e) })
    }
    accounts.refreshCounts()
  }

  async function setRead(ids: string[], read: boolean) {
    if (!ids.length) return
    const before = snapshot(ids).map((m) => [m.id, m.unread] as const)
    patchLocal(ids, { unread: !read })
    try {
      await api.markRead(ids, read)
    } catch (e) {
      for (const [id, unread] of before) patchLocal([id], { unread })
      toast.error('Could not update', { description: errorMessage(e) })
    }
    accounts.refreshCounts()
  }

  async function setStarred(ids: string[], starred: boolean) {
    if (!ids.length) return
    patchLocal(ids, { starred })
    try {
      await api.setStarred(ids, starred)
    } catch (e) {
      patchLocal(ids, { starred: !starred })
      toast.error('Could not update', { description: errorMessage(e) })
    }
    if (mailbox.value.kind === 'starred' && !starred) removeLocal(ids)
  }

  async function addLabel(ids: string[], labelId: string, quiet = false) {
    if (!ids.length) return
    patchLocal(ids, (m) => ({
      labels: m.labels.includes(labelId) ? m.labels : [...m.labels, labelId],
      labelDetails: [...(m.labelDetails ?? []).filter((d) => d.labelId !== labelId), { labelId, source: 'manual', confidence: 1 }],
    }))
    try {
      await api.addLabel(ids, labelId)
      if (!quiet && ids.length > 1) toast(`Label “${settings.labelMap.get(labelId)?.name ?? labelId}” added to ${plural(ids.length, 'message')}`)
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
      await api.removeLabel(ids, labelId)
      if (!quiet && ids.length > 1) toast(`Label removed from ${plural(ids.length, 'message')}`)
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
    mailbox, search, unreadOnly, filters, messages, nextCursor, total, status, refreshing, loadingMore, error,
    openId, selection, anchorId, selectAllQuery, addressBook,
    labelFilter, hasLabelFilter, query, byId, indexById, openSummary, hasMore, selectionCount, isUnified, mailboxName, mailboxAccount,
    lastByAccount, switchAccount, goToKind, inboxOf, ensureValidMailbox, saveScroll, savedScroll,
    reload, softRefresh, loadMore, setMailbox, setLabelFilter, toggleLabelFilter, toggleUnlabeledFilter, clearLabelFilter,
    open, move, clearSelection, toggleSelect, selectAllLoaded, targetIds, patchLocal, removeLocal,
    archive, trash, setRead, setStarred, addLabel, removeLabel, loadThread, invalidateThread, suggestAddresses,
  }
})
