// The ONLY module that talks to the Wails (v3) backend. Everything else in
// the UI imports `api` from here. When the page is not running inside a Wails
// webview (plain browser via `npm run dev`) every call is routed to the
// in-memory mock in ./mock.
import type {
  Account, AIModel, AppInfo, AttendeeResponse, Calendar, CalendarEvent, EventInput, RecurrenceScope, ClassificationResult, ClassifyEstimate, ClassifyJob, ComposeRequest, DefaultPrompts, EventName,
  EventPayloads, Folder, LabelCount, AttachmentData, Contact, AIUsage, ListQuery, Message, MessagePage, MessageSummary, SearchQuery, SearchResult, Settings, UpdateInfo,
} from './types'
import { isSafeExternalUrl } from './format'
import { LABEL_COLORS as LABEL_FALLBACK, safeColor } from './color'
import { DEFAULT_PRIMARY, DEFAULT_RADIUS } from './theme'

type AnyFn = (...args: unknown[]) => Promise<unknown>

// Generated v3 bindings (`wails3 generate bindings -ts`), discovered at build
// time so the app does not depend on exact service/package paths and still
// builds before the bindings exist. They (and the Wails runtime) are loaded
// lazily by initBackend(), only inside a Wails webview, keeping them out of
// the main bundle and away from the browser mock.
const bindingLoaders = import.meta.glob<Record<string, unknown>>(
  ['../../bindings/**/*.ts', '../../bindings/**/*.js', '!../../bindings/github.com/wailsapp/**', '!**/models.ts', '!**/models.js', '!**/index.ts', '!**/index.js', '!**/*.d.ts'],
)

type WailsRuntime = typeof import('@wailsio/runtime')
let runtime: WailsRuntime | null = null
const registry = new Map<string, AnyFn>()

function register(mod: Record<string, unknown>) {
  for (const [name, value] of Object.entries(mod)) {
    // Service methods are exported as PascalCase functions; model classes have a createFrom.
    if (typeof value === 'function' && /^[A-Z]/.test(name) && !('createFrom' in value) && !registry.has(name)) {
      registry.set(name, value as AnyFn)
    }
  }
}

/** Load the Wails runtime + bindings (no-op in the browser mock). Call before mounting. */
export async function initBackend(): Promise<void> {
  // Browser preview of the macOS chrome: ?chrome=mac
  if (new URLSearchParams(location.search).get('chrome') === 'mac') setWindowChrome('mac')
  if (!isWails()) return
  const [rt, ...mods] = await Promise.all([import('@wailsio/runtime'), ...Object.values(bindingLoaders).map((load) => load())])
  runtime = rt
  mods.forEach(register)
  try {
    if (rt.System.IsMac()) setWindowChrome('mac')
  } catch {
    /* environment not injected yet: settings store falls back to GetAppInfo().platform */
  }
}

/** "mac" = hidden inset title bar (traffic lights over the content, header drag regions). Windows/Linux keep native frames. */
export function setWindowChrome(kind: 'mac' | 'native') {
  if (kind === 'mac') document.documentElement.dataset.chrome = 'mac'
  else delete document.documentElement.dataset.chrome
}

let mode: 'wails' | 'mock' | null = null
let mockPromise: Promise<typeof import('./mock/backend')> | null = null

interface HostWindow {
  chrome?: { webview?: { postMessage?: unknown } }
  webkit?: { messageHandlers?: { external?: { postMessage?: unknown } } }
}

/** True inside a Wails webview (WebView2 / WKWebView / WebKitGTK). VITE_MOCK=1|0 overrides. */
export function isWails(): boolean {
  if (mode === null) {
    const forced = import.meta.env.VITE_MOCK as string | undefined
    const w = window as unknown as HostWindow
    const host = !!(w.chrome?.webview?.postMessage || w.webkit?.messageHandlers?.external?.postMessage)
    mode = forced === '1' ? 'mock' : forced === '0' ? 'wails' : host ? 'wails' : 'mock'
  }
  return mode === 'wails'
}

function loadMock() {
  // Lazy so the mock data never ships in the critical path of the real app.
  mockPromise ??= import('./mock/backend')
  return mockPromise
}

export class NotSupportedError extends Error {
  constructor(method: string) {
    super(`${method} is not available in this version of the backend`)
    this.name = 'NotSupportedError'
  }
}

// The core service names some methods without the Get prefix; accept both
// so a facade rename never silently breaks a feature.
const ALIASES: Record<string, string[]> = {
  GetAccounts: ['Accounts'],
  GetFolders: ['Folders'],
  GetMessage: ['Message'],
  GetThread: ['Thread'],
  GetLabelCounts: ['LabelCounts'],
  SendMessage: ['Send'],
  GetAppInfo: ['Info', 'AppInfo'],
}

function boundFn(name: string): AnyFn | undefined {
  const direct = registry.get(name)
  if (direct) return direct
  for (const alt of ALIASES[name] ?? []) {
    const fn = registry.get(alt)
    if (fn) return fn
  }
  return undefined
}

export function hasMethod(name: string): boolean {
  return isWails() ? !!boundFn(name) : true
}

/** Every backend method the UI uses; logged when missing so gaps are obvious in dev. */
export const USED_METHODS = [
  'GetAccounts', 'AddAccount', 'CancelAddAccount', 'RemoveAccount', 'UpdateAccount', 'GetFolders', 'ListMessages', 'GetMessage',
  'GetThread', 'MarkRead', 'SetStarred', 'Archive', 'Trash', 'MoveToFolder', 'SendMessage', 'SaveAttachment', 'SyncNow',
  'OpenExternalURL', 'SearchMessages', 'ResolveQueryIDs', 'GetSettings', 'SaveSettings', 'SetOpenRouterKey', 'GetAppInfo',
  'GetDefaultPrompts', 'TestOpenRouter', 'ListAIModels', 'ClassifyMessages', 'AddLabel', 'RemoveLabel', 'GetLabelCounts',
  'EstimateClassify', 'ClassifyQuery', 'CancelClassify', 'SummarizeMessage', 'ImproveGrammar',
]
/** Backend pass 2 methods: features hide gracefully while these are missing. */
/** Calendar (backend calendar pass). The UI hides the calendar when ListCalendars is missing. */
const CALENDAR_PREFS = ['calendarWeekStart', 'calendarDefaultView', 'calendarDefaultDuration', 'calendarShowDeclined', 'calendarDefaultReminder'] as const

export const CALENDAR_METHODS = ['ListCalendars', 'SetCalendarVisible', 'SetAccountCalendarEnabled', 'EnableCalendar', 'ListEvents', 'GetEvent', 'CreateEvent', 'UpdateEvent', 'DeleteEvent', 'RespondToEvent']

export const OPTIONAL_METHODS = ['GetAIUsage', 'UpdateAccountSignature', 'SearchContacts', 'EmptyFolder', 'MarkFolderRead', 'GetMessageWithRemoteContent', 'LoadOlder', 'ReauthAccount', 'Unsubscribe', 'GetAttachmentData', 'SaveDraft', 'DeleteDraft', 'RefreshAIModels', 'CheckForUpdate', 'GetPendingCompose', ...CALENDAR_METHODS]

export function missingMethods(): string[] {
  return isWails() ? USED_METHODS.filter((m) => !boundFn(m)) : []
}

async function call<T>(name: string, ...args: unknown[]): Promise<T> {
  if (isWails()) {
    const fn = boundFn(name)
    if (!fn) throw new NotSupportedError(name)
    try {
      return (await fn(...args)) as T
    } catch (e) {
      throw toError(e)
    }
  }
  const { mock } = await loadMock()
  const fn = (mock as unknown as Record<string, AnyFn | undefined>)[name]
  if (!fn) throw new NotSupportedError(name)
  return (await fn.apply(mock, args)) as T
}

/** Wails rejects with plain strings; normalise to Error. */
export function toError(e: unknown): Error {
  if (e instanceof Error) return e
  if (typeof e === 'string') return new Error(e)
  try {
    return new Error(JSON.stringify(e))
  } catch {
    return new Error(String(e))
  }
}

/** Human-friendly message for toasts (strips Go error prefixes / JSON wrappers). */
export function errorMessage(e: unknown): string {
  let msg = toError(e).message.replace(/^Error:\s*/, '')
  try {
    const parsed = JSON.parse(msg) as { message?: string }
    if (parsed && typeof parsed.message === 'string') msg = parsed.message
  } catch {
    /* not JSON */
  }
  if (/not available in this version/.test(msg)) return 'This feature needs a newer version of the gomail backend.'
  if (/context deadline exceeded|timeout/i.test(msg)) return 'The request timed out. Check your connection and try again.'
  if (/no such host|network is unreachable|connection refused/i.test(msg)) return 'Can’t reach the server. Check your internet connection.'
  return msg.charAt(0).toUpperCase() + msg.slice(1)
}

// ---- normalisation (Go nil slices arrive as null) ----

export function normSummary<T extends MessageSummary>(m: T): T {
  const legacy = (m as unknown as { category?: string }).category
  m.folderIds ??= []
  m.to ??= []
  m.labels ??= legacy ? [legacy] : []
  m.labelDetails ??= []
  m.from ??= { name: '', email: '' }
  return m
}

export function normMessage(m: Message): Message {
  normSummary(m)
  m.cc ??= []
  m.bcc ??= []
  m.replyTo ??= []
  m.attachments ??= []
  m.bodyHtml ??= ''
  m.bodyText ??= ''
  return m
}

export function normEvent(e: CalendarEvent): CalendarEvent {
  return {
    ...e,
    title: e.title ?? '',
    location: e.location ?? '',
    description: e.description ?? '',
    attendees: e.attendees ?? [],
    reminders: e.reminders ?? [],
    organizer: e.organizer ?? { name: '', email: '' },
    myResponse: e.myResponse ?? '',
    meetingUrl: e.meetingUrl ?? '',
    recurrence: e.recurrence ?? '',
    seriesId: e.seriesId ?? '',
    timeZone: e.timeZone ?? '',
    status: e.status || 'confirmed',
  }
}

export function normSettings(s: Settings): Settings {
  const legacy = (s as unknown as { categories?: Settings['labels'] }).categories
  return {
    ...s,
    theme: s.theme || 'system',
    density: s.density || 'comfortable',
    // Explicit checks (not ||): only missing/invalid values fall back to defaults.
    syncIntervalMinutes: typeof s.syncIntervalMinutes === 'number' && s.syncIntervalMinutes > 0 ? s.syncIntervalMinutes : 5,
    labels: (s.labels ?? legacy ?? []).map((l, i) => ({ ...l, name: l.name ?? '', description: l.description ?? '', color: safeColor(l.color, LABEL_FALLBACK[i % LABEL_FALLBACK.length]) })),
    primaryColor: /^#[0-9a-f]{6}$/i.test(s.primaryColor ?? '') ? s.primaryColor : DEFAULT_PRIMARY,
    radius: typeof s.radius === 'number' && s.radius > 0 ? s.radius : s.radius === 0 ? 0 : DEFAULT_RADIUS,
    classifierModel: s.classifierModel ? s.classifierModel : 'typesafe/jev-1.13',
    classifyThreshold: typeof s.classifyThreshold === 'number' && s.classifyThreshold > 0 ? s.classifyThreshold : 0.5,
    classifyRemoveThreshold: typeof s.classifyRemoveThreshold === 'number' ? s.classifyRemoveThreshold : 0.15,
    aiCorrectsProviderLabels: s.aiCorrectsProviderLabels ?? true,
    importProviderLabels: s.importProviderLabels ?? true,
    grammarSystemPrompt: s.grammarSystemPrompt ?? '',
    classifyInstructions: s.classifyInstructions ?? '',
    microsoftTenant: s.microsoftTenant || 'common',
    notifications: s.notifications ?? true,
    // Not (yet) in the Go Settings struct: fall back to the locally stored value.
    showInTray: s.showInTray ?? readLocal('showInTray', true),
    closeToTray: s.closeToTray ?? readLocal('closeToTray', false),
    conversationView: s.conversationView ?? readLocal('conversationView', true),
    markReadMode: s.markReadMode ?? readLocal('markReadMode', 'immediate'),
    readingPane: s.readingPane ?? readLocal('readingPane', 'right'),
    zoom: typeof s.zoom === 'number' && s.zoom > 0 ? s.zoom : readLocal('zoom', 1),
    emailBackground: s.emailBackground === 'light' ? 'light' : 'auto',
    launchAtLogin: s.launchAtLogin ?? false,
    checkUpdates: s.checkUpdates ?? true,
    aiDailyBudgetUsd: s.aiDailyBudgetUsd ?? 1,
    syncDepthDays: s.syncDepthDays ?? 90,
    undoSendSeconds: s.undoSendSeconds ?? readLocal('undoSendSeconds', 5),
    // Calendar preferences: backend values win; older backends keep a local copy.
    calendarWeekStart: s.calendarWeekStart ?? readLocal('calendarWeekStart', -1),
    calendarDefaultView: s.calendarDefaultView || readLocal('calendarDefaultView', 'week'),
    calendarDefaultDuration: s.calendarDefaultDuration || readLocal('calendarDefaultDuration', 30),
    calendarShowDeclined: s.calendarShowDeclined ?? readLocal('calendarShowDeclined', false),
    calendarDefaultReminder: s.calendarDefaultReminder ?? readLocal('calendarDefaultReminder', 10),
  }
}

const LOCAL_PREFS = 'gomail.localPrefs.v1'
export function readLocal<T>(key: string, fallback: T): T {
  try {
    const all = JSON.parse(localStorage.getItem(LOCAL_PREFS) || '{}') as Record<string, unknown>
    return key in all ? (all[key] as T) : fallback
  } catch {
    return fallback
  }
}
export function writeLocal(key: string, value: unknown) {
  try {
    const all = JSON.parse(localStorage.getItem(LOCAL_PREFS) || '{}') as Record<string, unknown>
    all[key] = value
    localStorage.setItem(LOCAL_PREFS, JSON.stringify(all))
  } catch {
    /* ignore */
  }
}

function normQuery(q: Partial<ListQuery>): ListQuery {
  return {
    accountId: q.accountId ?? '',
    folderId: q.folderId ?? '',
    folderKind: q.folderKind ?? '',
    labels: q.labels ?? [],
    unlabeled: q.unlabeled ?? false,
    search: q.search ?? '',
    unreadOnly: q.unreadOnly ?? false,
    limit: q.limit ?? 50,
    cursor: q.cursor ?? '',
  }
}

export const api = {
  // ---- accounts
  getAccounts: async () => ((await call<Account[] | null>('GetAccounts')) ?? []).map((a) => ({ ...a, color: safeColor(a.color, '#64748b') })),
  addAccount: (provider: string) => call<Account>('AddAccount', provider),
  cancelAddAccount: () => call<void>('CancelAddAccount'),
  removeAccount: (id: string) => call<void>('RemoveAccount', id),
  updateAccount: (id: string, displayName: string, color: string) => call<void>('UpdateAccount', id, displayName, color),

  // ---- folders & messages
  getFolders: async (accountId: string) => (await call<Folder[] | null>('GetFolders', accountId)) ?? [],
  listMessages: async (q: Partial<ListQuery>): Promise<MessagePage> => {
    const page = await call<MessagePage>('ListMessages', normQuery(q))
    return { messages: (page.messages ?? []).map(normSummary), nextCursor: page.nextCursor ?? '', total: page.total ?? 0, hasMoreRemote: !!page.hasMoreRemote }
  },
  getMessage: async (id: string) => normMessage(await call<Message>('GetMessage', id)),
  getThread: async (id: string) => ((await call<Message[] | null>('GetThread', id)) ?? []).map(normMessage),
  markRead: (ids: string[], read: boolean) => call<void>('MarkRead', ids, read),
  setStarred: (ids: string[], starred: boolean) => call<void>('SetStarred', ids, starred),
  archive: (ids: string[]) => call<void>('Archive', ids),
  trash: (ids: string[]) => call<void>('Trash', ids),
  moveToFolder: (ids: string[], folderId: string) => call<void>('MoveToFolder', ids, folderId),
  sendMessage: (req: ComposeRequest) => call<void>('SendMessage', req),
  saveAttachment: (messageId: string, attachmentId: string) => call<void>('SaveAttachment', messageId, attachmentId),
  syncNow: (accountId: string) => call<void>('SyncNow', accountId),
  openExternal: async (url: string) => {
    if (!isSafeExternalUrl(url)) throw new Error('This link can’t be opened (only http and https links are allowed)')
    if (isWails()) {
      if (boundFn('OpenExternalURL')) return call<void>('OpenExternalURL', url)
      return runtime?.Browser.OpenURL(url)
    }
    return call<void>('OpenExternalURL', url)
  },

  /** AI spend so far (next backend pass). */
  getAIUsage: () => call<AIUsage>('GetAIUsage'),

  // ---- calendar
  listCalendars: async () => ((await call<Calendar[] | null>('ListCalendars')) ?? []).map((c) => ({ ...c, color: safeColor(c.color, '#64748b') })),
  setCalendarVisible: (id: string, visible: boolean) => call<void>('SetCalendarVisible', id, visible),
  setAccountCalendarEnabled: (accountId: string, enabled: boolean) => call<void>('SetAccountCalendarEnabled', accountId, enabled),
  /** Re-runs OAuth to add calendar scopes to an existing account. */
  enableCalendar: (accountId: string) => call<void>('EnableCalendar', accountId),
  /** Events overlapping [start, end) (RFC 3339), recurring events expanded into occurrences. */
  listEvents: async (start: string, end: string) => ((await call<CalendarEvent[] | null>('ListEvents', start, end)) ?? []).map(normEvent),
  getEvent: async (id: string) => normEvent(await call<CalendarEvent>('GetEvent', id)),
  createEvent: async (input: EventInput) => normEvent(await call<CalendarEvent>('CreateEvent', input)),
  updateEvent: async (id: string, input: EventInput, scope: RecurrenceScope) => normEvent(await call<CalendarEvent>('UpdateEvent', id, input, scope)),
  deleteEvent: (id: string, scope: RecurrenceScope) => call<void>('DeleteEvent', id, scope),
  respondToEvent: (id: string, response: AttendeeResponse) => call<void>('RespondToEvent', id, response),
  checkForUpdate: () => call<UpdateInfo>('CheckForUpdate'),
  /** A mailto: link that launched the app before the UI was ready (returned once), or null. */
  getPendingCompose: () => call<EventPayloads['compose:open'] | null>('GetPendingCompose'),
  updateAccountSignature: (id: string, html: string) => call<void>('UpdateAccountSignature', id, html),
  /** Frequency-ranked contacts from all mail (backend pass 3). */
  searchContacts: async (query: string, limit = 8): Promise<Contact[]> => (await call<Contact[] | null>('SearchContacts', query, limit)) ?? [],
  emptyFolder: (folderId: string) => call<void>('EmptyFolder', folderId),
  markFolderRead: (folderId: string) => call<void>('MarkFolderRead', folderId),
  /** UI zoom: native webview zoom in Wails, CSS zoom in the browser. */
  async setZoom(z: number) {
    if (isWails() && runtime) {
      await runtime.Window.SetZoom(z)
      return
    }
    ;(document.documentElement.style as CSSStyleDeclaration & { zoom: string }).zoom = String(z)
  },

  /** Fetch older mail from the server beyond the local cache (backend pass 2). */
  loadOlder: async (q: Partial<ListQuery>): Promise<number> => {
    const r = await call<number | { added?: number } | null>('LoadOlder', normQuery(q))
    return typeof r === 'number' ? r : r?.added ?? 0
  },
  reauthAccount: (id: string) => call<unknown>('ReauthAccount', id),
  /** Returns how it was done: "oneClick" | "mailto" (email sent) | "url" (opened in the browser). */
  unsubscribe: async (id: string): Promise<{ method: string; url: string }> => (await call<{ method: string; url: string } | null>('Unsubscribe', id)) ?? { method: '', url: '' },
  /** The message with remote images/CSS kept ("Load images"). */
  getMessageWithRemoteContent: async (id: string) => normMessage(await call<Message>('GetMessageWithRemoteContent', id)),
  getAttachmentData: async (messageId: string, attachmentId: string): Promise<AttachmentData> => {
    const r = await call<AttachmentData | string>('GetAttachmentData', messageId, attachmentId)
    return typeof r === 'string' ? { mimeType: '', dataBase64: r } : r
  },
  /** Returns the server draft id (backend pass 2); local autosave works without it. */
  saveDraft: async (req: ComposeRequest, draftId: string): Promise<string> => (await call<string | null>('SaveDraft', req, draftId)) ?? draftId,
  deleteDraft: (id: string) => call<void>('DeleteDraft', id),

  // ---- search & bulk queries
  searchMessages: async (q: SearchQuery): Promise<SearchResult[]> =>
    ((await call<SearchResult[] | null>('SearchMessages', q)) ?? []).map((r) => ({ ...r, message: normSummary(r.message) })),
  resolveQueryIds: async (q: Partial<ListQuery>, max: number) => (await call<string[] | null>('ResolveQueryIDs', normQuery(q), max)) ?? [],

  // ---- settings
  getSettings: async () => normSettings(await call<Settings>('GetSettings')),
  // Reading/sending preferences now live in the backend's Settings; the
  // readLocal() fallbacks in normSettings only cover older backends.
  saveSettings: (s: Settings) => {
    for (const k of CALENDAR_PREFS) if (s[k] !== undefined) writeLocal(k, s[k])
    return call<void>('SaveSettings', s)
  },

  setOpenRouterKey: (key: string) => call<void>('SetOpenRouterKey', key),
  getAppInfo: () => call<AppInfo>('GetAppInfo'),
  getDefaultPrompts: async (): Promise<DefaultPrompts> => {
    if (hasMethod('GetDefaultPrompts')) return call<DefaultPrompts>('GetDefaultPrompts')
    const grammar = hasMethod('GetDefaultGrammarPrompt') ? await call<string>('GetDefaultGrammarPrompt') : ''
    return { grammar, classify: '' }
  },

  // ---- labels & AI
  testOpenRouter: () => call<void>('TestOpenRouter'),
  listAIModels: async (force = false) =>
    ((await (force && hasMethod('RefreshAIModels') ? call<AIModel[] | null>('RefreshAIModels') : call<AIModel[] | null>('ListAIModels'))) ?? [])
      .map((m) => ({ ...m, kind: m.kind || 'chat' })),
  classifyMessages: async (ids: string[]): Promise<ClassificationResult[]> =>
    ((await call<ClassificationResult[] | null>('ClassifyMessages', ids)) ?? []).map((r) => {
      // Tolerate the older {label, confidence} shape.
      const legacy = r as unknown as { label?: string; confidence?: number }
      const added = r.added ?? (legacy.label ? [legacy.label] : [])
      const scores = r.scores ?? (legacy.label ? { [legacy.label]: legacy.confidence ?? 0 } : {})
      return { messageId: r.messageId, added, removed: r.removed ?? [], scores, cost: r.cost ?? 0, error: r.error ?? '', skipped: !!r.skipped }
    }),
  addLabel: (ids: string[], labelId: string) => call<void>('AddLabel', ids, labelId),
  removeLabel: (ids: string[], labelId: string) => call<void>('RemoveLabel', ids, labelId),
  getLabelCounts: async (accountId: string) => (await call<LabelCount[] | null>('GetLabelCounts', accountId)) ?? [],
  estimateClassify: async (q: Partial<ListQuery>, onlyUnlabeled: boolean, includeAllFolders: boolean): Promise<ClassifyEstimate> => {
    const e = await call<ClassifyEstimate & { estimatedCost?: number }>('EstimateClassify', normQuery(q), onlyUnlabeled, includeAllFolders)
    return { ...e, estimatedCostUsd: e.estimatedCostUsd ?? e.estimatedCost ?? 0, priceKnown: e.priceKnown ?? true }
  },
  classifyQuery: async (q: Partial<ListQuery>, onlyUnlabeled: boolean, includeAllFolders: boolean): Promise<ClassifyJob> => {
    const r = await call<ClassifyJob | string>('ClassifyQuery', normQuery(q), onlyUnlabeled, includeAllFolders)
    return typeof r === 'string' ? { jobId: r, total: 0 } : r
  },
  cancelClassify: (jobId: string) => call<void>('CancelClassify', jobId),
  summarizeMessage: (id: string) => call<string>('SummarizeMessage', id),
  improveGrammar: (text: string) => call<string>('ImproveGrammar', text),

  // ---- runtime
  on<E extends EventName>(event: E, cb: (payload: EventPayloads[E]) => void): () => void {
    if (isWails()) return runtime ? runtime.Events.On(event, (ev: { data: unknown }) => cb(ev.data as EventPayloads[E])) : () => {}
    let off: (() => void) | null = null
    let cancelled = false
    void loadMock().then(({ mock }) => {
      if (!cancelled) off = mock.on(event, cb as (p: unknown) => void)
    })
    return () => {
      cancelled = true
      off?.()
    }
  },

  async clipboardGet(): Promise<string> {
    if (isWails() && runtime) return runtime.Clipboard.Text()
    try {
      return await navigator.clipboard.readText()
    } catch {
      return ''
    }
  },

  async clipboardSet(text: string): Promise<void> {
    if (isWails() && runtime) {
      await runtime.Clipboard.SetText(text)
      return
    }
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      ta.remove()
    }
  },
}

export type Api = typeof api
