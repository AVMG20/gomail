// Frontend mirror of internal/models/models.go. These are plain interfaces
// (not the generated wailsjs classes) so the UI is decoupled from binding
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
  /** Bulk AI labeling only: also include Drafts, Spam/Junk and Trash. */
  includeDraftsSpamTrash?: boolean
}

export interface MessagePage {
  messages: MessageSummary[]
  nextCursor: string
  total: number
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

export interface Label {
  id: string
  name: string
  description: string
  color: string
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
  classifyInstructions: string
  autoClassify: boolean
  labels: Label[]
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
}

export interface ClassificationResult {
  messageId: string
  /** Assigned label id ("" or "none" when nothing matched). Older backends call it `category`. */
  label?: string
  category?: string
  labels?: string[]
  confidence: number
  reason: string
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
  estimatedCost: number
  model?: string
}

export interface ClassifyJobProgress {
  jobId: string
  done: number
  total: number
  labeled: number
  skipped: number
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
} as const

export interface EventPayloads {
  'sync:started': { accountId: string }
  'sync:finished': { accountId: string; newMessages: number; error: string }
  'messages:changed': { accountId: string }
  'accounts:changed': undefined
  'classify:progress': Partial<ClassifyJobProgress> & { done: number; total: number }
  'classify:finished': Partial<ClassifyJobProgress>
  'mail:new': { accountId: string; count: number; latest: MessageSummary }
}

export type EventName = keyof EventPayloads
