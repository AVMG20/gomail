// The ONLY module that talks to the Wails (v3) backend. Everything else in
// the UI imports `api` from here. When the page is not running inside a Wails
// webview (plain browser via `npm run dev`) every call is routed to the
// in-memory mock in ./mock.
import { Browser, Clipboard, Events } from '@wailsio/runtime'
import type {
  Account, AIModel, AppInfo, ClassificationResult, ClassifyEstimate, ComposeRequest, DefaultPrompts, EventName,
  EventPayloads, Folder, LabelCount, ListQuery, Message, MessagePage, MessageSummary, SearchQuery, SearchResult, Settings,
} from './types'
import { DEFAULT_PRIMARY, DEFAULT_RADIUS } from './theme'

type AnyFn = (...args: unknown[]) => Promise<unknown>

// Generated v3 bindings (`wails3 generate bindings -ts`), discovered at build
// time so the app does not depend on exact service/package paths and still
// builds before the bindings exist. Wails' own internal modules are skipped.
const bindingModules = import.meta.glob<Record<string, unknown>>(
  ['../../bindings/**/*.ts', '../../bindings/**/*.js', '!../../bindings/github.com/wailsapp/**', '!**/models.ts', '!**/models.js', '!**/index.ts', '!**/index.js', '!**/*.d.ts'],
  { eager: true },
)

const registry = new Map<string, AnyFn>()
for (const mod of Object.values(bindingModules)) {
  for (const [name, value] of Object.entries(mod)) {
    // Service methods are exported as PascalCase functions; model classes have a createFrom.
    if (typeof value === 'function' && /^[A-Z]/.test(name) && !('createFrom' in value) && !registry.has(name)) {
      registry.set(name, value as AnyFn)
    }
  }
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

function boundFn(name: string): AnyFn | undefined {
  return registry.get(name)
}

export function hasMethod(name: string): boolean {
  return isWails() ? registry.has(name) : true
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

export function errorMessage(e: unknown): string {
  const msg = toError(e).message
  return msg.replace(/^Error:\s*/, '')
}

// ---- normalisation (Go nil slices arrive as null) ----

function normSummary<T extends MessageSummary>(m: T): T {
  const legacy = (m as unknown as { category?: string }).category
  m.folderIds ??= []
  m.to ??= []
  m.labels ??= legacy ? [legacy] : []
  m.labelDetails ??= []
  m.from ??= { name: '', email: '' }
  return m
}

function normMessage(m: Message): Message {
  normSummary(m)
  m.cc ??= []
  m.bcc ??= []
  m.replyTo ??= []
  m.attachments ??= []
  m.bodyHtml ??= ''
  m.bodyText ??= ''
  return m
}

function normSettings(s: Settings): Settings {
  const legacy = (s as unknown as { categories?: Settings['labels'] }).categories
  return {
    ...s,
    theme: s.theme || 'system',
    density: s.density || 'comfortable',
    syncIntervalMinutes: s.syncIntervalMinutes || 5,
    labels: s.labels ?? legacy ?? [],
    primaryColor: s.primaryColor || DEFAULT_PRIMARY,
    radius: typeof s.radius === 'number' && s.radius > 0 ? s.radius : s.radius === 0 ? 0 : DEFAULT_RADIUS,
    classifierModel: s.classifierModel || 'typesafe/jev-1.13',
    classifyThreshold: typeof s.classifyThreshold === 'number' && s.classifyThreshold > 0 ? s.classifyThreshold : 0.5,
    classifyRemoveThreshold: typeof s.classifyRemoveThreshold === 'number' ? s.classifyRemoveThreshold : 0.15,
    aiCorrectsProviderLabels: s.aiCorrectsProviderLabels ?? true,
    importProviderLabels: s.importProviderLabels ?? true,
    grammarSystemPrompt: s.grammarSystemPrompt ?? '',
    classifyInstructions: s.classifyInstructions ?? '',
    microsoftTenant: s.microsoftTenant || 'common',
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
    ...(q.includeTrashDrafts ? { includeTrashDrafts: true } : {}),
  }
}

export const api = {
  // ---- accounts
  getAccounts: async () => (await call<Account[] | null>('GetAccounts')) ?? [],
  addAccount: (provider: string) => call<Account>('AddAccount', provider),
  cancelAddAccount: () => call<void>('CancelAddAccount'),
  removeAccount: (id: string) => call<void>('RemoveAccount', id),
  updateAccount: (id: string, displayName: string, color: string) => call<void>('UpdateAccount', id, displayName, color),

  // ---- folders & messages
  getFolders: async (accountId: string) => (await call<Folder[] | null>('GetFolders', accountId)) ?? [],
  listMessages: async (q: Partial<ListQuery>): Promise<MessagePage> => {
    const page = await call<MessagePage>('ListMessages', normQuery(q))
    return { messages: (page.messages ?? []).map(normSummary), nextCursor: page.nextCursor ?? '', total: page.total ?? 0 }
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
    if (isWails()) {
      if (boundFn('OpenExternalURL')) return call<void>('OpenExternalURL', url)
      return Browser.OpenURL(url)
    }
    return call<void>('OpenExternalURL', url)
  },

  // ---- search & bulk queries
  searchMessages: async (q: SearchQuery): Promise<SearchResult[]> =>
    ((await call<SearchResult[] | null>('SearchMessages', q)) ?? []).map((r) => ({ ...r, message: normSummary(r.message) })),
  resolveQueryIds: async (q: Partial<ListQuery>, max: number) => (await call<string[] | null>('ResolveQueryIDs', normQuery(q), max)) ?? [],

  // ---- settings
  getSettings: async () => normSettings(await call<Settings>('GetSettings')),
  saveSettings: (s: Settings) => call<void>('SaveSettings', s),
  setOpenRouterKey: (key: string) => call<void>('SetOpenRouterKey', key),
  getAppInfo: () => call<AppInfo>('GetAppInfo'),
  getDefaultPrompts: async (): Promise<DefaultPrompts> => {
    if (hasMethod('GetDefaultPrompts')) return call<DefaultPrompts>('GetDefaultPrompts')
    const grammar = hasMethod('GetDefaultGrammarPrompt') ? await call<string>('GetDefaultGrammarPrompt') : ''
    return { grammar, classify: '' }
  },

  // ---- labels & AI
  testOpenRouter: () => call<void>('TestOpenRouter'),
  listAIModels: async () => (await call<AIModel[] | null>('ListAIModels')) ?? [],
  classifyMessages: async (ids: string[]): Promise<ClassificationResult[]> =>
    ((await call<ClassificationResult[] | null>('ClassifyMessages', ids)) ?? []).map((r) => {
      // Tolerate the older {label, confidence} shape.
      const legacy = r as unknown as { label?: string; confidence?: number }
      const added = r.added ?? (legacy.label ? [legacy.label] : [])
      const scores = r.scores ?? (legacy.label ? { [legacy.label]: legacy.confidence ?? 0 } : {})
      return { messageId: r.messageId, added, removed: r.removed ?? [], scores, cost: r.cost ?? 0 }
    }),
  addLabel: (ids: string[], labelId: string) => call<void>('AddLabel', ids, labelId),
  removeLabel: (ids: string[], labelId: string) => call<void>('RemoveLabel', ids, labelId),
  getLabelCounts: async (accountId: string) => (await call<LabelCount[] | null>('GetLabelCounts', accountId)) ?? [],
  estimateClassify: (q: Partial<ListQuery>, onlyUnlabeled: boolean) => call<ClassifyEstimate>('EstimateClassify', normQuery(q), onlyUnlabeled),
  classifyQuery: (q: Partial<ListQuery>, onlyUnlabeled: boolean) => call<string>('ClassifyQuery', normQuery(q), onlyUnlabeled),
  cancelClassify: (jobId: string) => call<void>('CancelClassify', jobId),
  summarizeMessage: (id: string) => call<string>('SummarizeMessage', id),
  improveGrammar: (text: string) => call<string>('ImproveGrammar', text),

  // ---- runtime
  on<E extends EventName>(event: E, cb: (payload: EventPayloads[E]) => void): () => void {
    if (isWails()) return Events.On(event, (ev: { data: unknown }) => cb(ev.data as EventPayloads[E]))
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
    if (isWails()) return Clipboard.Text()
    try {
      return await navigator.clipboard.readText()
    } catch {
      return ''
    }
  },

  async clipboardSet(text: string): Promise<void> {
    if (isWails()) {
      await Clipboard.SetText(text)
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
