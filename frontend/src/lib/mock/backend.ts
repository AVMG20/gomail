// In-memory mock of the Go backend, used when the UI runs in a plain browser
// (`npm run dev` without Wails). It mirrors the bound App methods closely
// enough that every screen can be demoed and tested.
import { parseSearch, type ParsedSearch } from '../searchQuery'
import { remoteImagesTrusted } from '../trust'
import { isFreeMailDomain, domainOf, type RuleRef, type SenderScope, type JunkSenderResult, type LabelSenderResult, type NotJunkResult } from '../junk'
import { groupSenders, type SenderGrouping, type SenderStat, type SenderSummary } from '../senders'
import { fromWireInput, MockCalendar } from './calendar'
import { labelInAccount, labelInMailbox } from '../labelScope'
import type {
  Account, AIModel, AppInfo, BackupResult, RestoreSummary, ClassificationResult, ClassifyEstimate, ClassifyJob, ComposeRequest, DefaultPrompts,
  AttachmentData, EventName, EventPayloads, Folder, LabelCount, ListQuery, Message, MessageLabel, MessagePage, MessageSummary,
  SearchQuery, SearchResult, Settings, Calendar, CalendarEvent, EventInput, RecurrenceScope, AttendeeResponse, EventDraft, AutoMove,
} from '../types'
import {
  DEFAULT_CLASSIFY_INSTRUCTIONS, DEFAULT_GRAMMAR_PROMPT, INCOMING, MOCK_MODELS, PROVIDER_LABELS, SEED_ACCOUNTS, SEED_LABELS, SEED_SETTINGS, mockLabelId,
  asTemplateAccount, buildMessage, buildSeedMessages, buildStressMessages, buildWelcome, foldersFor, SAMPLE_PDF, SAMPLE_PNG,
} from './data'

type Listener = (payload: unknown) => void

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))
const latency = (min = 60, max = 200) => sleep(min + Math.random() * (max - min))

const NEVER_LABEL = new Set(['drafts', 'trash'])

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string)
}

function levenshtein(a: string, b: string, max = 2): number {
  if (Math.abs(a.length - b.length) > max) return max + 1
  const prev = new Array(b.length + 1).fill(0).map((_, i) => i)
  for (let i = 1; i <= a.length; i++) {
    let diag = prev[0]
    prev[0] = i
    let rowMin = prev[0]
    for (let j = 1; j <= b.length; j++) {
      const tmp = prev[j]
      prev[j] = Math.min(prev[j] + 1, prev[j - 1] + 1, diag + (a[i - 1] === b[j - 1] ? 0 : 1))
      diag = tmp
      rowMin = Math.min(rowMin, prev[j])
    }
    if (rowMin > max) return max + 1
  }
  return prev[b.length]
}

function hash(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619) >>> 0
  return h
}

function words(s: string): string[] {
  return s.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean)
}

class MockBackend {
  private accounts: Account[] = clone(SEED_ACCOUNTS)
  private folders = new Map<string, Folder[]>()
  private messages = new Map<string, Message>()
  private settings: Settings = clone(SEED_SETTINGS)
  private providerLabels = clone(PROVIDER_LABELS)
  private listeners = new Map<string, Set<Listener>>()
  private addAccountCancel: (() => void) | null = null
  private jobs = new Map<string, { cancelled: boolean }>()
  private changedTimer: ReturnType<typeof setTimeout> | null = null
  private incomingIdx = 0
  private jobSeq = 0
  private cal = new MockCalendar((accountId) => this.emit('calendar:changed', { accountId }))

  constructor() {
    for (const a of this.accounts) this.folders.set(a.id, foldersFor(a.id, a.provider))
    const stress = typeof location !== 'undefined' ? Number(new URLSearchParams(location.search).get('stress') || 0) : 0
    const seed = stress ? buildStressMessages(stress === 1 ? 20000 : stress) : buildSeedMessages()
    for (const m of seed) this.messages.set(m.id, m)
    if (typeof location !== 'undefined' && new URLSearchParams(location.search).has('reauth')) {
      const work = this.accounts.find((a) => a.id === 'acc_work')
      if (work) work.needsReauth = true
    }
    for (const a of this.accounts) this.remoteOlder.set(a.id, 2)
    // Test flag for the "Connect calendar" flow: ?calconsent=none
    if (this.cal.noConsentFlag()) for (const a of this.accounts) a.calendarConsent = false
  }

  // ---------------------------------------------------------------- events
  on(event: string, cb: Listener): () => void {
    let set = this.listeners.get(event)
    if (!set) this.listeners.set(event, (set = new Set()))
    set.add(cb)
    return () => set!.delete(cb)
  }

  private emit<E extends EventName>(event: E, payload?: EventPayloads[E]) {
    this.listeners.get(event)?.forEach((cb) => {
      try {
        cb(payload)
      } catch (e) {
        console.error(e)
      }
    })
  }

  private changed(accountId = '') {
    this.version++
    if (this.changedTimer) clearTimeout(this.changedTimer)
    this.changedTimer = setTimeout(() => this.emit('messages:changed', { accountId }), 120)
  }

  // ---------------------------------------------------------------- helpers
  private kindOf(folderId: string): string {
    const k = folderId.split(':')[1] ?? ''
    return k === 'label' ? 'custom' : k
  }

  private inExcluded(m: MessageSummary): boolean {
    return m.folderIds.some((f) => ['trash', 'spam'].includes(this.kindOf(f)))
  }

  private neverLabel(m: MessageSummary): boolean {
    return m.folderIds.some((f) => NEVER_LABEL.has(this.kindOf(f)))
  }

  private summary(m: Message): MessageSummary {
    const { cc: _cc, bcc: _bcc, replyTo: _r, bodyHtml: _h, bodyText: _t, attachments: _a, messageId: _mid, inReplyTo: _irt, references: _ref, hasRemoteContent: _hrc, ...s } = m
    return this.visibleLabels(clone(s))
  }

  /** Hide imported provider labels when the import setting is off. */
  private visibleLabels<T extends MessageSummary>(m: T): T {
    if (this.settings.importProviderLabels) return m
    const hidden = new Set(this.providerLabels.map((l) => l.id))
    m.labels = m.labels.filter((l) => !hidden.has(l))
    m.labelDetails = (m.labelDetails ?? []).filter((d) => !hidden.has(d.labelId))
    return m
  }

  private allLabels() {
    return [...this.settings.labels, ...(this.settings.importProviderLabels ? this.providerLabels : [])]
  }

  private labelNameToId(name: string): string | undefined {
    const n = name.toLowerCase()
    return this.allLabels().find((l) => l.id === n || l.name.toLowerCase() === n || l.name.toLowerCase().replace(/\s+/g, '-') === n)?.id
  }

  private matchSearch(m: Message, p: ParsedSearch): { ok: boolean; score: number; matchedIn: string; fuzzy: boolean } {
    const fromStr = `${m.from.name} ${m.from.email}`.toLowerCase()
    const toStr = m.to.map((a) => `${a.name} ${a.email}`).join(' ').toLowerCase()
    const subject = m.subject.toLowerCase()
    const body = (m.bodyText || m.snippet).toLowerCase()
    if (p.from.length && !p.from.every((f) => fromStr.includes(f))) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.to.length && !p.to.every((f) => toStr.includes(f))) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.subject.length && !p.subject.every((f) => subject.includes(f))) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.unread !== undefined && m.unread !== p.unread) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.starred && !m.starred) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.hasAttachment && !m.hasAttachments) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.before && Date.parse(m.date) >= p.before) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.after && Date.parse(m.date) < p.after) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.inKind && !m.folderIds.some((f) => this.kindOf(f) === p.inKind) && !(p.inKind === 'starred' && m.starred)) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    if (p.labels.length) {
      const ids = p.labels.map((l) => this.labelNameToId(l))
      if (!ids.some((id) => id && m.labels.includes(id))) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    }
    let score = 0
    let matchedIn = ''
    let fuzzy = false
    const fields: [string, string, number][] = [['subject', subject, 3], ['from', fromStr, 2.5], ['to', toStr, 1], ['snippet', body, 1]]
    for (const ph of p.phrases) {
      const f = fields.find(([, v]) => v.includes(ph))
      if (!f) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
      score += f[2] * 1.5
      matchedIn ||= f[0]
    }
    p.terms.forEach((term, i) => {
      const isLast = i === p.terms.length - 1
      let best = 0
      let where = ''
      for (const [name, value, weight] of fields) {
        const ws = words(value)
        if (ws.some((w) => w === term)) { if (weight * 1.2 > best) { best = weight * 1.2; where = name } }
        else if (ws.some((w) => w.startsWith(term) && (isLast || term.length > 2))) { if (weight > best) { best = weight; where = name } }
        else if (value.includes(term)) { if (weight * 0.6 > best) { best = weight * 0.6; where = name } }
        else if (term.length >= 4 && ws.some((w) => levenshtein(w.slice(0, term.length + 1), term, 1) <= 1)) {
          if (weight * 0.4 > best) { best = weight * 0.4; where = name; fuzzy = true }
        }
      }
      if (!best) score = -1
      else if (score >= 0) { score += best; matchedIn ||= where }
    })
    if (score < 0) return { ok: false, score: 0, matchedIn: '', fuzzy: false }
    // mild recency boost
    const ageDays = (Date.now() - Date.parse(m.date)) / 86400000
    score += Math.max(0, 1 - ageDays / 60)
    return { ok: true, score, matchedIn, fuzzy }
  }

  private highlight(m: Message, p: ParsedSearch, matchedIn: string): string {
    const body = (m.bodyText || m.snippet).replace(/\s+/g, ' ')
    const source = matchedIn === 'from' ? `${m.from.name} <${m.from.email}>` : matchedIn === 'subject' ? m.subject : body
    const needles = [...p.phrases, ...p.terms, ...p.from, ...p.subject].filter((n) => n.length > 0)
    const lower = source.toLowerCase()
    let start = 0
    const first = needles.map((n) => lower.indexOf(n)).filter((i) => i >= 0).sort((a, b) => a - b)[0]
    if (first !== undefined && first > 50 && matchedIn !== 'subject') start = first - 40
    let excerpt = source.slice(start, start + 160)
    if (start > 0) excerpt = `…${excerpt}`
    let html = escapeHtml(excerpt)
    for (const n of needles.sort((a, b) => b.length - a.length)) {
      const re = new RegExp(`(${escapeHtml(n).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}[\\p{L}\\p{N}]*)`, 'giu')
      html = html.replace(re, '<mark>$1</mark>')
    }
    return html.replace(/<mark>(<mark>)+/g, '<mark>').replace(/(<\/mark>)+/g, '</mark>')
  }

  private version = 0
  /** Remaining "older pages" on the fake server per account (LoadOlder). */
  private remoteOlder = new Map<string, number>()
  private drafts = new Map<string, string>()
  private filterCache = new Map<string, { version: number; result: Message[] }>()

  private filter(q: Partial<ListQuery>): Message[] {
    // Memoised per query until the next mutation (keeps ?stress paging O(page)).
    const key = JSON.stringify({ ...q, cursor: '', limit: 0 })
    const hit = this.filterCache.get(key)
    if (hit && hit.version === this.version) return hit.result
    const result = this.filterUncached(q)
    if (this.filterCache.size > 40) this.filterCache.clear()
    this.filterCache.set(key, { version: this.version, result })
    return result
  }

  private filterUncached(q: Partial<ListQuery>): Message[] {
    const parsed = q.search ? parseSearch(q.search) : null
    const out: Message[] = []
    for (const m of this.messages.values()) {
      if (q.accountId && m.accountId !== q.accountId) continue
      if (q.folderId) {
        if (this.kindOf(q.folderId) === 'starred') {
          if (!m.starred || this.inExcluded(m) || (q.accountId && m.accountId !== q.accountId)) continue
          if (m.accountId !== q.folderId.split(':')[0]) continue
        } else if (!m.folderIds.includes(q.folderId)) continue
      } else if (q.folderKind) {
        if (q.folderKind === 'starred') {
          if (!m.starred || this.inExcluded(m)) continue
        } else if (!m.folderIds.some((f) => this.kindOf(f) === q.folderKind)) continue
      } else if (this.inExcluded(m)) continue
      if (q.unreadOnly && !m.unread) continue
      const labels = q.labels ?? []
      if (labels.length || q.unlabeled) {
        const hit = labels.some((l) => m.labels.includes(l)) || (q.unlabeled && m.labels.length === 0)
        if (!hit) continue
      }
      if (q.excludeLabels?.some((l) => m.labels.includes(l))) continue
      if (parsed && !this.matchSearch(m, parsed).ok) continue
      out.push(m)
    }
    out.sort((a, b) => (a.date === b.date ? (a.id < b.id ? 1 : -1) : a.date < b.date ? 1 : -1))
    return out
  }

  private inboxUnread(accountId: string): number {
    const threads = new Set<string>()
    for (const m of this.messages.values()) {
      if (m.accountId === accountId && m.unread && m.folderIds.includes(`${accountId}:inbox`)) threads.add(m.threadId)
    }
    return threads.size
  }

  // ---------------------------------------------------------------- accounts
  async GetAccounts(): Promise<Account[]> {
    await latency()
    return clone(this.accounts.map((a) => ({
      ...this.cal.decorateAccount(a),
      unreadCount: this.inboxUnread(a.id),
    })))
  }

  async AddAccount(provider: string): Promise<Account> {
    if (provider === 'gmail' && !this.settings.googleClientId) throw new Error('Google OAuth client ID is not configured. Add it in Settings → Mailboxes.')
    if (provider === 'outlook' && !this.settings.microsoftClientId) throw new Error('Microsoft OAuth client ID is not configured. Add it in Settings → Mailboxes.')
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, 3500)
      this.addAccountCancel = () => {
        clearTimeout(t)
        reject(new Error('cancelled'))
      }
    })
    this.addAccountCancel = null
    const n = this.accounts.length + 1
    const acc: Account = {
      id: `acc_new${n}`, provider, email: provider === 'gmail' ? `alex.new${n}@gmail.com` : `alex.new${n}@outlook.com`,
      displayName: provider === 'gmail' ? 'Gmail' : 'Outlook', color: ['#ef4444', '#8b5cf6', '#ec4899'][n % 3],
      createdAt: new Date().toISOString(), lastSyncAt: new Date().toISOString(), syncError: '', syncing: false, unreadCount: 1,
    }
    // Like the backend: when Jev auto-labels every mailbox so far, it auto-labels the new one too.
    const auto = this.settings.autoLabelAccounts ?? []
    if (this.accounts.length && this.accounts.every((a) => auto.includes(a.id))) this.settings.autoLabelAccounts = [...auto, acc.id]
    this.accounts.push(acc)
    // Every mailbox has its own labels: the new one gets its own copies of the defaults.
    this.settings.labels = [...this.settings.labels, ...SEED_LABELS.map((l) => ({ ...l, id: mockLabelId(l.id, acc.id), source: 'user', accountIds: [acc.id] }))]
    this.folders.set(acc.id, foldersFor(acc.id, provider))
    for (const m of buildWelcome(acc)) this.messages.set(m.id, m)
    this.emit('accounts:changed')
    this.changed(acc.id)
    return clone(acc)
  }

  async CancelAddAccount(): Promise<void> {
    this.addAccountCancel?.()
  }

  async RemoveAccount(id: string): Promise<void> {
    await latency()
    this.accounts = this.accounts.filter((a) => a.id !== id)
    this.folders.delete(id)
    // The mailbox's own settings go with it.
    const s = this.settings
    s.mutedMailboxes = (s.mutedMailboxes ?? []).filter((a) => a !== id)
    s.autoLabelAccounts = (s.autoLabelAccounts ?? []).filter((a) => a !== id)
    s.aiCorrectsOff = (s.aiCorrectsOff ?? []).filter((a) => a !== id)
    if (s.quietLabels) delete s.quietLabels[id]
    if (s.notJunkSenders) delete s.notJunkSenders[id]
    if (s.syncIntervals) delete s.syncIntervals[id]
    for (const [mid, m] of this.messages) if (m.accountId === id) this.messages.delete(mid)
    this.emit('accounts:changed')
    this.changed()
  }

  async AutoMoves(_accountId: string, _kind: string, _hours: number): Promise<AutoMove[]> {
    await latency(20, 60)
    return []
  }

  async ReorderAccounts(ids: string[]): Promise<void> {
    await latency(20, 60)
    const pos = new Map(ids.map((id, i) => [id, i]))
    this.accounts = [...this.accounts].sort((a, b) => (pos.get(a.id) ?? ids.length) - (pos.get(b.id) ?? ids.length))
    this.emit('accounts:changed')
  }

  async UpdateAccount(id: string, displayName: string, color: string): Promise<void> {
    await latency(30, 80)
    const a = this.accounts.find((x) => x.id === id)
    if (!a) throw new Error('account not found')
    a.displayName = displayName
    a.color = color
    this.emit('accounts:changed')
  }

  // ---------------------------------------------------------------- folders & messages
  async GetFolders(accountId: string): Promise<Folder[]> {
    await latency(40, 120)
    const list = this.folders.get(accountId) ?? []
    // Single pass over the mailbox (fast even in ?stress mode). Counts are per conversation.
    const total = new Map<string, number>()
    const unread = new Map<string, number>()
    const seen = new Set<string>()
    const sorted = [...this.messages.values()].filter((m) => m.accountId === accountId).sort((a, b) => (a.date < b.date ? 1 : -1))
    for (const m of sorted) {
      if (seen.has(m.threadId)) continue
      seen.add(m.threadId)
      const ids = [...m.folderIds]
      if (m.starred && !this.inExcluded(m)) ids.push(`${accountId}:starred`)
      for (const f of ids) {
        total.set(f, (total.get(f) ?? 0) + 1)
        if (m.unread) unread.set(f, (unread.get(f) ?? 0) + 1)
      }
    }
    return clone(list.map((f) => ({ ...f, total: total.get(f.id) ?? 0, unread: unread.get(f.id) ?? 0 })))
  }

  /** Conversation view: only the newest message of each thread. */
  private latestPerThread(list: Message[]): Message[] {
    const seen = new Set<string>()
    return list.filter((m) => {
      const key = `${m.accountId}:${m.threadId}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
  }

  private participants(m: Message): string[] {
    const own = this.accounts.find((a) => a.id === m.accountId)?.email
    const list = [...this.messages.values()].filter((x) => x.threadId === m.threadId && x.accountId === m.accountId).sort((a, b) => (a.date < b.date ? -1 : 1))
    const names: string[] = []
    for (const x of list) {
      const n = x.from.email === own ? 'me' : (x.from.name || x.from.email).split(' ')[0]
      if (!names.includes(n)) names.push(n)
    }
    return names
  }

  private threadSizes: { version: number; map: Map<string, number> } | null = null
  private threadSize(m: Message): number {
    if (!this.threadSizes || this.threadSizes.version !== this.version) {
      const map = new Map<string, number>()
      for (const x of this.messages.values()) map.set(`${x.accountId}:${x.threadId}`, (map.get(`${x.accountId}:${x.threadId}`) ?? 0) + 1)
      this.threadSizes = { version: this.version, map }
    }
    return this.threadSizes.map.get(`${m.accountId}:${m.threadId}`) ?? 1
  }

  async LoadOlder(q: ListQuery): Promise<number> {
    await sleep(900)
    const ids = q.accountId ? [q.accountId] : this.accounts.map((a) => a.id)
    let added = 0
    for (const id of ids) {
      const left = this.remoteOlder.get(id) ?? 0
      if (!left) continue
      this.remoteOlder.set(id, left - 1)
      const oldest = [...this.messages.values()].filter((m) => m.accountId === id).reduce((t, m) => Math.min(t, Date.parse(m.date)), Date.now())
      const pool = [...this.messages.values()].filter((m) => m.accountId === id && m.folderIds.includes(`${id}:inbox`) && !m.threadId.startsWith('thr_old')).slice(0, 8)
      pool.forEach((src, i) => {
        const nid = `old_${id}_${left}_${i}`
        this.messages.set(nid, { ...clone(src), id: nid, threadId: `thr_old_${nid}`, unread: false, subject: `${src.subject.replace(/^Re: /, '')} (archive copy)`, date: new Date(oldest - (i + 1) * 36 * 3600_000).toISOString() })
        added++
      })
    }
    this.changed(q.accountId)
    return added
  }

  private usage = { todayUsd: 0.0123, monthUsd: 0.4187, requestsToday: 42, requestsMonth: 1310 }
  async GetAIUsage() {
    await latency(30, 80)
    const daily = this.settings.aiDailyBudgetUsd ?? 1
    return { ...this.usage, dailyBudgetUsd: daily, budgetReached: daily > 0 && this.usage.todayUsd >= daily }
  }

  async UpdateAccountSignature(id: string, html: string): Promise<void> {
    await latency(40, 100)
    const a = this.accounts.find((x) => x.id === id)
    if (!a) throw new Error('account not found')
    a.signature = html
    this.emit('accounts:changed', { accountId: id })
  }

  async SearchContacts(query: string, limit = 8): Promise<{ name: string; email: string; count: number }[]> {
    await latency(15, 50)
    const q = query.trim().toLowerCase()
    const own = new Set(this.accounts.map((a) => a.email.toLowerCase()))
    const map = new Map<string, { name: string; email: string; count: number }>()
    for (const m of this.messages.values()) {
      for (const a of [m.from, ...m.to, ...m.cc]) {
        const key = a.email.toLowerCase()
        if (own.has(key)) continue
        const c = map.get(key) ?? { name: a.name, email: a.email, count: 0 }
        c.count++
        if (!c.name && a.name) c.name = a.name
        map.set(key, c)
      }
    }
    return [...map.values()]
      .filter((c) => !q || c.email.toLowerCase().includes(q) || c.name.toLowerCase().includes(q))
      .sort((a, b) => b.count - a.count)
      .slice(0, limit)
  }

  async EmptyFolder(folderId: string): Promise<void> {
    await sleep(400)
    for (const [id, m] of this.messages) if (m.folderIds.includes(folderId)) this.messages.delete(id)
    this.changed(folderId.split(':')[0])
  }

  async DeleteFolder(folderId: string): Promise<void> {
    await sleep(300)
    const accountId = folderId.split(':')[0]
    const list = this.folders.get(accountId) ?? []
    const f = list.find((x) => x.id === folderId)
    if (!f || f.kind !== 'custom') throw new Error('only your own folders can be deleted')
    const trash = list.find((x) => x.kind === 'trash')
    for (const m of this.messages.values()) {
      if (m.folderIds.includes(folderId)) m.folderIds = [...m.folderIds.filter((id) => id !== folderId && id !== trash?.id), ...(trash ? [trash.id] : [])]
    }
    this.folders.set(accountId, list.filter((x) => x.id !== folderId))
    this.changed(accountId)
  }

  async MarkFolderRead(folderId: string): Promise<void> {
    await latency(80, 200)
    this.version++
    for (const m of this.messages.values()) if (m.folderIds.includes(folderId)) m.unread = false
    this.changed(folderId.split(':')[0])
  }

  async ReauthAccount(id: string): Promise<Account> {
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, 2500)
      this.addAccountCancel = () => { clearTimeout(t); reject(new Error('cancelled')) }
    })
    this.addAccountCancel = null
    const a = this.accounts.find((x) => x.id === id)
    if (!a) throw new Error('account not found')
    a.needsReauth = false
    a.syncError = ''
    this.emit('accounts:changed', { accountId: id })
    return clone(a)
  }

  async Unsubscribe(id: string): Promise<{ method: string; url: string }> {
    await sleep(700)
    const m = this.messages.get(id)
    if (!m?.unsubscribe) throw new Error('This sender does not support unsubscribing')
    if (m.unsubscribe.oneClick) return { method: 'oneClick', url: '' }
    // Like the Go backend: "url" means the page was opened in the browser already.
    if (m.unsubscribe.url) void this.OpenExternalURL(m.unsubscribe.url)
    return { method: 'url', url: m.unsubscribe.url ?? '' }
  }

  async GetMessageWithRemoteContent(id: string): Promise<Message> {
    await latency(50, 150)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    return this.withInvite(this.visibleLabels(clone(m)))
  }

  /** Like the Go backend: strip remote images unless allowed (global switch or trusted sender). */
  private blockRemote(m: Message): Message {
    if (this.settings.showRemoteImages || remoteImagesTrusted(this.settings.trustedImageSenders, m.from.email)) return m
    const html = m.bodyHtml.replace(/\s(src|background)\s*=\s*"\s*(https?:)?\/\/[^"]*"/gi, ' data-gomail-blocked=""').replace(/\ssrcset\s*=\s*"[^"]*"/gi, '')
    return html === m.bodyHtml ? m : { ...m, bodyHtml: html, remoteContentBlocked: true }
  }

  async GetAttachmentData(messageId: string, attachmentId: string): Promise<AttachmentData> {
    await sleep(250)
    const att = this.messages.get(messageId)?.attachments.find((a) => a.id === attachmentId)
    if (!att) throw new Error('attachment not found')
    if (att.mimeType.startsWith('image/')) return { mimeType: 'image/png', dataBase64: SAMPLE_PNG }
    if (att.mimeType === 'application/pdf') return { mimeType: 'application/pdf', dataBase64: SAMPLE_PDF }
    return { mimeType: 'text/plain', dataBase64: btoa(`Preview of ${att.filename}\n(mock data)`) }
  }

  async SaveDraft(req: ComposeRequest, draftId: string): Promise<string> {
    await latency(80, 200)
    const id = draftId || `draft_${Date.now().toString(36)}`
    const acc = this.accounts.find((a) => a.id === req.accountId)
    if (!acc) return id
    const msgId = this.drafts.get(id)
    if (msgId) this.messages.delete(msgId)
    const m = buildMessage({ acc: acc.id, from: { name: 'Alex Morgan', email: acc.email }, to: req.to, subject: req.subject || '(no subject)', html: req.bodyHtml, text: req.bodyText, folder: 'drafts', read: true }, Date.now(), undefined, { isDraft: true })
    this.messages.set(m.id, m)
    this.drafts.set(id, m.id)
    this.changed(acc.id)
    return id
  }

  async DeleteDraft(id: string): Promise<void> {
    await latency(40, 100)
    const msgId = this.drafts.get(id)
    if (msgId) this.messages.delete(msgId)
    this.drafts.delete(id)
    this.changed()
  }

  /** Thread-level actions apply to every message of the conversation. */
  private withThreads(ids: string[]): string[] {
    const threads = new Set(this.expandIds(ids).map((id) => this.messages.get(id)).filter(Boolean).map((m) => `${m!.accountId}:${m!.threadId}`))
    return [...this.messages.values()].filter((m) => threads.has(`${m.accountId}:${m.threadId}`)).map((m) => m.id)
  }

  private threadCache = new WeakMap<Message[], Message[]>()

  async ListMessages(q: ListQuery): Promise<MessagePage> {
    await latency(90, 260)
    const filtered = this.filter(q)
    const conv = this.settings.conversationView !== false
    let all = conv ? this.threadCache.get(filtered) : filtered
    if (!all) {
      all = this.latestPerThread(filtered)
      this.threadCache.set(filtered, all)
    }
    let start = 0
    if (q.cursor) {
      const [date, id] = atob(q.cursor).split('|')
      let lo = 0
      let hi = all.length
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        const m = all[mid]
        if (m.date < date || (m.date === date && m.id < id)) hi = mid
        else lo = mid + 1
      }
      start = lo
    }
    const limit = q.limit > 0 ? q.limit : 50
    const page = all.slice(start, start + limit)
    const last = page[page.length - 1]
    const more = start + limit < all.length
    const kind = q.folderId ? this.kindOf(q.folderId) : q.folderKind
    const remote = (q.accountId ? [q.accountId] : this.accounts.map((a) => a.id)).some((id) => (this.remoteOlder.get(id) ?? 0) > 0)
    return {
      messages: page.map((m) => ({ ...this.summary(m), threadCount: conv ? this.threadSize(m) : 1, conversationId: conv ? `c:${m.threadId}` : '', participants: conv ? this.participants(m) : null })),
      nextCursor: more && last ? btoa(`${last.date}|${last.id}`) : '',
      total: all.length,
      hasMoreRemote: !more && kind === 'inbox' && !q.search && remote,
    }
  }

  async GetMessage(id: string): Promise<Message> {
    await latency(50, 150)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    return this.blockRemote(this.withInvite(this.visibleLabels(clone(m))))
  }

  private withInvite(m: Message): Message {
    const invite = this.cal.inviteFor(m)
    return invite ? { ...m, invite } : m
  }

  // ---------------------------------------------------------------- calendar
  async ListCalendars(): Promise<Calendar[]> {
    await latency(30, 90)
    // Like the backend: a disabled mailbox's calendars stay listed (shown grayed out); only its events go.
    const consent = new Set(this.accounts.map((a) => this.cal.decorateAccount(a)).filter((a) => a.calendarConsent).map((a) => a.id))
    return clone(this.cal.calendars.filter((c) => consent.has(c.accountId)))
  }

  async SetCalendarVisible(id: string, visible: boolean): Promise<void> {
    await latency(20, 60)
    this.cal.setVisible(id, visible)
  }

  async SetCalendarColor(id: string, color: string): Promise<void> {
    await latency(20, 60)
    const c = this.cal.calendars.find((x) => x.id === id)
    if (!c) throw new Error('calendar not found')
    const base = c as typeof c & { providerColor?: string }
    base.providerColor ??= c.color
    c.color = color || base.providerColor
  }

  async SetAccountCalendarEnabled(accountId: string, enabled: boolean): Promise<void> {
    await latency(20, 60)
    const a = this.accounts.find((x) => x.id === accountId)
    if (!a) throw new Error('account not found')
    a.calendarEnabled = enabled
    this.emit('accounts:changed', { accountId })
    this.emit('calendar:changed', { accountId })
  }

  async SetAccountCalendarMuted(accountId: string, muted: boolean): Promise<void> {
    await latency(20, 60)
    const a = this.accounts.find((x) => x.id === accountId)
    if (!a) throw new Error('account not found')
    a.calendarMuted = muted
    this.emit('accounts:changed', { accountId })
  }

  async EnableCalendar(accountId: string): Promise<void> {
    await sleep(1200) // the OAuth round trip
    const a = this.accounts.find((x) => x.id === accountId)
    if (!a) throw new Error('account not found')
    a.calendarConsent = true
    this.cal.connect(accountId)
    this.emit('accounts:changed', { accountId })
  }

  async ListEvents(start: string, end: string): Promise<CalendarEvent[]> {
    await latency(60, 160)
    const ok = new Set(this.accounts.map((a) => this.cal.decorateAccount(a)).filter((a) => a.calendarConsent).map((a) => a.id))
    return this.cal.list(start, end, ok)
  }

  async GetEvent(id: string): Promise<CalendarEvent> {
    await latency(20, 60)
    return this.cal.get(id)
  }

  async CreateEvent(wire: EventInput): Promise<CalendarEvent> {
    await latency(120, 260)
    let input = fromWireInput(wire as unknown as Record<string, unknown>)
    if (!input.calendarId) input = { ...input, calendarId: this.cal.calendars.find((c) => c.primary && c.canEdit)?.id ?? '' }
    if (!input.title.trim()) input = { ...input, title: '(No title)' }
    return this.cal.create(input)
  }

  async UpdateEvent(id: string, wire: EventInput, scope: RecurrenceScope): Promise<CalendarEvent> {
    await latency(120, 260)
    return this.cal.update(id, fromWireInput(wire as unknown as Record<string, unknown>), scope)
  }

  async MoveEventCheck(id: string, calendarId: string) {
    await latency(20, 60)
    const ev = this.cal.get(id)
    const target = this.cal.calendars.find((c) => c.id === calendarId)
    const crossAccount = !!target && target.accountId !== ev.accountId
    // Like the backend: recurring events can't move across accounts.
    const allowed = !!target?.canEdit && !(crossAccount && ev.isRecurring)
    return { allowed, reason: allowed ? '' : ev.isRecurring ? 'Recurring events can’t be moved to another account.' : 'That calendar is read-only.', crossAccount, hasAttendees: ev.attendees.length > 0, recurring: ev.isRecurring }
  }

  async SyncCalendars(accountId: string): Promise<void> {
    await latency(200, 500)
    this.emit('calendar:changed', { accountId })
  }

  async DeleteEvent(id: string, scope: RecurrenceScope): Promise<void> {
    await latency(100, 220)
    this.cal.remove(id, scope)
  }

  async RespondToEvent(id: string, response: AttendeeResponse): Promise<void> {
    await latency(150, 300)
    this.cal.respond(id, response)
  }

  async GetThread(id: string): Promise<Message[]> {
    await latency(60, 180)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    const thread = [...this.messages.values()].filter((x) => x.threadId === m.threadId && x.accountId === m.accountId)
    thread.sort((a, b) => (a.date < b.date ? -1 : 1))
    return clone(thread).map((x) => this.blockRemote(this.withInvite(this.visibleLabels(x))))
  }

  /** "c:<threadId>" conversation ids expand to the thread's messages (like the Go backend). */
  private expandIds(ids: string[]): string[] {
    const out: string[] = []
    for (const id of ids) {
      if (id.startsWith('c:')) {
        const tid = id.slice(2)
        for (const m of this.messages.values()) if (m.threadId === tid) out.push(m.id)
      } else out.push(id)
    }
    return out
  }

  private mutate(ids: string[], fn: (m: Message) => void) {
    this.version++
    const accs = new Set<string>()
    for (const id of this.expandIds(ids)) {
      const m = this.messages.get(id)
      if (m) {
        fn(m)
        accs.add(m.accountId)
      }
    }
    this.changed(accs.size === 1 ? [...accs][0] : '')
  }

  async MarkRead(ids: string[], read: boolean): Promise<void> {
    await latency(30, 90)
    this.mutate(this.withThreads(ids), (m) => (m.unread = !read))
  }

  async SetStarred(ids: string[], starred: boolean): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => (m.starred = starred))
  }

  async Archive(ids: string[]): Promise<void> {
    await latency(50, 140)
    this.mutate(this.withThreads(ids), (m) => {
      m.folderIds = [...m.folderIds.filter((f) => this.kindOf(f) !== 'inbox' && this.kindOf(f) !== 'custom'), `${m.accountId}:archive`]
      m.folderIds = [...new Set(m.folderIds)]
    })
  }

  async Trash(ids: string[]): Promise<void> {
    await latency(50, 140)
    this.mutate(this.withThreads(ids), (m) => (m.folderIds = [`${m.accountId}:trash`]))
  }

  async MoveToJunk(ids: string[]): Promise<void> {
    await latency(50, 140)
    this.mutate(this.withThreads(ids), (m) => (m.folderIds = [`${m.accountId}:spam`]))
  }

  async MoveToFolder(ids: string[], folderId: string): Promise<void> {
    await latency(50, 140)
    this.mutate(this.withThreads(ids), (m) => {
      if (folderId.split(':')[0] !== m.accountId) return
      m.folderIds = this.kindOf(folderId) === 'custom' ? [`${m.accountId}:inbox`, folderId] : [folderId]
    })
  }

  /** Like the backend's senderNeedle: the address, or "@domain" (not for shared mail services). */
  private senderNeedle(accountId: string, address: string, scope: SenderScope): string {
    address = address.trim().toLowerCase()
    const domain = domainOf(address)
    if (!domain) throw new Error('this sender has no valid address')
    const acct = this.accounts.find((a) => a.id === accountId)
    if (!acct) throw new Error('that mailbox no longer exists')
    if (scope === 'domain' && (isFreeMailDomain(domain) || domainOf(acct.email) === domain)) throw new Error(`${domain} is shared by many people; use the address instead`)
    return scope === 'domain' ? `@${domain}` : address
  }

  /** Like the backend: the mailbox's rule with this action that covers needle, else a new one. */
  private ensureSenderRule(accountId: string, needle: string, action: 'junk' | 'label', labelId: string): { rule: RuleRef; created: boolean } {
    const rules = this.settings.labelRules ?? []
    const have = rules.find((r) => r.action === action && (r.labelId ?? '') === labelId && r.field === 'from' && (!r.accountId || r.accountId === accountId) && r.contains && needle.includes(r.contains.toLowerCase()))
    if (have) return { rule: { id: have.id, accountId: have.accountId, contains: have.contains, action, labelId }, created: false }
    const rule = { id: `rule_${Math.random().toString(36).slice(2, 10)}`, accountId, field: 'from' as const, contains: needle, action, labelId }
    this.settings.labelRules = [...rules, rule]
    this.emit('labels:changed', { accountId: '' })
    return { rule, created: true }
  }

  private fromMatches(m: MessageSummary, needle: string): boolean {
    return `${m.from.name} ${m.from.email}`.toLowerCase().includes(needle)
  }

  /** Like the backend: a junk rule for the mailbox (unless one covers the sender), then the picked
   *  rows and the sender's inbox mail to Junk, marked read. */
  async JunkSender(accountId: string, address: string, scope: SenderScope, ids: string[]): Promise<JunkSenderResult> {
    await latency(80, 200)
    const needle = this.senderNeedle(accountId, address, scope)
    const { rule, created } = this.ensureSenderRule(accountId, needle, 'junk', '')
    const picked = new Set(this.withThreads(ids).filter((id) => this.messages.get(id)?.accountId === accountId))
    const moved: string[] = []
    for (const m of this.messages.values()) {
      if (m.accountId !== accountId || picked.has(m.id) || !m.folderIds.some((f) => this.kindOf(f) === 'inbox')) continue
      if (this.fromMatches(m, needle)) moved.push(m.id)
    }
    const all = [...picked, ...moved]
    const unread = all.filter((id) => this.messages.get(id)?.unread)
    this.mutate(all, (m) => {
      m.unread = false
      m.folderIds = [`${m.accountId}:spam`]
    })
    return { rule, created, count: all.length, moved, unread }
  }

  /** Like the backend: a label rule for the mailbox, and the label on the sender's received mail now. */
  async LabelSender(accountId: string, address: string, scope: SenderScope, labelId: string): Promise<LabelSenderResult> {
    await latency(80, 200)
    const label = this.allLabels().find((l) => l.id === labelId)
    if (!label) throw new Error('that label no longer exists')
    const needle = this.senderNeedle(accountId, address, scope)
    const { rule, created } = this.ensureSenderRule(accountId, needle, 'label', labelId)
    const labeled: string[] = []
    for (const m of this.messages.values()) {
      if (m.accountId !== accountId || m.labels.includes(labelId) || !this.fromMatches(m, needle)) continue
      if (m.folderIds.some((f) => ['sent', 'drafts', 'trash'].includes(this.kindOf(f)))) continue
      m.labels.push(labelId)
      m.labelDetails = [...(m.labelDetails ?? []), { labelId, source: 'manual', confidence: 1 }]
      labeled.push(m.id)
    }
    if (labeled.length) {
      this.version++
      this.changed(accountId)
      this.emit('labels:changed', { accountId: '' })
    }
    return { rule, created, labeled }
  }

  /** Like the backend: back to the inbox, plus the junk rules still matching the senders. */
  async NotJunk(ids: string[]): Promise<NotJunkResult> {
    await latency(50, 140)
    const list = this.withThreads(ids).map((id) => this.messages.get(id)).filter((m): m is Message => !!m)
    this.mutate(list.map((m) => m.id), (m) => {
      m.folderIds = [`${m.accountId}:inbox`]
      m.labels = m.labels.filter((l) => l !== 'junk')
      m.labelDetails = (m.labelDetails ?? []).filter((d) => d.labelId !== 'junk')
    })
    const moved = new Set(list.map((m) => m.id))
    const senders = this.settings.notJunkSenders ?? (this.settings.notJunkSenders = {})
    const more = new Map<string, { accountId: string; email: string; count: number }>()
    for (const m of list) {
      const email = m.from.email.toLowerCase()
      if (!email) continue
      if (!(senders[m.accountId] ?? []).includes(email)) senders[m.accountId] = [...(senders[m.accountId] ?? []), email]
      const count = this.junkFrom(m.accountId, email).filter((x) => !moved.has(x.id)).length
      if (count) more.set(`${m.accountId} ${email}`, { accountId: m.accountId, email, count })
    }
    const found = new Map<string, RuleRef>()
    for (const r of this.settings.labelRules ?? []) {
      if (r.action !== 'junk' || r.field !== 'from' || !r.contains) continue
      const hit = list.some((m) => (!r.accountId || r.accountId === m.accountId) && `${m.from.name} ${m.from.email}`.toLowerCase().includes(r.contains.toLowerCase()))
      if (hit) found.set(r.id, { id: r.id, accountId: r.accountId, contains: r.contains, action: 'junk', labelId: '' })
    }
    return { count: list.length, rules: [...found.values()], more: [...more.values()] }
  }

  private junkFrom(accountId: string, email: string): Message[] {
    return [...this.messages.values()].filter((m) => m.accountId === accountId && m.from.email.toLowerCase() === email && m.folderIds.some((f) => this.kindOf(f) === 'spam'))
  }

  /** Like the backend: all of a sender's Junk mail to the inbox. */
  async NotJunkSender(accountId: string, email: string): Promise<number> {
    const ids = this.junkFrom(accountId, email.toLowerCase()).map((m) => m.id)
    return ids.length ? (await this.NotJunk(ids)).count : 0
  }

  async RemoveRules(ruleIds: string[]): Promise<void> {
    await latency(30, 90)
    this.settings.labelRules = (this.settings.labelRules ?? []).filter((r) => !ruleIds.includes(r.id))
    this.emit('labels:changed', { accountId: '' })
  }

  /** Like the backend's sender kinds: "" is the inbox, "all" is received mail outside Junk and Trash. */
  private senderKind(m: Message, kind: string): boolean {
    const kinds = m.folderIds.map((f) => this.kindOf(f))
    return kind === 'all' ? kinds.some((k) => ['inbox', 'archive', 'custom'].includes(k)) : kinds.includes(kind || 'inbox')
  }

  /** Like the backend: senders of a folder kind per address or company domain, the most mail first. */
  async TopSenders(accountId: string, folderKind: string, groupBy: SenderGrouping, limit: number): Promise<SenderSummary[]> {
    await latency(80, 200)
    const own = (id: string) => this.accounts.find((a) => a.id === id)?.email ?? ''
    const stats = new Map<string, SenderStat>()
    for (const m of this.messages.values()) {
      const address = m.from.email.trim().toLowerCase()
      if ((accountId && m.accountId !== accountId) || !address || address === own(m.accountId).toLowerCase() || !this.senderKind(m, folderKind)) continue
      const key = `${m.accountId}\n${address}`
      const st = stats.get(key)
      if (!st) stats.set(key, { accountId: m.accountId, address, name: m.from.name, count: 1, unread: m.unread ? 1 : 0, newest: m.date, unsubscribe: !!m.unsubscribe })
      else {
        st.count++
        if (m.unread) st.unread++
        if (m.date > st.newest) Object.assign(st, { newest: m.date, name: m.from.name, unsubscribe: !!m.unsubscribe })
      }
    }
    return groupSenders([...stats.values()], groupBy === 'domain' ? 'domain' : 'address', own, Math.min(limit > 0 ? limit : 200, 2000))
  }

  /** Like the backend: one sender's mail (the address, or "@domain") in a folder kind, matched exactly. */
  async SenderMessageIDs(accountId: string, folderKind: string, sender: string): Promise<string[]> {
    await latency(40, 120)
    const s = sender.trim().toLowerCase()
    const hit = (email: string) => (s.startsWith('@') ? email.endsWith(s) : email === s)
    return [...this.messages.values()]
      .filter((m) => m.accountId === accountId && this.senderKind(m, folderKind) && hit(m.from.email.trim().toLowerCase()))
      .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0))
      .map((m) => m.id)
  }

  async SendMessage(req: ComposeRequest): Promise<void> {
    await sleep(700)
    if (!req.to.length && !req.cc.length && !req.bcc.length) throw new Error('Add at least one recipient')
    const acc = this.accounts.find((a) => a.id === req.accountId)
    if (!acc) throw new Error('Choose an account to send from')
    const orig = req.replyToId ? this.messages.get(req.replyToId) : undefined
    const m = buildMessage({
      acc: acc.id, from: { name: 'Alex Morgan', email: acc.email }, to: req.to, cc: req.cc, subject: req.subject || '(no subject)',
      html: req.bodyHtml, text: req.bodyText, folder: 'sent', read: true,
      att: req.attachments.map((a) => ({ filename: a.filename, mimeType: a.mimeType, size: Math.round(a.dataBase64.length * 0.75) })),
    }, Date.now(), orig && req.mode !== 'forward' ? orig.threadId : undefined)
    this.messages.set(m.id, m)
    if (req.draftId) {
      const dm = this.drafts.get(req.draftId)
      if (dm) this.messages.delete(dm)
      this.drafts.delete(req.draftId)
    }
    this.changed(acc.id)
  }

  async SaveAttachment(_messageId: string, _attachmentId: string): Promise<void> {
    await sleep(400)
  }

  async SyncNow(accountId: string): Promise<void> {
    const targets = accountId ? this.accounts.filter((a) => a.id === accountId) : this.accounts
    for (const a of targets) {
      a.syncing = true
      this.emit('sync:started', { accountId: a.id })
    }
    void (async () => {
      for (const a of targets) {
        for (let step = 1; step <= 4; step++) {
          await sleep(220)
          this.emit('sync:progress', { accountId: a.id, phase: step < 3 ? 'headers' : 'labels', done: step * 25, total: 100 })
        }
      }
      await sleep(300 + Math.random() * 300)
      for (const a of targets) {
        let newMessages = 0
        // Every other sync of the first target brings in one new message.
        if (a === targets[0] && this.incomingIdx % 2 === 0) {
          const tpl = asTemplateAccount(INCOMING[(this.incomingIdx / 2) % INCOMING.length], this.accounts.map((x) => x.id))
          if (tpl.acc === a.id || !accountId) {
            const msg = buildMessage({ ...tpl, acc: accountId ? a.id : tpl.acc }, Date.now())
            if (!this.settings.autoLabelAccounts?.includes(msg.accountId)) { msg.labels = []; msg.labelDetails = [] }
            this.messages.set(msg.id, msg)
            newMessages = 1
            if (!this.quiet(msg)) this.emit('mail:new', { accountId: msg.accountId, count: 1, latest: this.summary(msg) })
          }
        }
        a.syncing = false
        a.lastSyncAt = new Date().toISOString()
        this.emit('sync:finished', { accountId: a.id, newMessages, error: '' })
        if (newMessages) this.changed(a.id)
      }
      this.incomingIdx++
    })()
  }

  async OpenExternalURL(url: string): Promise<void> {
    window.open(url, '_blank', 'noopener,noreferrer')
  }

  // ---------------------------------------------------------------- settings
  async GetSettings(): Promise<Settings> {
    await latency(20, 60)
    return clone({ ...this.settings, labels: this.allLabels() })
  }

  async SaveSettings(s: Settings): Promise<void> {
    await latency(30, 90)
    this.version++
    const keySet = this.settings.openRouterKeySet
    const incoming = clone(s)
    // Provider/system labels can only change color.
    for (const pl of this.providerLabels) {
      const upd = incoming.labels.find((l) => l.id === pl.id)
      if (upd) pl.color = upd.color
    }
    const userLabels = incoming.labels.filter((l) => !l.source || l.source === 'user').map((l) => ({ ...l, source: 'user' }))
    this.settings = { ...incoming, labels: userLabels, openRouterKeySet: keySet }
    this.settings.autoLabelAccounts = [...new Set(incoming.autoLabelAccounts ?? [])]
    this.settings.autoClassify = this.settings.autoLabelAccounts.length > 0 // kept in sync, like the backend
    if (incoming.importProviderLabels !== undefined) this.settings.importProviderLabels = incoming.importProviderLabels
    this.emit('labels:changed', { accountId: '' })
    const valid = new Set([...this.settings.labels, ...this.providerLabels].map((l) => l.id))
    for (const m of this.messages.values()) {
      if (m.labels.some((l) => !valid.has(l))) {
        m.labels = m.labels.filter((l) => valid.has(l))
        m.labelDetails = (m.labelDetails ?? []).filter((d) => valid.has(d.labelId))
      }
    }
  }

  async SetOpenRouterKey(key: string): Promise<void> {
    await latency()
    this.settings.openRouterKeySet = !!key.trim()
  }

  async TestOpenRouter(): Promise<void> {
    await sleep(700)
    if (!this.settings.openRouterKeySet) throw new Error('No OpenRouter API key set')
  }

  async RefreshAIModels(): Promise<AIModel[]> {
    return this.ListAIModels()
  }

  async ListAIModels(_force = false): Promise<AIModel[]> {
    await sleep(350)
    return clone(MOCK_MODELS)
  }

  async GetDefaultPrompts(): Promise<DefaultPrompts> {
    await latency(20, 50)
    return { grammar: DEFAULT_GRAMMAR_PROMPT, classify: DEFAULT_CLASSIFY_INSTRUCTIONS }
  }

  async GetAppInfo(): Promise<AppInfo> {
    await latency(20, 50)
    return { version: '0.1.0-dev', dataDir: '~/.config/gomail', logFile: '~/.config/gomail/gomail.log', platform: 'browser/mock', os: 'browser', arch: 'wasm', demo: true }
  }

  // ---------------------------------------------------------------- backup
  /** Pretends the save dialog picked ~/Downloads (nothing is written in the browser). */
  async ExportBackup(includeSecrets: boolean): Promise<BackupResult> {
    await sleep(500)
    let marks = 0
    for (const m of this.messages.values()) marks += (m.labelDetails ?? []).filter((d) => d.source === 'manual' || d.source === 'ai').length
    const day = new Date().toISOString().slice(0, 10)
    return { cancelled: false, path: `~/Downloads/gomail-backup-${day}.gomailbackup`, accounts: this.accounts.length, labels: this.allLabels().length, labelAssignments: marks, includesSecrets: includeSecrets }
  }

  /** Pretends a backup of this same install was picked: everything merges into the existing accounts. */
  async ImportBackup(): Promise<RestoreSummary> {
    await sleep(700)
    const exp = await this.ExportBackup(false)
    this.emit('accounts:changed', { accountId: '' })
    this.emit('labels:changed', { accountId: '' })
    return {
      cancelled: false, path: exp.path, createdAt: new Date().toISOString(), labels: exp.labels, rules: 0, accountsAdded: 0, accountsReady: 0,
      accountsMerged: exp.accounts, accountsSkipped: 0, labelAssignments: exp.labelAssignments, appliedNow: exp.labelAssignments, calendars: 0, secretsRestored: false,
    }
  }

  /** Next backend pass. ?update=1 pretends a newer release exists (and emits update:available). */
  async CheckForUpdate() {
    await latency(150, 400)
    const available = typeof location !== 'undefined' && new URLSearchParams(location.search).has('update')
    const info = { current: '0.1.0-dev', latest: available ? '0.2.0' : '0.1.0-dev', url: 'https://github.com/AVMG20/gomail/releases/latest', notes: available ? 'Faster search, dark-mode email rendering and bug fixes.' : '', available }
    if (available) setTimeout(() => this.emit('update:available', info), 50)
    return info
  }

  // ---------------------------------------------------------------- labels & AI
  private requireAI() {
    if (!this.settings.openRouterKeySet) throw new Error('Set an OpenRouter API key in Settings → AI first')
  }

  private classifyOne(m: Message): ClassificationResult {
    const text = `${m.from.name} ${m.from.email} ${m.subject} ${m.bodyText || m.snippet}`.toLowerCase()
    const hints: Record<string, string[]> = {
      work: ['roadmap', 'meeting', 'sync', 'standup', 'contract', 'client', 'interview', 'design', 'project', 'northwind', 'logo', 'incident'],
      finance: ['receipt', 'invoice', 'statement', 'payment', 'order', 'price', '€', '$', 'paid', 'total'],
      travel: ['flight', 'booking', 'hotel', 'airbnb', 'check-in', 'lisbon', 'trip', 'klm'],
      newsletters: ['newsletter', 'digest', 'weekly', 'unsubscribe', "what's new", 'issue'],
      social: ['saturday', 'dinner', 'love', 'photos', 'pizza', 'book', 'climbing'],
      updates: ['github', 'notification', 'alert', 'deployment', 'commented', 'reminder', 'assigned', 'linear'],
      junk: ['won', 'prize', 'claim', 'urgent', 'verify', 'password expires', 'lucky'],
      'gmail-receipts': ['receipt', 'invoice', 'statement', 'paid', 'total'],
    }
    const s = this.settings
    const corrects = s.aiCorrectsProviderLabels && !s.aiCorrectsOff?.includes(m.accountId)
    // The labels of the message's mailbox; imported ones need a description (like the backend).
    const candidates = [...s.labels, ...(s.importProviderLabels && corrects ? this.providerLabels.filter((l) => l.description) : [])]
      .filter((l) => labelInAccount(l, m.accountId))
    const scores: Record<string, number> = {}
    for (const l of candidates) {
      const kw = [...(hints[l.id] ?? hints[l.id.replace(/-(work|side)$/, '')] ?? []), ...words(`${l.name} ${l.description}`).filter((w) => w.length > 4)]
      const hitsN = kw.reduce((acc, k) => acc + (text.includes(k) ? 1 : 0), 0)
      const x = hitsN * 1.4 - 2.2 + (hash(m.id + l.id) % 10) / 20
      scores[l.id] = Math.round((1 / (1 + Math.exp(-x))) * 100) / 100
    }
    const details = m.labelDetails ?? []
    const isManual = (id: string) => details.some((d) => d.labelId === id && d.source === 'manual')
    const added = Object.entries(scores)
      .filter(([id, p]) => p >= s.classifyThreshold && !m.labels.includes(id) && !this.providerLabels.some((pl) => pl.id === id))
      .sort((x, y) => y[1] - x[1]).slice(0, 2).map(([id]) => id)
    const removed = m.labels.filter((id) => scores[id] !== undefined && scores[id] < s.classifyRemoveThreshold && !isManual(id))
    return { messageId: m.id, added, removed, scores, cost: 0.00009 + (hash(m.id) % 40) / 1_000_000 }
  }

  private applyResult(m: Message, r: ClassificationResult) {
    let details: MessageLabel[] = m.labelDetails ?? []
    for (const id of r.removed) {
      m.labels = m.labels.filter((l) => l !== id)
      details = details.filter((d) => d.labelId !== id)
    }
    for (const id of r.added) {
      if (!m.labels.includes(id)) m.labels = [...m.labels, id]
      details = [...details.filter((d) => d.labelId !== id), { labelId: id, source: 'ai', confidence: r.scores[id] ?? 0 }]
    }
    m.labelDetails = details
  }

  async ClassifyMessages(ids: string[]): Promise<ClassificationResult[]> {
    this.requireAI()
    await sleep(900 + ids.length * 40)
    const results: ClassificationResult[] = []
    for (const id of ids) {
      const m = this.messages.get(id)
      if (!m) continue
      const r = this.classifyOne(m)
      this.applyResult(m, r)
      results.push(r)
    }
    this.changed()
    return results
  }

  /** The label a message gets for labelId: its own mailbox's label with that name (like the backend). */
  private mailboxLabel(m: Message, labelId: string): string | undefined {
    const l = this.allLabels().find((x) => x.id === labelId)
    return l && labelInMailbox(this.allLabels(), l, m.accountId)?.id
  }

  async AddLabel(ids: string[], labelId: string): Promise<number> {
    await this.RestoreLabel(ids, labelId)
    return 0 // the mock doesn't do label actions
  }

  async RestoreLabel(ids: string[], labelId: string): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => {
      const id = this.mailboxLabel(m, labelId)
      if (!id) return
      if (!m.labels.includes(id)) m.labels = [...m.labels, id]
      m.labelDetails = [...(m.labelDetails ?? []).filter((d) => d.labelId !== id), { labelId: id, source: 'manual', confidence: 1 }]
    })
  }

  async ApplyLabelAction(labelId: string): Promise<number> {
    await latency(80, 200)
    const l = this.settings.labels.find((x) => x.id === labelId)
    const kind = l?.then === 'junk' ? 'spam' : l?.then
    if (!l || !kind) throw new Error('this label doesn’t move mail')
    let n = 0
    for (const m of this.messages.values()) {
      if (!m.labels.includes(labelId)) continue
      const folders = this.folders.get(m.accountId) ?? []
      const dest = folders.find((f) => f.kind === kind)
      const kinds = m.folderIds.map((id) => folders.find((f) => f.id === id)?.kind)
      if (!dest || kinds.some((k) => k === kind || k === 'sent' || k === 'drafts' || k === 'trash') || (kind === 'archive' && !kinds.includes('inbox'))) continue
      m.folderIds = [dest.id]
      n++
    }
    if (n) this.changed('')
    return n
  }

  async RemoveLabel(ids: string[], labelId: string): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => {
      const id = this.mailboxLabel(m, labelId) ?? labelId
      m.labels = m.labels.filter((l) => l !== id)
      m.labelDetails = (m.labelDetails ?? []).filter((d) => d.labelId !== id)
    })
  }

  async GetViewLabelCounts(q: ListQuery): Promise<LabelCount[]> {
    await latency(30, 80)
    const counts = new Map<string, LabelCount>()
    const hidden = new Set(this.settings.importProviderLabels ? [] : this.providerLabels.map((l) => l.id))
    const view = this.filter({ ...q, labels: [], excludeLabels: [], unlabeled: false, search: '', unreadOnly: false, cursor: '', limit: 0 })
    for (const m of view) {
      for (const l of m.labels) {
        if (hidden.has(l)) continue
        const c = counts.get(l) ?? { labelId: l, total: 0, unread: 0 }
        c.total++
        if (m.unread) c.unread++
        counts.set(l, c)
      }
    }
    return [...counts.values()]
  }

  async GetLabelCounts(accountId: string): Promise<LabelCount[]> {
    await latency(30, 80)
    const counts = new Map<string, LabelCount>()
    const hidden = new Set(this.settings.importProviderLabels ? [] : this.providerLabels.map((l) => l.id))
    for (const m of this.messages.values()) {
      if (accountId && m.accountId !== accountId) continue
      for (const l of m.labels) {
        if (hidden.has(l)) continue
        const c = counts.get(l) ?? { labelId: l, total: 0, unread: 0 }
        c.total++
        if (m.unread) c.unread++
        counts.set(l, c)
      }
    }
    return [...counts.values()]
  }

  async SearchMessages(q: SearchQuery): Promise<SearchResult[]> {
    await latency(25, 90)
    const parsed = parseSearch(q.query)
    const out: SearchResult[] = []
    for (const m of this.messages.values()) {
      if (q.accountId && m.accountId !== q.accountId) continue
      if (q.labels?.length && !q.labels.some((l) => m.labels.includes(l))) continue
      if (!parsed.inKind && this.inExcluded(m)) continue
      const r = this.matchSearch(m, parsed)
      if (!r.ok) continue
      out.push({ message: this.summary(m), highlight: this.highlight(m, parsed, r.matchedIn), matchedIn: r.matchedIn, score: r.score, fuzzy: r.fuzzy })
    }
    out.sort((a, b) => b.score - a.score)
    return out.slice(0, Math.min(q.limit || 20, 100))
  }

  async ResolveQueryIDs(q: ListQuery, max: number): Promise<string[]> {
    await latency()
    return this.latestPerThread(this.filter({ ...q, cursor: '' })).slice(0, max > 0 ? max : 10000).map((m) => m.id)
  }

  private classifyTargets(q: ListQuery, onlyUnlabeled: boolean, includeAllFolders: boolean): Message[] {
    // Spam/Junk is labeled like normal mail; Trash and Drafts only on request.
    const includeAll = includeAllFolders
    const base = !q.folderId && !q.folderKind
      ? [...this.messages.values()].filter((m) => (!q.accountId || m.accountId === q.accountId) && (!q.unreadOnly || m.unread)
        && (!(q.labels?.length || q.unlabeled) || q.labels.some((l) => m.labels.includes(l)) || (q.unlabeled && m.labels.length === 0))
        && !q.excludeLabels?.some((l) => m.labels.includes(l)))
      : this.filter({ ...q, cursor: '' })
    return base.filter((m) => (includeAll || !this.neverLabel(m)) && (!onlyUnlabeled || m.labels.length === 0))
  }

  async EstimateClassify(q: ListQuery, onlyUnlabeled: boolean, includeAllFolders = false): Promise<ClassifyEstimate> {
    await latency(80, 200)
    const count = this.classifyTargets(q, onlyUnlabeled, includeAllFolders).length
    const requests = count * Math.ceil(this.allLabels().length / 20)
    return { count, requests, estimatedTokens: count * 450, estimatedCostUsd: count * 0.00011, priceKnown: true }
  }

  async ClassifyQuery(q: ListQuery, onlyUnlabeled: boolean, includeAllFolders = false): Promise<ClassifyJob> {
    this.requireAI()
    await latency()
    const targets = this.classifyTargets(q, onlyUnlabeled, includeAllFolders)
    const jobId = `job_${++this.jobSeq}`
    const job = { cancelled: false }
    this.jobs.set(jobId, job)
    void (async () => {
      let done = 0
      let labeled = 0
      const skipped = 0
      let cost = 0
      const step = Math.max(25, Math.min(180, 6000 / Math.max(1, targets.length)))
      this.emit('classify:progress', { jobId, done, total: targets.length, labeled, skipped, failed: 0, cost })
      for (const m of targets) {
        if (job.cancelled) break
        await sleep(step)
        const r = this.classifyOne(m)
        this.applyResult(m, r)
        // Like the backend: "labeled" = labels changed; unchanged mail isn't "skipped" (that means cached).
        if (r.added.length || r.removed.length) labeled++
        done++
        cost += r.cost
        this.emit('classify:progress', { jobId, done, total: targets.length, labeled, skipped, failed: 0, cost })
        if (done % 5 === 0) this.changed()
      }
      this.jobs.delete(jobId)
      this.changed()
      this.emit('classify:finished', { jobId, done, total: targets.length, labeled, skipped, failed: 0, cost, cancelled: job.cancelled, error: '' })
    })()
    return { jobId, total: targets.length }
  }

  async CancelClassify(jobId: string): Promise<void> {
    const job = this.jobs.get(jobId)
    if (job) job.cancelled = true
  }

  /** Like the backend's Quiet hook: muted mailbox, Junk, or a quiet label of that mailbox. */
  private quiet(m: Message): boolean {
    if (this.settings.mutedMailboxes?.includes(m.accountId)) return true
    const quiet = this.settings.quietLabels?.[m.accountId] ?? ['junk']
    return m.labels.some((l) => l === 'junk' || quiet.includes(l))
  }

  /** Like the backend: labels only (Junk rules act on new mail), counts messages that got a label. */
  async ApplyLabelRules(): Promise<number> {
    await latency(100, 300)
    const rules = (this.settings.labelRules ?? []).filter((r) => r.action === 'label' && r.labelId && r.contains.trim())
    const valid = new Set(this.allLabels().map((l) => l.id))
    const changed = new Set<string>()
    for (const m of this.messages.values()) {
      for (const r of rules) {
        if (r.accountId && r.accountId !== m.accountId) continue
        if (!valid.has(r.labelId)) continue
        const hay = r.field === 'subject' ? m.subject : `${m.from.name} ${m.from.email}`
        if (!hay.toLowerCase().includes(r.contains.trim().toLowerCase())) continue
        if (!m.labels.includes(r.labelId)) {
          m.labels.push(r.labelId)
          m.labelDetails = [...(m.labelDetails ?? []), { labelId: r.labelId, source: 'manual', confidence: 1 }]
          changed.add(m.id)
        }
      }
    }
    if (changed.size) {
      this.emit('messages:changed', { accountId: '' })
      this.emit('labels:changed', { accountId: '' })
    }
    return changed.size
  }

  async ParseEventText(text: string): Promise<EventDraft> {
    await latency(300, 700)
    // Tiny stand-in for the model: "… at 12:30 …" sets the time, the rest is the title.
    const t = /\b(\d{1,2})[:.](\d{2})\b/.exec(text)
    const d = new Date(Date.now() + 86_400_000)
    const ymd = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    return {
      title: text.replace(/\b(tomorrow|morgen)\b|\b\d{1,2}[:.]\d{2}\b/gi, '').replace(/\s+/g, ' ').trim(),
      date: ymd, startTime: t ? `${t[1].padStart(2, '0')}:${t[2]}` : '', endDate: '', endTime: '', allDay: !t,
      location: '', description: '', attendees: [], videoCall: /\b(teams|meet|video)\b/i.test(text), repeat: 'none',
    }
  }

  async SummarizeMessage(id: string): Promise<string> {
    this.requireAI()
    await sleep(1300)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    const text = (m.bodyText || m.snippet).replace(/\s+/g, ' ').trim()
    const sentences = text.split(/(?<=[.!?])\s+/).filter((s) => s.length > 20).slice(0, 3)
    return `${m.from.name || m.from.email} writes about “${m.subject}”.\n${sentences.map((s) => `• ${s}`).join('\n')}`
  }

  async ImproveGrammar(text: string): Promise<string> {
    this.requireAI()
    await sleep(1600 + Math.random() * 600)
    const fix = (s: string) => s
      .replace(/\bteh\b/gi, 'the').replace(/\brecieve/gi, 'receive').replace(/\bdefinately\b/gi, 'definitely')
      .replace(/\balot\b/gi, 'a lot').replace(/\bdont\b/gi, "don't").replace(/\bcant\b/gi, "can't").replace(/\bwont\b/gi, "won't")
      .replace(/\bim\b/gi, "I'm").replace(/\bi\b/g, 'I').replace(/\bthier\b/gi, 'their').replace(/\bseperate/gi, 'separate')
      .replace(/\byour welcome\b/gi, "you're welcome").replace(/ {2,}/g, ' ').replace(/\s+([,.!?])/g, '$1')
      .replace(/(^|[.!?]\s+)([a-z])/g, (_m, p: string, c: string) => p + c.toUpperCase())
    if (/<[a-z][\s\S]*>/i.test(text)) {
      const doc = new DOMParser().parseFromString(`<body>${text}</body>`, 'text/html')
      const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT)
      for (let n = walker.nextNode(); n; n = walker.nextNode()) n.nodeValue = fix(n.nodeValue ?? '')
      return doc.body.innerHTML
    }
    return fix(text)
  }

  // Legacy names from the first contract draft.
  async ClassifyFolder(q: ListQuery): Promise<number> {
    const est = await this.EstimateClassify(q, false, false)
    await this.ClassifyQuery(q, false, false)
    return est.count
  }
}

export const mock = new MockBackend()
export type Mock = MockBackend
