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
  /** OAuth grant includes calendar scopes; false = show "Connect calendar" (EnableCalendar). */
  calendarConsent?: boolean
  /** The mailbox's calendar is synced and shown (default true). Disabled = no events, no sync, no reminders. */
  calendarEnabled?: boolean
  /** Event reminders muted for this mailbox (events stay visible). */
  calendarMuted?: boolean
}

// ---- calendar ----

export interface Calendar {
  id: string
  accountId: string
  name: string
  color: string
  primary: boolean
  canEdit: boolean
  visible: boolean
  /** Video meetings this calendar can create: "meet" (Google), "teams" (Microsoft) or "" (none). */
  videoCallProvider?: 'meet' | 'teams' | 'skype' | '' | string
}

export type AttendeeResponse = 'accepted' | 'tentative' | 'declined' | 'needsAction' | string
export type RecurrenceScope = 'this' | 'series'

export interface Attendee {
  name: string
  email: string
  response: AttendeeResponse
}

export interface CalendarEvent {
  id: string
  calendarId: string
  accountId: string
  title: string
  /** RFC 3339 for timed events; "YYYY-MM-DD" for all-day events (end exclusive). */
  start: string
  end: string
  allDay: boolean
  /** IANA zone the event was created in ("" = floating/local). */
  timeZone: string
  location: string
  description: string
  attendees: Attendee[]
  organizer: Address
  isOrganizer: boolean
  /** Your own response when you're an attendee ("" when you're the organizer). */
  myResponse: AttendeeResponse
  meetingUrl: string
  isRecurring: boolean
  /** Id of the series master for occurrences of a recurring event. */
  seriesId: string
  /** RRULE without the "RRULE:" prefix, e.g. "FREQ=WEEKLY;UNTIL=20261231". */
  recurrence: string
  status: 'confirmed' | 'tentative' | 'cancelled' | string
  /** Minutes before the start. */
  reminders: number[]
  /** Human summary of the recurrence ("Weekly on Monday"), from the backend. */
  recurrenceText?: string
  /** Backend: you can edit this event (organizer on an editable calendar). */
  canEdit?: boolean
  updatedAt?: string
}

export interface EventInput {
  /** Mailbox of the calendar (the backend needs it; filled from the calendar when omitted). */
  accountId?: string
  calendarId: string
  title: string
  start: string
  end: string
  allDay: boolean
  timeZone: string
  location: string
  description: string
  attendees: Attendee[]
  meetingUrl: string
  /** Ask the provider to attach a Google Meet / Teams meeting (Calendar.videoCallProvider). */
  addVideoCall?: boolean
  recurrence: string
  reminders: number[]
}

/** A meeting invitation parsed from the mail's text/calendar part. */
export interface EventInvite {
  /** Matching calendar event, when it is already on a synced calendar. */
  eventId?: string
  uid: string
  title: string
  start: string
  end: string
  allDay: boolean
  location: string
  organizer: Address
  /** iTIP method: REQUEST, CANCEL, REPLY… */
  method: string
  /** Not sent by the backend yet; the card starts without a selection. */
  myResponse?: AttendeeResponse
}

export interface Contact {
  name: string
  email: string
  count: number
  lastSeen?: string
}

export interface AIUsage {
  todayUsd: number
  monthUsd: number
  requestsToday: number
  requestsMonth: number
  /** 0 = unlimited */
  dailyBudgetUsd?: number
  /** Automatic labeling is paused until tomorrow. */
  budgetReached?: boolean
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
  /** "c:<id>" in conversation view: pass instead of message ids to act on the whole thread. */
  conversationId?: string
  /** Sender names, oldest first; "me" for the account itself. */
  participants?: string[] | null
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
  /** Remote images/CSS were stripped (offer "Load images"). */
  remoteContentBlocked?: boolean
  unsubscribe?: UnsubscribeInfo | null
  isDraft?: boolean
  /** Meeting invitation (ICS) carried by this mail. */
  invite?: EventInvite | null
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
  /** Draft handle from SaveDraft; the backend removes the draft once sent. */
  draftId?: string
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
  /** Dark mode: "auto" renders simple emails with theme colors; "light" always uses a light card. */
  emailBackground?: 'auto' | 'light'
  /** UI zoom factor 0.9–1.25. */
  zoom?: number
  /** Daily AI spend cap in USD (0 = unlimited). Next backend pass. */
  aiDailyBudgetUsd?: number
  /** How far back to sync (days; 0 = everything). Next backend pass. */
  syncDepthDays?: number
  /** Pause the background download of older mail. */
  backfillPaused?: boolean
  /** Start gomail when you log in (minimized to the tray). */
  launchAtLogin?: boolean
  /** Check GitHub releases for updates once a day. */
  checkUpdates?: boolean
  /** Delay before a message is actually sent (0 = off). Requested; stored locally until supported. */
  undoSendSeconds?: number
  /** Calendar: first day of the week ("auto" follows the locale). */
  calendarWeekStart?: 'auto' | 'sunday' | 'monday' | 'saturday' | string
  calendarDefaultView?: 'week' | 'day' | 'month' | 'agenda'
  /** Minutes. */
  calendarDefaultDuration?: number
  calendarShowDeclined?: boolean
  /** Minutes before the start, used when an event has no reminders of its own. */
  calendarDefaultReminder?: number
  /** Desktop reminders for upcoming events. */
  calendarNotifications?: boolean
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
  /** Unchanged since the last AI run: stored scores, no cost. */
  skipped?: boolean
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
  /** Messages that would be skipped (already labeled with the current labels). */
  unchanged?: number
  /** Spent today (USD). */
  todayUsd?: number
  /** Today's spend + estimate is over the daily budget (allowed, but warn). */
  exceedsBudget?: boolean
}

export interface ClassifyJob {
  jobId: string
  total: number
}

/** Payload of classify:progress / classify:finished (models.ClassifyProgress). */
export interface ClassifyJobProgress {
  jobId: string
  done: number
  total: number
  /** messages whose labels changed */
  labeled: number
  /** unchanged since the last AI run (no cost) */
  skipped: number
  failed?: number
  /** USD so far */
  cost: number
  error?: string
  cancelled?: boolean
}

/** UI state of the running/last job: the payload plus "finished" (set on classify:finished). */
export interface ClassifyJobState extends ClassifyJobProgress {
  finished?: boolean
}

export interface AppInfo {
  version: string
  dataDir: string
  logFile: string
  platform: string
  /** runtime.GOOS / GOARCH (backend pass 3) */
  os?: string
  arch?: string
  demo: boolean
}

export interface UpdateInfo {
  current: string
  latest: string
  url: string
  notes: string
  available: boolean
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
  NetworkStatus: 'network:status',
  SearchUpdated: 'search:updated',
  NotificationOpen: 'notification:open',
  AiBudget: 'ai:budget',
  DatabaseStatus: 'database:status',
  CalendarChanged: 'calendar:changed',
  CalendarOpen: 'calendar:open',
  ComposeOpen: 'compose:open',
  UpdateAvailable: 'update:available',
} as const

export interface EventPayloads {
  'sync:started': { accountId: string }
  'sync:finished': { accountId: string; newMessages: number; error: string }
  'messages:changed': { accountId: string }
  'accounts:changed': { accountId?: string } | undefined
  'labels:changed': { accountId?: string } | undefined
  'sync:progress': SyncProgress
  'network:status': { online: boolean }
  'search:updated': { query: string; accountId: string; added: number }
  /** A desktop notification was clicked: a new mail (messageId) or an event reminder (eventId). */
  'notification:open': { messageId: string; accountId: string; eventId?: string }
  /** Auto-labeling paused (daily AI budget reached) or resumed. */
  'ai:budget': { paused: boolean; todayUsd?: number; budgetUsd?: number }
  /** Database health: integrity problems / repair. */
  'database:status': { ok: boolean; message: string }
  /** Calendars or events changed (sync, edits elsewhere). */
  'calendar:changed': { accountId?: string } | undefined
  /** A reminder notification was clicked: show this event. */
  'calendar:open': { eventId: string }
  /** mailto: handler: open compose prefilled (next backend pass). */
  'compose:open': { to?: string | Address[] | null; cc?: string | Address[] | null; bcc?: string | Address[] | null; subject?: string; body?: string }
  /** A newer release exists (next backend pass). */
  'update:available': UpdateInfo
  'classify:progress': Partial<ClassifyJobProgress> & { done: number; total: number }
  'classify:finished': Partial<ClassifyJobProgress>
  'mail:new': { accountId: string; count: number; latest: MessageSummary }
}

export type EventName = keyof EventPayloads
