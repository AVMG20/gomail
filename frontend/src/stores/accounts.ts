import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { api, hasMethod, readLocal, writeLocal } from '@/lib/api'
import type { Account, Folder, LabelCount, SyncProgress } from '@/lib/types'

export interface LabelScope {
  accountId: string
  folderId: string
  folderKind: string
}

export const SYSTEM_ORDER = ['inbox', 'starred', 'sent', 'drafts', 'archive', 'spam', 'trash']

export const useAccountsStore = defineStore('accounts', () => {
  const accounts = ref<Account[]>([])
  const folders = shallowRef<Record<string, Folder[]>>({})
  /** Label counts for the active scope (account id, or "" for all accounts). */
  const labelCounts = shallowRef<Map<string, LabelCount>>(new Map())
  /** Mailbox-wide totals (the scope's account, every folder): which imported labels to list at all. */
  const labelTotals = shallowRef<Map<string, LabelCount>>(new Map())
  /** The box the sidebar's label counts are for (the list on screen): clicking a label filters it. */
  const labelScope = shallowRef<LabelScope>({ accountId: '', folderId: '', folderKind: '' })
  const loaded = ref(false)
  const syncing = ref<Set<string>>(new Set())
  /** Latest sync:progress per account (cleared when the sync finishes). */
  const progress = ref<Record<string, SyncProgress>>({})
  /** network:status from the backend (true until told otherwise). */
  const online = ref(true)

  const accountMap = computed(() => new Map(accounts.value.map((a) => [a.id, a])))
  const anySyncing = computed(() => syncing.value.size > 0 || accounts.value.some((a) => a.syncing))
  const folderMap = computed(() => {
    const m = new Map<string, Folder>()
    for (const list of Object.values(folders.value)) for (const f of list) m.set(f.id, f)
    return m
  })

  function sortFolders(list: Folder[]): Folder[] {
    return [...list].sort((a, b) => {
      const ia = SYSTEM_ORDER.indexOf(a.kind)
      const ib = SYSTEM_ORDER.indexOf(b.kind)
      if (ia !== ib) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
      return a.name.localeCompare(b.name)
    })
  }

  let foldersSeq = 0
  let countsSeq = 0

  /** Move folder unread counters right away (read/unread); refreshCounts() then corrects them. */
  function adjustUnread(rows: { folderIds: string[] }[], delta: 1 | -1) {
    const per = new Map<string, number>()
    for (const r of rows) for (const f of r.folderIds ?? []) per.set(f, (per.get(f) ?? 0) + delta)
    if (!per.size) return
    const next: typeof folders.value = {}
    for (const [acct, list] of Object.entries(folders.value)) {
      next[acct] = list.map((f) => (per.has(f.id) ? { ...f, unread: Math.max(0, f.unread + per.get(f.id)!) } : f))
    }
    folders.value = next
  }
  async function loadFolders(ids = accounts.value.map((a) => a.id)) {
    const seq = ++foldersSeq
    const results = await Promise.all(ids.map(async (id) => [id, sortFolders(await api.getFolders(id))] as const))
    if (seq !== foldersSeq) return // a newer load is on its way (out-of-order responses)
    const next = { ...folders.value }
    for (const [id, list] of results) next[id] = list
    for (const id of Object.keys(next)) if (!accounts.value.some((a) => a.id === id)) delete next[id]
    folders.value = next
  }

  /** Deletes one of the user's own folders on the server; its mail goes to Trash. */
  async function deleteFolder(id: string) {
    const f = folderMap.value.get(id)
    await api.deleteFolder(id)
    if (f) await loadFolders([f.accountId])
    refreshCounts()
  }

  async function loadLabelCounts(scope: LabelScope = labelScope.value) {
    labelScope.value = scope
    const seq = ++countsSeq
    try {
      const [counts, totals] = await Promise.all([api.getViewLabelCounts(scope), api.getLabelCounts(scope.accountId)])
      // Compare by value: a stale reply (another box) must not overwrite the counts.
      if (JSON.stringify(scope) !== JSON.stringify(labelScope.value) || seq !== countsSeq) return
      labelCounts.value = new Map(counts.map((c) => [c.labelId, c]))
      labelTotals.value = new Map(totals.map((c) => [c.labelId, c]))
    } catch {
      /* optional binding */
    }
  }

  /** Signatures fall back to local storage until the backend stores them. */
  function withSignatures(list: Account[]): Account[] {
    return list.map((a) => (a.signature === undefined || a.signature === null ? { ...a, signature: readLocal<string>(`sig:${a.id}`, '') } : a))
  }

  async function saveSignature(id: string, html: string) {
    const a = accounts.value.find((x) => x.id === id)
    if (a) a.signature = html
    if (hasMethod('UpdateAccountSignature')) await api.updateAccountSignature(id, html)
    else writeLocal(`sig:${id}`, html)
  }

  async function load() {
    accounts.value = withSignatures(await api.getAccounts())
    await Promise.all([loadFolders(), loadLabelCounts()])
    loaded.value = true
  }

  async function reloadAccounts() {
    accounts.value = withSignatures(await api.getAccounts())
  }

  /** Counts (folder unread, label counts, account unread) after mail changes. */
  const refreshCounts = useDebounceFn(async () => {
    await Promise.all([reloadAccounts(), loadFolders(), loadLabelCounts()])
  }, 400)

  function folderOf(accountId: string, kind: string): Folder | undefined {
    return folders.value[accountId]?.find((f) => f.kind === kind)
  }

  /** Unread mail of a folder kind over all mailboxes; skip leaves out mailboxes (muted ones on badges). */
  function unifiedUnread(kind: string, skip?: ReadonlySet<string>): number {
    let n = 0
    for (const a of accounts.value) if (!skip?.has(a.id)) n += folderOf(a.id, kind)?.unread ?? 0
    return n
  }

  function setProgress(p: SyncProgress | null, accountId = '') {
    const next = { ...progress.value }
    if (p) next[p.accountId] = p
    else delete next[accountId]
    progress.value = next
  }

  function setSyncing(id: string, on: boolean) {
    const s = new Set(syncing.value)
    if (on) s.add(id)
    else {
      s.delete(id)
      if (progress.value[id]) setProgress(null, id)
    }
    syncing.value = s
  }

  async function sync(id = '') {
    const ids = id ? [id] : accounts.value.map((a) => a.id)
    ids.forEach((x) => setSyncing(x, true))
    try {
      if (id) await api.syncNow(id)
      else await Promise.all(ids.map((x) => api.syncNow(x)))
    } catch (e) {
      ids.forEach((x) => setSyncing(x, false))
      throw e
    }
  }

  /** Moves a mailbox to another place in the list (rail, settings, mod+1…9), optimistic. */
  async function moveAccount(from: number, to: number) {
    const list = [...accounts.value]
    if (from === to || from < 0 || from >= list.length) return
    const [a] = list.splice(from, 1)
    list.splice(Math.max(0, Math.min(to, list.length)), 0, a)
    const prev = accounts.value
    accounts.value = list
    try {
      await api.reorderAccounts(list.map((x) => x.id))
    } catch (e) {
      accounts.value = prev
      throw e
    }
  }

  /** Mailbox color (avatar, accent, calendar group), optimistic. */
  async function setAccountColor(id: string, color: string) {
    const a = accounts.value.find((x) => x.id === id)
    if (!a || a.color === color) return
    const prev = a.color
    a.color = color
    try {
      await api.updateAccount(id, a.displayName, color)
    } catch (e) {
      a.color = prev
      throw e
    }
  }

  return {
    setAccountColor, moveAccount,
    accounts, folders, labelCounts, labelTotals, labelScope, loaded, syncing, progress, setProgress, online, accountMap, anySyncing, folderMap,
    load, saveSignature, loadFolders, deleteFolder, adjustUnread, loadLabelCounts, reloadAccounts, refreshCounts, folderOf, unifiedUnread, setSyncing, sync,
  }
})
