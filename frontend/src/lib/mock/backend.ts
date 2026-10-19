// In-memory mock of the Go backend, used when the UI runs in a plain browser
// (`npm run dev` without Wails). It mirrors the bound App methods closely
// enough that every screen can be demoed and tested.
import type {
  Account, AIModel, AppInfo, ClassificationResult, ClassifyEstimate, ComposeRequest, DefaultPrompts,
  EventName, EventPayloads, Folder, LabelCount, ListQuery, Message, MessageLabel, MessagePage, MessageSummary,
  SearchQuery, SearchResult, Settings,
} from '../types'
import {
  DEFAULT_CLASSIFY_INSTRUCTIONS, DEFAULT_GRAMMAR_PROMPT, INCOMING, MOCK_MODELS, SEED_ACCOUNTS, SEED_SETTINGS,
  asTemplateAccount, buildMessage, buildSeedMessages, buildWelcome, foldersFor,
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

interface ParsedSearch {
  terms: string[]
  phrases: string[]
  from: string[]
  to: string[]
  subject: string[]
  labels: string[]
  inKind: string
  unread?: boolean
  starred?: boolean
  hasAttachment?: boolean
  before?: number
  after?: number
}

function parseSearch(q: string): ParsedSearch {
  const out: ParsedSearch = { terms: [], phrases: [], from: [], to: [], subject: [], labels: [], inKind: '' }
  const re = /(\w+):("([^"]*)"|\S+)|"([^"]*)"|(\S+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(q))) {
    if (m[1]) {
      const key = m[1].toLowerCase()
      const val = (m[3] ?? m[2]).toLowerCase()
      if (key === 'from') out.from.push(val)
      else if (key === 'to') out.to.push(val)
      else if (key === 'subject') out.subject.push(val)
      else if (key === 'label') out.labels.push(val)
      else if (key === 'in') out.inKind = val
      else if (key === 'is' && val === 'unread') out.unread = true
      else if (key === 'is' && val === 'read') out.unread = false
      else if (key === 'is' && val === 'starred') out.starred = true
      else if (key === 'has' && val.startsWith('attach')) out.hasAttachment = true
      else if (key === 'before') out.before = Date.parse(val)
      else if (key === 'after') out.after = Date.parse(val)
      else out.terms.push(`${key}:${val}`)
    } else if (m[4] !== undefined) {
      if (m[4]) out.phrases.push(m[4].toLowerCase())
    } else if (m[5]) {
      out.terms.push(m[5].toLowerCase())
    }
  }
  return out
}

function words(s: string): string[] {
  return s.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean)
}

class MockBackend {
  private accounts: Account[] = clone(SEED_ACCOUNTS)
  private folders = new Map<string, Folder[]>()
  private messages = new Map<string, Message>()
  private settings: Settings = clone(SEED_SETTINGS)
  private listeners = new Map<string, Set<Listener>>()
  private addAccountCancel: (() => void) | null = null
  private jobs = new Map<string, { cancelled: boolean }>()
  private changedTimer: ReturnType<typeof setTimeout> | null = null
  private incomingIdx = 0
  private jobSeq = 0

  constructor() {
    for (const a of this.accounts) this.folders.set(a.id, foldersFor(a.id, a.provider))
    for (const m of buildSeedMessages()) this.messages.set(m.id, m)
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
    return clone(s)
  }

  private labelNameToId(name: string): string | undefined {
    const n = name.toLowerCase()
    return this.settings.labels.find((l) => l.id === n || l.name.toLowerCase() === n || l.name.toLowerCase().replace(/\s+/g, '-') === n)?.id
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
    const source = matchedIn === 'subject' ? m.subject : matchedIn === 'from' ? `${m.from.name} <${m.from.email}>` : (m.bodyText || m.snippet).replace(/\s+/g, ' ')
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

  private filter(q: Partial<ListQuery>): Message[] {
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
      if (parsed && !this.matchSearch(m, parsed).ok) continue
      out.push(m)
    }
    out.sort((a, b) => (a.date === b.date ? (a.id < b.id ? 1 : -1) : a.date < b.date ? 1 : -1))
    return out
  }

  // ---------------------------------------------------------------- accounts
  async GetAccounts(): Promise<Account[]> {
    await latency()
    return clone(this.accounts.map((a) => ({
      ...a,
      unreadCount: this.filter({ accountId: a.id, folderKind: 'inbox' }).filter((m) => m.unread).length,
    })))
  }

  async AddAccount(provider: string): Promise<Account> {
    if (provider === 'gmail' && !this.settings.googleClientId) throw new Error('Google OAuth client ID is not configured. Add it in Settings → Accounts.')
    if (provider === 'outlook' && !this.settings.microsoftClientId) throw new Error('Microsoft OAuth client ID is not configured. Add it in Settings → Accounts.')
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
    this.accounts.push(acc)
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
    for (const [mid, m] of this.messages) if (m.accountId === id) this.messages.delete(mid)
    this.emit('accounts:changed')
    this.changed()
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
    return clone(list.map((f) => {
      const msgs = this.filter({ accountId, folderId: f.id })
      return { ...f, total: msgs.length, unread: msgs.filter((m) => m.unread).length }
    }))
  }

  async ListMessages(q: ListQuery): Promise<MessagePage> {
    await latency(90, 260)
    const all = this.filter(q)
    let start = 0
    if (q.cursor) {
      const [date, id] = atob(q.cursor).split('|')
      start = all.findIndex((m) => m.date < date || (m.date === date && m.id < id))
      if (start < 0) start = all.length
    }
    const limit = q.limit > 0 ? q.limit : 50
    const page = all.slice(start, start + limit)
    const last = page[page.length - 1]
    const more = start + limit < all.length
    return {
      messages: page.map((m) => this.summary(m)),
      nextCursor: more && last ? btoa(`${last.date}|${last.id}`) : '',
      total: all.length,
    }
  }

  async GetMessage(id: string): Promise<Message> {
    await latency(50, 150)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    return clone(m)
  }

  async GetThread(id: string): Promise<Message[]> {
    await latency(60, 180)
    const m = this.messages.get(id)
    if (!m) throw new Error('message not found')
    const thread = [...this.messages.values()].filter((x) => x.threadId === m.threadId && x.accountId === m.accountId)
    thread.sort((a, b) => (a.date < b.date ? -1 : 1))
    return clone(thread)
  }

  private mutate(ids: string[], fn: (m: Message) => void) {
    const accs = new Set<string>()
    for (const id of ids) {
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
    this.mutate(ids, (m) => (m.unread = !read))
  }

  async SetStarred(ids: string[], starred: boolean): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => (m.starred = starred))
  }

  async Archive(ids: string[]): Promise<void> {
    await latency(50, 140)
    this.mutate(ids, (m) => {
      m.folderIds = [...m.folderIds.filter((f) => this.kindOf(f) !== 'inbox' && this.kindOf(f) !== 'custom'), `${m.accountId}:archive`]
      m.folderIds = [...new Set(m.folderIds)]
    })
  }

  async Trash(ids: string[]): Promise<void> {
    await latency(50, 140)
    this.mutate(ids, (m) => (m.folderIds = [`${m.accountId}:trash`]))
  }

  async MoveToFolder(ids: string[], folderId: string): Promise<void> {
    await latency(50, 140)
    this.mutate(ids, (m) => {
      if (folderId.split(':')[0] !== m.accountId) return
      m.folderIds = this.kindOf(folderId) === 'custom' ? [`${m.accountId}:inbox`, folderId] : [folderId]
    })
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
      await sleep(1200 + Math.random() * 800)
      for (const a of targets) {
        let newMessages = 0
        // Every other sync of the first target brings in one new message.
        if (a === targets[0] && this.incomingIdx % 2 === 0) {
          const tpl = asTemplateAccount(INCOMING[(this.incomingIdx / 2) % INCOMING.length], this.accounts.map((x) => x.id))
          if (tpl.acc === a.id || !accountId) {
            const msg = buildMessage({ ...tpl, acc: accountId ? a.id : tpl.acc }, Date.now())
            if (!this.settings.autoClassify) { msg.labels = []; msg.labelDetails = [] }
            this.messages.set(msg.id, msg)
            newMessages = 1
            this.emit('mail:new', { accountId: msg.accountId, count: 1, latest: this.summary(msg) })
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
    return clone(this.settings)
  }

  async SaveSettings(s: Settings): Promise<void> {
    await latency(30, 90)
    const keySet = this.settings.openRouterKeySet
    this.settings = { ...clone(s), openRouterKeySet: keySet }
    const valid = new Set(this.settings.labels.map((l) => l.id))
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

  async ListAIModels(): Promise<AIModel[]> {
    await sleep(350)
    return clone(MOCK_MODELS)
  }

  async GetDefaultPrompts(): Promise<DefaultPrompts> {
    await latency(20, 50)
    return { grammar: DEFAULT_GRAMMAR_PROMPT, classify: DEFAULT_CLASSIFY_INSTRUCTIONS }
  }

  async GetAppInfo(): Promise<AppInfo> {
    await latency(20, 50)
    return { version: '0.1.0-dev', dataDir: '~/.config/gomail', logFile: '~/.config/gomail/gomail.log', platform: 'browser/mock', demo: true }
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
    }
    const scores = this.settings.labels.map((l) => {
      const kw = [...(hints[l.id] ?? []), ...words(`${l.name} ${l.description}`).filter((w) => w.length > 4)]
      return { id: l.id, s: kw.reduce((acc, k) => acc + (text.includes(k) ? 1 : 0), 0) + (m.labels.includes(l.id) ? 1.5 : 0) }
    })
    const total = scores.reduce((a, b) => a + Math.exp(b.s), Math.exp(0.6))
    const ranked = scores.map((x) => ({ id: x.id, p: Math.exp(x.s) / total })).sort((a, b) => b.p - a.p)
    const best = ranked[0]
    const reason = ranked.slice(0, 3).map((r) => `${this.settings.labels.find((l) => l.id === r.id)?.name ?? r.id} ${Math.round(r.p * 100)}%`).join(' · ')
    const confidence = best ? Math.min(0.99, Math.round(best.p * 100) / 100) : 0
    const ok = best && confidence >= this.settings.classifyThreshold
    return { messageId: m.id, label: ok ? best.id : '', confidence, reason }
  }

  private applyResult(m: Message, r: ClassificationResult) {
    if (!r.label) return
    if (!m.labels.includes(r.label)) m.labels = [...m.labels, r.label]
    const details: MessageLabel[] = (m.labelDetails ?? []).filter((d) => d.labelId !== r.label)
    details.push({ labelId: r.label, source: 'ai', confidence: r.confidence })
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

  async AddLabel(ids: string[], labelId: string): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => {
      if (!m.labels.includes(labelId)) m.labels = [...m.labels, labelId]
      m.labelDetails = [...(m.labelDetails ?? []).filter((d) => d.labelId !== labelId), { labelId, source: 'manual', confidence: 1 }]
    })
  }

  async RemoveLabel(ids: string[], labelId: string): Promise<void> {
    await latency(30, 90)
    this.mutate(ids, (m) => {
      m.labels = m.labels.filter((l) => l !== labelId)
      m.labelDetails = (m.labelDetails ?? []).filter((d) => d.labelId !== labelId)
    })
  }

  async GetLabelCounts(accountId: string): Promise<LabelCount[]> {
    await latency(30, 80)
    const counts = new Map<string, LabelCount>()
    for (const m of this.messages.values()) {
      if (accountId && m.accountId !== accountId) continue
      for (const l of m.labels) {
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
    return this.filter({ ...q, cursor: '' }).slice(0, max > 0 ? max : 10000).map((m) => m.id)
  }

  private classifyTargets(q: ListQuery, onlyUnlabeled: boolean): Message[] {
    // Spam/Junk is labeled like normal mail; Trash and Drafts only on request.
    const includeAll = !!q.includeTrashDrafts
    const base = !q.folderId && !q.folderKind
      ? [...this.messages.values()].filter((m) => (!q.accountId || m.accountId === q.accountId) && (!q.unreadOnly || m.unread)
        && (!(q.labels?.length || q.unlabeled) || q.labels.some((l) => m.labels.includes(l)) || (q.unlabeled && m.labels.length === 0)))
      : this.filter({ ...q, cursor: '' })
    return base.filter((m) => (includeAll || !this.neverLabel(m)) && (!onlyUnlabeled || m.labels.length === 0))
  }

  async EstimateClassify(q: ListQuery, onlyUnlabeled: boolean): Promise<ClassifyEstimate> {
    await latency(80, 200)
    const count = this.classifyTargets(q, onlyUnlabeled).length
    return { count, estimatedCost: count * 0.00011, model: this.settings.classifierModel }
  }

  async ClassifyQuery(q: ListQuery, onlyUnlabeled: boolean): Promise<string> {
    this.requireAI()
    await latency()
    const targets = this.classifyTargets(q, onlyUnlabeled)
    const jobId = `job_${++this.jobSeq}`
    const job = { cancelled: false }
    this.jobs.set(jobId, job)
    void (async () => {
      let done = 0
      let labeled = 0
      let skipped = 0
      let cost = 0
      const step = Math.max(25, Math.min(180, 6000 / Math.max(1, targets.length)))
      this.emit('classify:progress', { jobId, done, total: targets.length, labeled, skipped, cost })
      for (const m of targets) {
        if (job.cancelled) break
        await sleep(step)
        const r = this.classifyOne(m)
        if (r.label) { this.applyResult(m, r); labeled++ } else skipped++
        done++
        cost += 0.00009 + Math.random() * 0.00004
        this.emit('classify:progress', { jobId, done, total: targets.length, labeled, skipped, cost })
        if (done % 5 === 0) this.changed()
      }
      this.jobs.delete(jobId)
      this.changed()
      this.emit('classify:finished', { jobId, done, total: targets.length, labeled, skipped, cost, cancelled: job.cancelled, finished: true })
    })()
    return jobId
  }

  async CancelClassify(jobId: string): Promise<void> {
    const job = this.jobs.get(jobId)
    if (job) job.cancelled = true
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
    const est = await this.EstimateClassify(q, false)
    await this.ClassifyQuery(q, false)
    return est.count
  }
}

export const mock = new MockBackend()
export type Mock = MockBackend
