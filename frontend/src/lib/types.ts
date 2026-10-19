// Frontend mirror of internal/models/models.go. These are plain interfaces
// (not the generated v3 binding classes) so the UI is decoupled from binding
// regeneration; src/lib/api.ts is the only bridge to the generated code.

export type Provider = 'gmail' | 'outlook'

export type FolderKind =
  | 'inbox'
  | 'starred'
  | 'sent'
  | 'drafts'
  | 'archive'
  | 'spam'
  | 'trash'
  | 'custom'

export interface Address {
  name: string
  email: string
}

export interface Account {
  id: string
  provider: Provider | string
  email: string
  displayName: string
  color: string
  createdAt: string
  lastSyncAt: string | null
  syncError: string
  syncing: boolean
  unreadCount: number
  /** Sign-in expired/revoked: show a Reconnect banner (backend pass 2). */
  needsReauth?: boolean
  /** Per-account HTML signature (backend pass 3). */
  signature?: string
}

export interface Contact {
  name: string
  email: string
  count: number
}

export interface Folder {
  id: string
  accountId: string
  remoteId: string
  name: string
  kind: FolderKind | string
  unread: number
  total: number
}

export interface MessageSummary {
  id: string
  accountId: string
  remoteId: string
  threadId: string
  folderIds: string[]
  from: Address
  to: Address[]
  subject: string
  snippet: string
  date: string
  unread: boolean
  starred: boolean
  hasAttachments: boolean
  /** Label ids (multi-label). */
  labels: string[]
  /** Who assigned each label (manual vs AI) and with what confidence. */
  labelDetails?: MessageLabel[]
  /** Messages in the conversation (conversation view, backend pass 2). */
  threadCount?: number
}

export interface UnsubscribeInfo {
  url: string
  mailto: string
  oneClick: boolean
}

export interface AttachmentData {
  filename?: string
  mimeType: string
  size?: number
  dataBase64: string
}

export interface SyncProgress {
  accountId: string
  phase: string
  done: number
  total: number
}

export interface MessageLabel {
  labelId: string
  source: 'ai' | 'manual' | string
  confidence: number
}

export interface Attachment {
  id: string
  filename: string
  mimeType: string
  size: number
  inline: boolean
  contentId: string
}

export interface Message extends MessageSummary {
  cc: Address[]
  bcc: Address[]
  replyTo: Address[]
  bodyHtml: string
  bodyText: string
  attachments: Attachment[]
  messageId: string
  inReplyTo: string
  references: string
  hasRemoteContent?: boolean
  unsubscribe?: UnsubscribeInfo | null
}

export interface ListQuery {
  accountId: string
  folderId: string
  folderKind: string
  /** Match mail having ANY of these labels. */
  labels: string[]
  /** Match mail without any label (combined with `labels` as OR). */
  unlabeled: boolean
  search: string
  unreadOnly: boolean
  limit: number
  cursor: string
}

export interface MessagePage {
  messages: MessageSummary[]
  nextCursor: string
  total: number
  /** More (older) mail exists on the server beyond the local cache: call LoadOlder. */
  hasMoreRemote?: boolean
}

export interface OutgoingAttachment {
  filename: string
  mimeType: string
  dataBase64: string
}

export type ComposeMode = 'new' | 'reply' | 'replyAll' | 'forward'

export interface ComposeRequest {
  accountId: string
  to: Address[]
  cc: Address[]
  bcc: Address[]
  subject: string
  bodyHtml: string
  bodyText: string
  replyToId: string
  mode: ComposeMode
  attachments: OutgoingAttachment[]
}

export type LabelSource = 'user' | 'provider' | 'system'

export interface Label {
  id: string
  name: string
  description: string
  color: string
  /** "user" labels are fully editable; "provider" ones are imported from Gmail/Outlook, "system" is built-in (Junk). */
  source?: LabelSource | string
  /** Optional hint (mock only today): provider the label was imported from. */
  provider?: string
}

export type ThemeMode = 'system' | 'light' | 'dark'
export type Density = 'comfortable' | 'compact'

export interface Settings {
  theme: ThemeMode | string
  density: Density | string
  syncIntervalMinutes: number
  showRemoteImages: boolean
  googleClientId: string
  googleClientSecret: string
  microsoftClientId: string
  microsoftTenant: string
  openRouterKeySet: boolean
  /** Chat model: used only for grammar improvement and summaries. */
  openRouterModel: string
  /** Jev decision model used for AI labeling. */
  classifierModel: string
  /** 0..1; labels below this confidence are left unassigned. */
  classifyThreshold: number
  /** 0..1; imported labels scoring below this are removed (when aiCorrectsProviderLabels). */
  classifyRemoveThreshold: number
  aiCorrectsProviderLabels: boolean
  classifyInstructions: string
  autoClassify: boolean
  labels: Label[]
  /** Import Gmail labels / Outlook categories as labels. */
  importProviderLabels: boolean
  /** Native desktop notifications for new mail (backend shows them). */
  notifications: boolean
  /** Keep a system tray icon (requested from backend; stored locally until supported). */
  showInTray?: boolean
  /** Keep running in the tray when the window is closed. */
  closeToTray?: boolean
  /** Group messages into conversations (default true). */
  conversationView?: boolean
  /** When opening a message marks it read. Frontend preference (stored locally). */
  markReadMode?: 'immediate' | 'delay' | 'manual'
  /** "right" = three panes; "off" = full-width list, message opens in the main area. */
  readingPane?: 'right' | 'off'
  /** UI zoom factor 0.9–1.25. */
  zoom?: number
  /** Delay before a message is actually sent (0 = off). Requested; stored locally until supported. */
  undoSendSeconds?: number
  primaryColor: string
  /** rem */
  radius: number
  grammarSystemPrompt: string
}

export interface AIModel {
  id: string
  name: string
  description: string
  contextLength: number
  promptPrice: number
  completionPrice: number
  priceUnknown?: boolean
  /** "chat" | "decision" (Jev / Decisions API) */
  kind?: string
}

export interface ClassificationResult {
  messageId: string
  added: string[]
  removed: string[]
  /** AI score per label id (0..1). */
  scores: Record<string, number>
  cost: number
  error?: string
}

export interface LabelCount {
  labelId: string
  total: number
  unread: number
}

export interface SearchQuery {
  query: string
  accountId: string
  labels: string[]
  limit: number
}

export interface SearchResult {
  message: MessageSummary
  /** Snippet with <mark> highlights; everything else is pre-escaped. */
  highlight: string
  matchedIn: string
  score: number
  fuzzy?: boolean
}

export interface ClassifyEstimate {
  count: number
  requests?: number
  estimatedTokens?: number
  estimatedCostUsd: number
  /** false when OpenRouter lists no price for the classifier model */
  priceKnown: boolean
}

export interface ClassifyJob {
  jobId: string
  total: number
}

export interface ClassifyJobProgress {
  jobId: string
  done: number
  total: number
  labeled: number
  skipped: number
  added?: number
  removed?: number
  failed?: number
  cost: number
  finished?: boolean
  error?: string
  cancelled?: boolean
}

export interface AppInfo {
  version: string
  dataDir: string
  logFile: string
  platform: string
  demo: boolean
}

export interface DefaultPrompts {
  grammar: string
  classify: string
}

// ---- events ----

export const Events = {
  SyncStarted: 'sync:started',
  SyncFinished: 'sync:finished',
  MessagesChanged: 'messages:changed',
  AccountsChanged: 'accounts:changed',
  ClassifyProgress: 'classify:progress',
  NewMail: 'mail:new',
  ClassifyFinished: 'classify:finished',
  LabelsChanged: 'labels:changed',
  SyncProgress: 'sync:progress',
} as const

export interface EventPayloads {
  'sync:started': { accountId: string }
  'sync:finished': { accountId: string; newMessages: number; error: string }
  'messages:changed': { accountId: string }
  'accounts:changed': { accountId?: string } | undefined
  'labels:changed': { accountId?: string } | undefined
  'sync:progress': SyncProgress
  'classify:progress': Partial<ClassifyJobProgress> & { done: number; total: number }
  'classify:finished': Partial<ClassifyJobProgress>
  'mail:new': { accountId: string; count: number; latest: MessageSummary }
}

export type EventName = keyof EventPayloads
