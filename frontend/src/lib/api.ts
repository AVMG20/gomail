// The ONLY module that talks to the Wails bindings. Everything else in the UI
// imports `api` from here. When the page is not running inside Wails (plain
// browser via `npm run dev`) every call is routed to the in-memory mock.
import * as Bindings from '../../wailsjs/go/main/App'
import * as Runtime from '../../wailsjs/runtime/runtime'
import type {
  Account, AIModel, AppInfo, ClassificationResult, ClassifyEstimate, ComposeRequest, DefaultPrompts, EventName,
  EventPayloads, Folder, LabelCount, ListQuery, Message, MessagePage, MessageSummary, SearchQuery, SearchResult, Settings,
} from './types'
import { DEFAULT_PRIMARY, DEFAULT_RADIUS } from './theme'

type AnyFn = (...args: unknown[]) => Promise<unknown>

interface WailsWindow {
  go?: { main?: { App?: Record<string, AnyFn> } }
  runtime?: unknown
}

let mode: 'wails' | 'mock' | null = null
let mockPromise: Promise<typeof import('./mock/backend')> | null = null

export function isWails(): boolean {
  if (mode === null) mode = (window as unknown as WailsWindow).go?.main?.App ? 'wails' : 'mock'
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
  const generated = (Bindings as unknown as Record<string, AnyFn | undefined>)[name]
  if (generated) return generated
  // Methods added after the bindings were generated are still reachable at runtime.
  const live = (window as unknown as WailsWindow).go?.main?.App?.[name]
  return live ? (...args: unknown[]) => live(...args) : undefined
}

export function hasMethod(name: string): boolean {
  return isWails() ? !!boundFn(name) : true
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
    ...(q.includeDraftsSpamTrash ? { includeDraftsSpamTrash: true } : {}),
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
      return Runtime.BrowserOpenURL(url)
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
  classifyMessages: async (ids: string[]) =>
    ((await call<ClassificationResult[] | null>('ClassifyMessages', ids)) ?? []).map((r) => ({ ...r, label: r.label ?? r.category ?? '' })),
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
    if (isWails()) return Runtime.EventsOn(event, (p: EventPayloads[E]) => cb(p))
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
    if (isWails()) return Runtime.ClipboardGetText()
    try {
      return await navigator.clipboard.readText()
    } catch {
      return ''
    }
  },

  async clipboardSet(text: string): Promise<void> {
    if (isWails()) {
      await Runtime.ClipboardSetText(text)
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
