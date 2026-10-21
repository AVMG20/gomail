# gomail backend

Go backend of the gomail desktop client (Wails v3). The frontend calls the
`MailService` methods in `app.go` through the generated bindings in
`frontend/bindings/github.com/AVMG20/gomail` and listens to typed events
(`frontend/bindings/events`).

## Architecture

```
app.go / main.go     Wails service (thin facade), window, tray, typed events
internal/core        use cases: accounts, messages, compose, labels, AI jobs
internal/syncer      per-account background sync + optimistic actions
internal/provider    Provider interface
  ├─ gmail           Gmail REST v1 client (history.list incremental sync)
  ├─ outlook         Microsoft Graph v1.0 client (delta queries, $batch)
  ├─ gcal            Google Calendar v3 client (singleEvents, syncToken)
  └─ graphcal        Graph calendar client (calendarView delta, UTC times)
internal/calendar    calendar types, Windows→IANA zones, RRULE, ICS invites
internal/demo        in-memory Gmail-like and Outlook-like accounts
internal/store       SQLite (modernc.org/sqlite, pure Go): cache, FTS5 search
internal/mailmime    MIME build/parse, charsets, HTML sanitizing, cid: images
internal/auth        OAuth 2.0 loopback + PKCE, refreshing token sources
internal/secrets     OS keyring with AES-GCM encrypted-file fallback
internal/ai          OpenRouter: model list, chat, Decisions API (Jev)
internal/httpx       REST helper: retries/backoff, Retry-After, 401 refresh
internal/config      data dir, logging, default settings and validation
internal/models      types shared with the frontend (JSON = contract)
```

**Sync.** Each account syncs at start-up and every `syncIntervalMinutes`
(default 5), one sync at a time per account. The first sync fetches recent
mail per folder (inbox 300, other system folders 100, custom folders 50).
After that Gmail uses `history.list` from the stored `historyId` and Graph
uses per-folder delta links. Expired state falls back to a full resync.
Events: `sync:started`, `sync:finished`, `sync:progress` (first sync only:
phases `folders`, `listing`, `fetching`, `saving`), `messages:changed`,
`accounts:changed`, `labels:changed`, `mail:new` (plus a desktop
notification), `classify:progress`, `classify:finished`, `network:status`
and `search:updated`.

**Reliability.**

- A revoked or expired refresh token (`invalid_grant`, or a 401 after a
  token refresh) sets `Account.needsReauth` and a readable `syncError`.
  The account is then skipped until `ReauthAccount(id)` runs OAuth again.
  That call requires the same mailbox and keeps all local data.
- Repeated failures back off exponentially: 30 s, doubling, capped at 30
  minutes. `SyncNow` always tries immediately.
- Network errors trigger a connectivity probe (TCP to the provider hosts).
  When offline, the engine emits `network:status {online:false}`, doesn't
  record an account error, and probes every 30 s. When the network comes
  back it emits `online:true` and syncs everything.

**Provider edge cases.**

- Gmail `history.list` 404 (history too old) falls back to a full resync.
  Upserts never touch manual labels or AI overrides.
- Graph delta 410 or `syncStateNotFound` resets that folder's delta link.
- Every Graph call sends `Prefer: IdType="ImmutableId"`, including each
  `$batch` sub-request, so message ids survive moves. No id migration is
  needed: gomail has always stored immutable ids.
- Retries with exponential backoff apply to 429 and 5xx (honouring
  `Retry-After`, also on batched sub-requests) and to Google quota 403s
  (`rateLimitExceeded`, `userRateLimitExceeded`).
- Large mail:
  - Gmail messages and drafts over 4 MB go through the media upload
    endpoint (`/upload/.../messages/send` and `/drafts` with
    `uploadType=multipart`).
  - Graph attachments over 3 MB use `createUploadSession` with 3.75 MB
    chunks; the upload URL gets no bearer token.
  - Both paths are tested with 10 MB attachments.

**Background backfill.** After the first sync, older mail is fetched page
by page (100 messages, one page every 3 s, starting 30 s after launch). It
continues until `syncDepthDays` is covered (30, 90 (the default), 365, or
0 for all mail) or the server has nothing older.

- Gmail backfills All Mail; Outlook backfills Inbox, Archive, Sent and
  custom folders.
- Each page is its own short transaction.
- `backfillPaused` pauses it. Progress survives restarts, because it
  derives from the oldest cached message per folder.
- It emits `sync:progress` with phase `backfill` (days covered out of
  `syncDepthDays`), and `messages:changed` at most every 10 s.

**Older mail.** `ListMessages` sets `hasMoreRemote` on the last local page
when the server folder holds more mail than is cached. `LoadOlder(query)`
then fetches the next 50 messages per folder in the view and emits
`messages:changed`. Gmail uses `before:<epoch>` and Graph uses
`receivedDateTime lt`. Once a folder has nothing older, it is marked done.

**Server search.** When `SearchMessages` (or `ListMessages` with `search`)
finds fewer than 5 local results, a background server search runs:

- Gmail uses `q=`. Graph uses `$search` with KQL (from:, to:, subject:,
  hasattachments:, received). Unread and starred are filtered locally.
- It is throttled per account and query (2 min), with at most one request in
  flight per account.
- Results are cached locally. If anything new arrived, `search:updated
  {query, accountId, added}` tells the UI to search again.

**Conversations.** When `conversationView` is on (the default),
`ListMessages` returns one row per thread: Gmail `threadId` or Graph
`conversationId`. Each row works like this:

- It shows the thread's newest message that matches the query.
- `unread`, `starred`, `hasAttachments` and `labels`/`labelDetails` are
  aggregated over the thread. Trashed messages only group with other
  trashed messages.
- `threadCount` and `participants` describe the whole thread. The account
  itself shows as "me".
- `total` counts conversations. Keyset pagination uses the row's message
  (date, id), so it stays exact.

Action methods accept `conversationId` (`"c:<message id>"`) in place of
message ids:

- `MarkRead` and `Add`/`RemoveLabel` apply to every message in the thread.
- `Archive`, `Trash` and `MoveToFolder` apply to the thread's messages that
  share a folder with the row. Gmail's All Mail is ignored for this, so
  your replies in Sent stay where they are.
- `SetStarred(true)` stars only the newest message; `SetStarred(false)`
  unstars all of them.
- `ClassifyMessages` labels the newest message.

`GetMessage` and `GetThread` accept either kind of id.

**Drafts.** `SaveDraft(req, draftId)` creates a provider draft (Gmail
`drafts`, Graph draft messages) and returns a handle
`draft:<account>:<id>`. You can also pass the id of a message in Drafts to
keep editing it.

- Saves of the same draft within 3 s are coalesced: the latest content is
  written when the window closes.
- A failed autosave is kept and retried (5 s, doubling up to 5 min, and
  right away when the network returns); newer content replaces it. A save
  the server keeps refusing is dropped after 10 attempts.
- `SendMessage` with `draftId` sends that draft. `DeleteDraft` discards it.
- Replies and forwards keep threading. Forwards include the original's
  attachments: Graph's `createForward` adds them, and Gmail and the demo
  provider re-attach them.

**Attachments.** `GetAttachmentData` returns at most 15 MB, base64-encoded,
for previews. Attachment bytes are cached in `<data dir>/attachments`: the
cache is LRU, bounded at 500 MB, and cleared per account when the account is
removed.

**AI cost control.**

- Every Decisions and chat call records `usage.cost` in `ai_usage`. Chat
  requests ask OpenRouter for usage reporting.
- `GetAIUsage()` returns today's and this month's spend and request
  counts, plus `aiDailyBudgetUsd` (default $1, 0 = unlimited).
- When the budget is reached, automatic labeling pauses and emits
  `ai:budget {paused:true, todayUsd, budgetUsd}` once a day. It resumes
  the next day (or after the budget is raised) and emits `paused:false`.
- Manual and bulk labeling still run. `EstimateClassify` reports
  `todayUsd`, `exceedsBudget` and `unchanged` so the UI can warn first.
- A message is not sent to the classifier again while its fingerprint is
  unchanged. The fingerprint combines the model, the instructions, the
  label definitions and the message identity. Unchanged messages return
  `skipped: true` with their stored scores, at no cost.

**Unsubscribe.** `List-Unsubscribe` and `List-Unsubscribe-Post` are parsed
into `Message.unsubscribe`. `Unsubscribe(id)` tries these in order:

- an RFC 8058 one-click POST, https only, with no cookies and https-only
  redirects;
- an email sent from the account to the mailto address;
- opening the link in the browser.

**Contacts.** Every address in from, to and cc of newly synced mail goes
into a `contacts` table. Addresses you sent to count 5×.
`SearchContacts(q, limit)` prefix-matches the email or any word of the
name, and ranks by count × recency. noreply addresses are ranked down and
your own addresses are excluded.

**Folder operations.** `MarkFolderRead(folderId)` and
`EmptyFolder(folderId)` (Trash or Spam only) are optimistic and revert on
failure.

- Graph permanently deletes via `$batch` `permanentDelete`.
- Gmail moves Spam to Trash. It can't empty Trash: permanent deletion
  needs the full `https://mail.google.com/` scope, which gomail doesn't
  request, so it returns an explanation instead. Gmail purges Trash itself
  after 30 days.

**Mailbox order, mute and hidden folders.** `ReorderAccounts(ids)` sets
the order of the mailboxes (`accounts.position`); `GetAccounts` returns
them in that order and a new mailbox goes last. A muted mailbox
(`mutedMailboxes`) never notifies, and the UI hides its unread badge and
leaves it out of the All inboxes and tray counts. `hiddenFolders` (folder
ids) are left out of the sidebar's other folders; their mail still shows
in search.

**Notifications.** New mail is announced after the label rules ran and,
in mailboxes where Jev labels new mail, after Jev labeled and moved it (at
most 20 s later), so mail that went to Junk or got a quiet label doesn't
notify. A single message shows the sender and subject.
Several show "5 new messages · <account>" with the senders. Clicking one
shows and focuses the window and emits `notification:open {messageId,
accountId}`.

**Robustness.**

- Every goroutine recovers and logs panics.
- The log is size-rotated at 5 MB, keeping 3 old files.
- Shutdown waits up to 5 s for in-flight write transactions.
- 15 s after start, `PRAGMA quick_check` runs. If it fails:
  - a marker is written and `database:status {ok:false}` is emitted;
  - on the next launch the database is rebuilt: the damaged file is kept
    as `gomail.db.corrupt-<time>`, accounts, signatures and settings are
    salvaged, and mail syncs again (tokens live in the keyring).
- A database that fails to open as corrupt is rebuilt the same way, right
  away.
- Every 6 h, `PRAGMA optimize` runs. New databases use
  `auto_vacuum=INCREMENTAL`; old ones are vacuumed once when more than a
  quarter of the file is free.

**Startup.** Opening the database (migrations, settings) takes about 3 ms
with 50k messages. Syncing starts only after the UI's first `ListMessages`
(or after 3 s). The integrity check runs at 15 s, backfill at 30 s, and
the model list is only fetched when it is needed. From a cold start, the
first-paint calls (accounts, folders, label counts and the first
conversation page) take about 25 ms with 50k messages.

**Tray.** The tray icon shows the unread count (macOS label, tooltip
elsewhere). Its menu has Open gomail, Check mail and Quit.

- `showInTray` (default true) shows or hides it.
- With `closeToTray` (default false), closing the window hides it instead.
  On Linux this only applies when a StatusNotifier host exists (checked
  over D-Bus); otherwise the window closes normally.
- Missing D-Bus or notification daemons are logged and ignored.

**Desktop integration.**

- *Single instance.* One gomail runs per data directory (id
  `com.avmg20.gomail`, plus a hash of the directory for custom profiles).
  A second launch focuses the running window, forwards its arguments and
  exits. On Linux this goes over the D-Bus session bus. Before enabling it,
  gomail checks that the bus is reachable and the name
  `<id>.SingleInstance` can be owned, because Wails exits with a fatal
  error otherwise. If not, gomail logs a warning and runs as a normal
  instance.
- *Data-directory lock.* Every instance also takes an exclusive lock on
  `<data dir>/instance.lock`: `flock` on macOS and Linux, `LockFileEx` on
  Windows. The OS releases it if the process dies. When the lock is held
  (two launches without working single-instance support), the new process
  opens no window, tray or database. It logs the reason, prints
  "gomail is already running" on stderr, shows a native error dialog
  (closed after a minute if no one answers) and exits with code 1. If the
  lock file can't be created at all (a read-only or unusual file system),
  gomail logs a warning and starts without the guard.
- *Startup errors* (data directory, Wails fatal errors, `app.Run`) are
  written to stderr and to `gomail.log` before exiting. Backend init errors
  keep the window open and are returned by every call, so the UI can show
  them.
- *Launch at login.* `launchAtLogin` (default false) registers gomail with
  the OS: a HKCU `Run` value on Windows, a LaunchAgent on macOS, and an
  XDG autostart `.desktop` file on Linux. Custom profiles never register.
  The login entry passes `--minimized`, so gomail starts hidden in the tray.
  When there is no tray (`showInTray` off, or no StatusNotifier host on
  Linux), the window is shown anyway. On macOS 13+ a bundled app is
  registered with SMAppService, which can't pass arguments, so there it
  starts with its window open.
- *mailto: links.* A `mailto:` URL on the command line, forwarded from a
  second instance, or delivered by macOS as an open-URL event is parsed
  (RFC 6068: to, cc, bcc, subject, body) and emitted as
  `compose:open {to, cc, bcc, subject, body}`. Addresses are `Address`
  lists. A link that arrives before the UI subscribed is kept, and the UI
  collects it once with `GetPendingCompose()`, which returns `null` when
  there is none. Registering as the handler:
  - Linux: `MimeType=x-scheme-handler/mailto;` in `build/linux/gomail.desktop`,
    with `Exec=gomail %u`. Users pick gomail with
    `xdg-mime default gomail.desktop x-scheme-handler/mailto`.
  - macOS: `CFBundleURLTypes` with the `mailto` scheme in
    `build/darwin/Info.plist` (rank Alternate). Users pick gomail as the
    default mail app in Mail's settings.
  - Windows: not registered yet (see docs/RELEASING.md, TODO).
- *Update check.* `CheckForUpdate()` asks
  `api.github.com/repos/AVMG20/gomail/releases/latest` and returns
  `UpdateInfo {current, latest, url, notes, available}`. Versions are
  compared as semver, so a pre-release sorts before its release. A `dev`
  build never reports an update. With `checkUpdates` (default true), the
  backend checks one minute after start and then once every 24 h, and
  emits `update:available` (an `UpdateInfo`) once per new version. Nothing
  is downloaded; the UI links to `url`.

**Actions** (read, star, archive, trash, move) update SQLite first, emit
`messages:changed`, then go to the provider. Gmail uses `batchModify`
(1000 ids per call) and Graph uses JSON `$batch` (20 per call). If the
provider call fails, the local change is reverted and a readable error is
returned. While offline (a network error and the online probe fails) the
change stays and its push is queued per account, in order, until the
network is back; a queued push the server then refuses is reverted and
reported in `sync:finished`. The queue is in memory: changes made offline
are lost if gomail quits before reconnecting. Sends are never queued.

**Labels.** Labels are gomail's own concept, separate from folders. A
message can have several labels. Each assignment has a source: `manual`,
`ai`, `provider` (Gmail user labels and Outlook categories) or `system` (the
built-in **Junk** label, which every message in Spam/Junk gets). Gmail system
labels (INBOX, SPAM, TRASH, SENT, DRAFT, STARRED, IMPORTANT, CATEGORY_*) are
folders or flags, never labels.

Every mailbox has its own labels. `Settings.labels` is one list with
globally unique ids, and each label's `accountIds` holds exactly one account
id; only Junk (`source: "system"`) has none and belongs to every mailbox.
Names are unique within a mailbox (case-insensitive), so two mailboxes can
both have a "Newsletters" label, with different ids. Editing, recoloring or
deleting a label never changes another mailbox's.

- A new mailbox gets its own copies of the default labels
  (`config.MailboxDefaultLabels`; a taken id gets `-2`, `-3`, ...). Removing
  a mailbox drops its labels, rules, quiet labels and assignments.
- Gmail labels and Outlook categories are matched to the labels of their
  mailbox by name; a missing one is added to that mailbox only. Deleting an
  imported label hides it for that mailbox only
  (`dismissedMailboxLabels`: account id -> lower-cased names).
- `AddLabel`/`RemoveLabel` on mail of several mailboxes (the unified view)
  use each message's own mailbox's label with the same name; mail of a
  mailbox without one is skipped. `label:<name>` search matches the label
  of that name in every mailbox. Label counts stay per label id; the UI
  groups labels by name in the unified view and sums their counts.
- Rules (`labelRules`) and quiet labels belong to one mailbox each, and
  only use that mailbox's labels.
- `Label.then` is what happens to new mail that gets the label: `""` (it
  stays), `"archive"`, `"junk"` or `"trash"` (not used for Junk). It acts
  when a rule or Jev's automatic labeling of new mail adds the label
  (`core/labelactions.go`): Junk and Trash mark the mail read first,
  Archive only moves mail out of the inbox and keeps it unread. Jev never
  trashes: its Trash picks go to Junk. New mail Jev labels Junk always
  goes to Junk, read. Manual "Label with AI" runs don't move mail.
- Every automatic move is logged in `auto_moves` (kept 30 days).
  `AutoMoves(accountID, kind, hours)` lists the mail that is still in that
  folder kind (`spam`, `archive`, `trash`) with the reason, for the "sorted
  for you" review above those lists.

Older versions shared labels between mailboxes (`accountIds` with several
ids, or none for "every mailbox", and provider labels merged by name). On
startup `config.SplitLabels` gives each mailbox its own copy: the first
mailbox keeps the id, the others get new ids with the same name, color,
description, source and action. Each mailbox's assignments, overrides,
rejections, pending backup marks and stored AI scores move to its copy
(`store.MoveLabels`), and its "already classified" fingerprints are
renamed so the new ids don't re-run (and re-bill) classification. Rules
for every mailbox become one rule per mailbox; rules and quiet labels
follow the copies. The first run (`labelsVersion` 1) also moves the old
dismissed provider labels to every mailbox and removes AI assignments of
imported labels without a description. Running it again changes nothing.

The AI classifier (OpenRouter Decisions API, model `typesafe/jev-1.13`)
asks one `noul` question per label of the message's mailbox, in chunks of
20 labels per request. Imported labels are only asked about once they have
a description (from a name like "Categorie Rood" the model can only guess).
It works as follows:

- It adds a label (source `ai`) when P(yes) ≥ `classifyThreshold`.
- On re-classification, AI labels that score below that threshold are
  removed.
- When `aiCorrectsProviderLabels` is on, it removes provider and Junk
  assignments that score below `classifyRemoveThreshold`. It records an
  override, so the next sync doesn't add them back unless the provider
  changes the label again. These corrections stay local; the mail is not
  moved out of spam on the server.
- It never touches manual assignments.

Manually adding or removing a provider-sourced label is also pushed to
Gmail or Outlook. When `importProviderLabels` is off, provider assignments
are hidden everywhere but kept and still synced, so turning it back on is
instant. The label definitions stay in `Settings.labels` with
`source: "provider"`, and the UI hides them.

Automatic labeling (`autoClassify`) covers newly arrived inbox, custom and
spam mail. It skips drafts, sent mail and trash. On the first sync it labels
only the 30 most recent messages. Bulk `ClassifyQuery` skips Trash and Drafts
unless `includeAllFolders` is set. Label counts leave out Trash.

**Search.** The `SearchMessages` command palette and the `ListMessages`
`search` field share one parser. Search covers subject, sender, recipients,
snippet and cached body text:

- Matching uses FTS5 with prefix matching on the last word. Results are
  ranked by field weight (subject > from > to > snippet > body) times a
  recency boost.
- When few results match, the query is spell-corrected against the
  vocabulary of subjects and senders (edit distance) and run again.
  Corrected results come back with `fuzzy: true`.
- Operators: `from: to: subject: label: in:<inbox|sent|drafts|archive|spam|trash|starred>
  is:unread|read|starred has:attachment before:/after:YYYY-MM-DD` and
  `"phrases"`.
- User input is always quoted, so it never reaches FTS as syntax.

**HTML bodies** are sanitized with bluemonday:

- Kept: tables, inline styles, images, and `srcset` (http(s) candidates
  only).
- Removed: scripts, iframes, forms and event handlers.
- `@import`, `expression()`, `javascript:` and `behavior` are stripped from
  CSS.
- Links get `rel="noreferrer noopener"` and `target="_blank"`.
- `cid:` images are inlined as `data:` URIs.

`emailBackground` (`"auto"` or `"light"`, default `"auto"`) is a rendering
preference for the UI. With `"light"`, bodies are always shown on white;
with `"auto"`, they follow the theme. The backend only stores and validates
it; any other value becomes `"auto"`.

When `showRemoteImages` is off, `GetMessage` and `GetThread` also strip
remote `src`, `background`, `srcset` and CSS `url()` references. In that
case `remoteContentBlocked` is true, and `GetMessageWithRemoteContent(id)`
returns the unblocked body. Render bodies in a sandboxed `<iframe srcdoc>`
so their `<style>` rules cannot affect the app.

**Security notes.**

- Tokens and keys are never logged. Logs carry account ids, not
  addresses.
- `OpenExternalURL` only opens http(s) and mailto.
- `data:` URIs are only allowed for base64 images; links to `data:` and
  other schemes are removed. CSS escapes are decoded before `<style>`
  blocks are checked, inline styles containing escapes are dropped, and
  `image-set()` counts as remote content.
- One-click unsubscribe posts only to https URLs that resolve to public
  addresses. Loopback, private, link-local and CGNAT ranges are refused at
  dial time, so DNS rebinding doesn't get around the check. Redirects must
  stay on https.
- Outgoing header values can't contain CR or LF, so a crafted address or
  Message-ID can't inject headers. mailto: links are capped at 256 KB and
  100 recipients each.
- Update links must point into `github.com/AVMG20/gomail/releases/`;
  anything else is replaced by the releases page.
- A POST that might already have been processed (timeout, 500/502/504) is
  never repeated automatically, so mail can't be sent twice. Only
  repeat-safe POSTs (Gmail batchModify/trash, Graph `$batch`, AI calls) are
  retried.
- The OAuth loopback server listens only on loopback: 127.0.0.1, plus ::1
  for Microsoft's `localhost` redirect. It shuts down as soon as the flow
  ends or times out.

## Calendar

One unified calendar shows the events of every account. There are three
switches:
- **Calendar checkboxes** (`SetCalendarVisible`) are view filters only. They
  decide what `ListEvents` returns; hidden calendars keep syncing and still
  show reminders.
- **Disable calendar** (`SetAccountCalendarEnabled`,
  `Account.calendarEnabled`, default true). A disabled account isn't
  synced, shows no reminders, and its events are left out of `ListEvents`.
  Its calendars stay in `ListCalendars`, so the UI can show them grayed
  out. Re-enabling syncs the account right away.
- **Mute** (`SetAccountCalendarMuted`, `Account.calendarMuted`, default
  false). The account keeps syncing and showing events but shows no
  reminders.

**Access.** New sign-ins request calendar access as well: Google
`https://www.googleapis.com/auth/calendar` and Microsoft
`Calendars.ReadWrite`. `Account.calendarConsent` comes from the granted
scopes in the token response. A 403 "insufficient scope" from either API
sets it back to false and emits `accounts:changed` and `calendar:changed`.
Accounts added before calendar support start without consent;
`EnableCalendar(accountId)` signs in again for the same mailbox. For Google
this is incremental authorization (`include_granted_scopes=true`), and mail
data is kept. If the user leaves calendar access unticked on the consent
screen, it returns an error. Demo accounts always have consent.

**Sync** runs after each successful mail sync (same interval), with one
state per calendar. The window runs from the first of the month three
months back to twelve months ahead; when the month changes, the next round
is a full fetch.
- Google: `events.list?singleEvents=true` over the window, then
  `syncToken` rounds. A 410 means a full resync.
- Graph: `/me/calendarView/delta` for the default calendar, the only
  calendar v1.0 supports delta on. Other calendars refetch their
  `calendarView` window each round. Requests send
  `Prefer: IdType="ImmutableId", outlook.timezone="UTC",
  outlook.body-content-type="text"`.
- When `ListEvents` asks for a range outside the window, it answers from the
  cache and fetches that range in the background (once per 10 minutes per
  calendar and range), followed by `calendar:changed`.

**Model.**
- Times are UTC. `timeZone` keeps the event's IANA zone; Graph's Windows
  zone names are mapped through the CLDR table, and the binary embeds tzdata
  so this also works on Windows.
- All-day events have dates at 00:00 UTC and an exclusive end.
- Occurrences of a series carry `isRecurring`, a `seriesId` shared by the
  series, and `recurrence`, a text summary read from the series master.
- Also mapped:
  - attendees with their responses, the organizer and `myResponse`;
  - meeting links (Google `hangoutLink`/conference data, Graph
    `onlineMeeting.joinUrl`; https only);
  - pop-up reminders (Google calendar defaults are applied);
  - `canEdit`.
- Cancelled events are removed.
- **Video calls**: `Calendar.videoCallProvider` is `meet`, `teams`,
  `skype` (personal Microsoft accounts that only allow Skype) or `""`.
  - With `EventInput.addVideoCall`, Google gets a Meet link
    (`conferenceData.createRequest`, `hangoutsMeet`,
    `conferenceDataVersion=1`) and Graph gets an online meeting
    (`isOnlineMeeting` with `teamsForBusiness`/`skypeForConsumer`, from
    the calendar's `allowedOnlineMeetingProviders`). An existing call is
    kept. Without a provider, the request is refused.
  - A typed `EventInput.meetingUrl` (https only) goes into the location
    when that is empty; otherwise "Join: <url>" is put in front of the
    description.
  - `meetingUrl` is read from conference data, or else detected in the
    location or description (Zoom, Meet, Teams, Webex, GoTo, Whereby,
    Jitsi, Chime, BlueJeans).

**Changes** are optimistic, like mail: the local cache changes first and is
restored if the provider refuses.
- `CreateEvent` shows single events at once. For a series, it syncs the
  occurrences from the provider and returns the first.
- `UpdateEvent(id, input, "this"|"series")` with "series" targets the series
  master and shifts it by the change made to this occurrence. A recurrence
  in the input replaces the rule; `none` keeps it.
- `DeleteEvent(id, scope)` deletes one occurrence or the whole series.
- `RespondToEvent` answers for the whole series. Google patches your
  attendee status; Graph uses accept/tentativelyAccept/decline. Invitations
  and responses are sent to the other attendees.
- Updates and deletes send the synced etag as `If-Match` (Google `etag`,
  Graph `@odata.etag`). If the event changed elsewhere (412), the change is
  reverted, the calendar is resynced and the error says "This event changed
  elsewhere". Series updates go to the master and aren't conditional.
- **Moving**: `UpdateEvent` with another `calendarId` moves the event, or
  the whole series (scope "series"; single occurrences can't move).
  - Within a Google account it uses `events.move`, so guests aren't
    re-invited.
  - Within an Outlook account, Graph has no move: the event is created in
    the target calendar and deleted from the source.
  - Across accounts it is created in the target and deleted from the
    source.
  - Only the organizer can move an event. Ask the user first with
    `MoveEventCheck(id, calendarId) → {allowed, reason, crossAccount,
    hasAttendees, recurring}`. `hasAttendees` means attendees get a
    cancellation and a new invitation.
- Attendee emails are validated and de-duplicated. Descriptions are plain
  text, HTML-escaped when sent to Google and converted back to text when
  read.
- `canEdit` also requires the calendar to be writable now, so a permission
  downgrade takes effect without a resync. Deleted calendars take their
  events with them.

**Reminders.**
- With `calendarNotifications` (default true), a desktop notification
  appears for each reminder of upcoming events of accounts that are enabled
  and not muted, whatever the calendar checkboxes say. The event's own reminders are used, else
  `calendarDefaultReminder` (default 10 minutes).
- Declined and cancelled events are skipped. All-day events count from
  local midnight.
- A reminder is shown once, at most 10 minutes late (after sleep or a
  restart). Shown reminders are remembered in the database, so restarts
  don't repeat them.
- Clicking one emits `calendar:open {eventId}`.

**Calendar settings** (stored for the Calendar tab):
- `calendarWeekStart` (`auto` | `sunday` | `monday` | `saturday`)
- `calendarDefaultView` (`week` | `day` | `month` | `agenda`)
- `calendarDefaultDuration` (minutes, default 30)
- `calendarShowDeclined` (default false; `ListEvents` leaves declined
  invitations out unless it is on)
- `calendarDefaultReminder`
- `calendarNotifications`

**Time zones and all-day events.** Graph sometimes reports all-day
boundaries converted from the event's zone (for example 15:00Z for
midnight in Tokyo). gomail recovers the date from the original zone, or
from the nearest midnight when the zone is unknown, so all-day events never
shift by a day. Windows zone names that aren't mapped leave `timeZone`
empty and fall back to UTC when writing. Send all-day dates to
`CreateEvent`/`UpdateEvent` either as `YYYY-MM-DDT00:00:00Z` or with the
local offset: the date part is used.

**Invitations in mail.** A `text/calendar` part (Gmail, and the demo
invitation) is parsed into `Message.invite {uid, title, start, end, allDay,
location, organizer, method, eventId}`. `eventId` points to the synced
event with the same iCalUID, so the UI can call `RespondToEvent`. Outlook
meeting messages (`#microsoft.graph.eventMessage`) carry no ICS part.
Their event is read with `$expand=microsoft.graph.eventMessage/event`
(method from `meetingMessageType`), and `eventId` is linked the same way.

## Backup and restore

`ExportBackup(includeSecrets)` shows a save dialog and writes
`gomail-backup-YYYY-MM-DD.gomailbackup`. The file is gzip-compressed JSON
`{format: "gomail-backup", version: 1, createdAt, appVersion,
includesSecrets, settings, extra, accounts, secrets?}`:

- `settings` is the whole `models.Settings`, marshaled as is, so new
  fields are included automatically. The OAuth app credentials are blanked.
- `extra` holds other stored preferences (deleted provider labels per
  mailbox).
- `accounts` holds the provider, email, name, color, signature and calendar
  switches of each account, never its OAuth tokens. Each account also
  carries `labelMarks` (`{remoteId, labelId, kind: manual|ai|rejected|
  override|classified, confidence}`, keyed by the provider's message id)
  and `calendars` (`{remoteId, visible, color}`). Provider and Junk labels
  aren't included, because sync derives them again.
- `secrets` is only written with `includeSecrets`. It holds the OpenRouter
  key and the Google/Microsoft client id, secret and tenant.

`ImportBackup()` shows an open dialog, validates `format` and `version`
(a newer version is rejected), and merges the backup:

- Every setting in the backup replaces the current one. Settings the
  backup lacks keep their value. Credentials only change when the backup
  has secrets.
- Labels are merged per mailbox, after the accounts: each backup mailbox's
  labels go to that mailbox here, matched by name (the backup's color,
  description and action win) or added. A backup from before labels were
  per mailbox is split first, like on startup. The backup's rules and quiet
  labels replace those of its mailboxes, and label marks follow the label
  ids here.
- Missing accounts are recreated with their id, with `needsReauth` and a
  "Restored from a backup" `syncError`, and `ReauthAccount` finishes them.
  If a token for that id is still in the keychain, it is used. An account
  that already exists (same provider and email) is kept unchanged. It only
  gains the backup's label marks, its calendar choices, and its signature
  if it has none.
- Label marks and calendar choices go to `pending_labels` and
  `pending_calendars`, keyed by provider, email and remote id. They are
  applied right away to messages and calendars already present, and
  otherwise on ingest (`ApplySync`, `ReplaceCalendars`).

## Sync between computers

Settings → Sync keeps several installs the same through a `gomail-sync`
server (`cmd/gomail-sync`, `internal/syncserver`; deployment in
[deploy/sync/README.md](../deploy/sync/README.md)). The default server is
`https://gomail.avmg.nl`.

**Sync code and keys.** `ConnectDeviceSync({create: true})` makes a sync
code: 24 words from the BIP39 English list (`internal/devsync/code.go`),
256 random bits plus an 8-bit checksum, encoded exactly like a BIP39
mnemonic. Other computers call `ConnectDeviceSync({code})`; `ParseCode`
ignores case, spacing, line breaks and numbering, takes four-letter
abbreviations, names a word that isn't in the list and rejects a bad
checksum. HKDF-SHA256 derives three values from the code's entropy: the
account id (sent), the auth key (sent; the server stores a salted SHA-256)
and the AES-256-GCM key (never sent). The code is kept in the OS keychain
(`devicesync:code`); `GetDeviceSyncCode` returns it for the next computer.

**Documents.** The engine (`internal/devsync`) syncs a few documents of keyed
JSON values, each encrypted (gzip, then AES-GCM with the document name as
associated data) and stored with a revision. A key's entry carries its
value and the time it was written; merging takes the newer entry per key,
and deletions leave tombstones (forgotten after 180 days).
`internal/core/devsync.go` maps app state to documents. Mailboxes are named
`provider:email` and labels by a per-computer sync id, because local ids
differ:

| Document | Keys |
|---|---|
| `accounts` | `a/<mailbox>` name, color, signature, order, calendar switches; `c/<mailbox>` calendar choices (shown, color, reminders muted); with sign-in sync on: `t/<mailbox>` refresh token, `s/openrouter`, `s/oauth` (OAuth app); `secrets` the switch itself |
| `settings` | `g/<field>` for the fields in `syncedSettings`; `m/<mailbox>/<choice>` muted, auto-label, AI corrections off, Junk stays put, Not-junk senders, hidden folders (by remote id) |
| `labels` | `l/<mailbox>/<sync id>` labels, `l/*/junk`, `r/<mailbox>/<field>/<action>/<text>` rules, `q/<mailbox>` quiet labels, `x/<mailbox>` deleted imported labels |
| `marks-<hash>` | one per mailbox: label decisions per remote message id (manual, AI with confidence, rejected, override, already labeled by Jev) |

Not synced: `deviceSettings` (zoom, launch at login, tray, close to tray,
mail kept, check intervals, `deviceSyncInterval`) and everything the
provider already syncs (mail, folders, archive/junk/trash moves, read and
starred state). `TestDeviceSyncClassifiesEverySetting` fails until a new
settings field is put in one of the lists.

**Rounds.** On start, every `deviceSyncInterval` minutes (1 by default;
5, 15, 30, 60, or -1 for only on start and "Sync now"), after waking, when
back online and on `SyncDevicesNow`. A round compares each document with its
local shadow (the entries as last merged, stored encrypted in the settings
table) to find this computer's changes, fetches the server's copy if its
revision moved, merges, applies what other computers changed, and uploads
with the revision it read; a 409 means another computer was faster, so it
merges again. Applying makes the local state equal the shadow, so nothing
is sent back. Values a computer had before it first synced count as oldest:
a computer that joins takes the synced settings and adds what only it has.
Keys this computer can't hold yet (a mailbox it doesn't have) are kept and
applied once it can. A sign-in from another computer is only used for a
mailbox without a working one; a rotated refresh token isn't uploaded again.
After applying, the backend emits `settings:changed` (the UI re-reads its
settings) and `devicesync:status` carries `DeviceSyncStatus`.

## Data directory

| OS      | Default                                   |
|---------|-------------------------------------------|
| Linux   | `$XDG_CONFIG_HOME/gomail`, default `~/.config/gomail` |
| macOS   | `~/Library/Application Support/gomail`    |
| Windows | `%AppData%\gomail` (roaming profile)       |

These come from Go's `os.UserConfigDir`. `GOMAIL_DATA_DIR` overrides them,
and demo mode uses a `demo` subdirectory.

The directory contains:

- `gomail.db`: SQLite in WAL mode, with migrations tracked by `PRAGMA user_version`.
- `gomail.log`: the log, rotated at 5 MB.
- `openrouter-models.json`: the model list cache (1 h).
- `attachments/`: the attachment cache (LRU, 500 MB).
- `secrets.enc` and `secrets.key`: the encrypted fallback secret store,
  created only when the OS keyring can't be used.

OAuth refresh tokens and the OpenRouter key go into the OS keyring:

- macOS: the login Keychain (service `gomail`).
- Windows: Credential Manager. A credential holds at most 2.5 KB, and
  Microsoft refresh tokens can be bigger, so values over 2,000 bytes are
  split into numbered chunks (`<key>#0`, `<key>#1`, ...) and the main
  entry records how many there are.
- Linux: the Secret Service (GNOME Keyring, KWallet).

When no keyring is available or a write fails, the value goes to the
AES-GCM encrypted file instead, and any stale keyring copy is deleted. The
file's key is protected with DPAPI on Windows, where file modes don't
restrict access. On macOS and Linux it is a mode-0600 file in the
mode-0700 data directory. Keys written by older versions are upgraded in
place.

File names in the data directory are lower-case hex or fixed names, so
case-insensitive file systems (the default on macOS and Windows) can't
make two cache entries collide. Attachment names offered in the save
dialog are cleaned for Windows: reserved characters, trailing dots and
spaces, and device names such as `CON` or `NUL.txt`. Built messages always
use CRLF line endings, whatever the input or the platform.

**Webview storage.** The webview's localStorage and cache live under
`<data dir>/webview`, so profiles don't share them:

- Windows: `WebviewUserDataPath`.
- Linux: WebKitGTK follows `XDG_DATA_HOME` and `XDG_CACHE_HOME`. These are
  only redirected for custom profiles (`GOMAIL_DATA_DIR` or demo), so GTK
  itself isn't affected for normal users.
- macOS: WKWebView storage is per bundle id and can't be redirected, so
  profiles share it there.

## Environment variables

| Variable | Purpose |
|---|---|
| `GOMAIL_DATA_DIR` | Override the data directory |
| `GOMAIL_DEMO=1` | Demo mode (see below); uses `<data dir>/demo` |
| `GOMAIL_LOG_LEVEL` | `debug`, `info` (default), `warn`, `error` |
| `GOMAIL_SECRET_BACKEND=file` | Skip the OS keyring |
| `GOMAIL_GOOGLE_CLIENT_ID`, `GOMAIL_GOOGLE_CLIENT_SECRET` | Google OAuth client |
| `GOMAIL_MICROSOFT_CLIENT_ID`, `GOMAIL_MICROSOFT_TENANT` | Microsoft app registration |

Client credentials are taken from the first of these that is set: Settings,
then environment variables, then build-time defaults. Distributors can bake
in the build-time defaults:

```
-ldflags "-X github.com/AVMG20/gomail/internal/auth.DefaultGoogleClientID=... \
          -X github.com/AVMG20/gomail/internal/auth.DefaultGoogleClientSecret=... \
          -X github.com/AVMG20/gomail/internal/auth.DefaultMicrosoftClientID=... \
          -X main.version=1.2.3"
```

## Creating a Google OAuth client (Gmail)

1. Open the [Google Cloud console](https://console.cloud.google.com/) and create a project, or select one.
2. Go to **APIs & Services → Library** and enable the **Gmail API** and the **Google
   Calendar API**.
3. Go to **APIs & Services → OAuth consent screen** (Google Auth Platform) and set it up:
   - Pick the **External** user type and fill in the app name and support email.
   - Under **Data access**, add the scopes `openid`, `email`, `profile`,
     `https://www.googleapis.com/auth/gmail.modify` and
     `https://www.googleapis.com/auth/calendar` (sensitive, not restricted).
   - Under **Audience**, add your Google account as a **test user**. While the app is in
     "Testing", only test users can sign in, and their refresh tokens expire after 7 days.
     Publishing a Gmail app to everyone requires Google's verification.
4. Go to **Clients** (APIs & Services → Credentials), click **Create client** and choose
   **Desktop app**. Copy the **Client ID** and **Client secret**.
5. Paste both into gomail **Settings → Accounts**, or set the `GOMAIL_GOOGLE_*` environment
   variables. A desktop client secret is not really secret; Google's installed-app flow
   expects it to ship with the app. gomail also uses PKCE.

gomail signs in through a loopback redirect on `http://127.0.0.1:<random port>/`. Desktop
clients accept it without any redirect URI configuration.

## Creating a Microsoft app registration (Outlook / Microsoft 365)

1. Open the [Azure portal](https://portal.azure.com/) and go to **Microsoft Entra ID → App
   registrations → New registration**.
2. Choose **Supported account types**. For outlook.com and work accounts, pick "Accounts in
   any organizational directory and personal Microsoft accounts". This works with tenant
   `common`.
3. Under **Redirect URI**, choose platform **Public client/native (mobile & desktop)** and
   enter `http://localhost`. Microsoft allows any port on localhost.
4. After creating it, open **Authentication** and turn on **Allow public client flows**. No
   client secret is needed.
5. Open **API permissions → Add a permission → Microsoft Graph → Delegated** and add
   `offline_access`, `openid`, `email`, `profile`, `User.Read`, `Mail.ReadWrite`,
   `Mail.Send`, `MailboxSettings.Read` and `Calendars.ReadWrite`. `MailboxSettings.Read`
   is used to read the colors of Outlook categories.
6. Copy the **Application (client) ID** into gomail **Settings → Accounts**, or set
   `GOMAIL_MICROSOFT_CLIENT_ID`. Leave the tenant at `common`, or enter your tenant ID or
   `organizations` / `consumers`.

## OpenRouter

Add an API key in Settings. It is stored in the keyring and never sent back to the
frontend.

- **Chat model** (`openRouterModel`, default `openai/gpt-4o-mini`): used by
  `ImproveGrammar` and `SummarizeMessage`.
- **Classifier** (`classifierModel`, default `typesafe/jev-1.13`, alias
  `~typesafe/jev-latest`): uses the Decisions API (`POST /api/alpha/decisions`).

`ListAIModels` works without a key. It returns every model with its price in
USD per 1M tokens and is cached for an hour in memory and on disk.
`RefreshAIModels` forces a new fetch.

## Demo mode

```
GOMAIL_DEMO=1 wails3 dev      # or: GOMAIL_DEMO=1 ./gomail
```

Demo mode seeds a Gmail-like account (`alex.demo@gmail.com`) and an Outlook-like account
(`alex.demo@outlook.com`). They hold about 65 messages each, including threads,
attachments, inline images, spam, labels and categories, and are backed by an in-memory
provider. All actions and sending work. New mail arrives at random during periodic syncs.
Demo data lives in `<data dir>/demo`, so it never mixes with real accounts. The AI
features still need a real OpenRouter key.

## Development

```
go build ./... && go vet ./... && go test ./...
wails3 generate bindings -clean=true -ts -i   # after changing MailService or models
go test -run xxx -bench . -benchtime 3x ./internal/store ./internal/core
```

**Tests.** `internal/fakes` has stateful in-process fakes of the external
services: OAuth token endpoints (PKCE is checked, refresh tokens can be
revoked), the Gmail REST API (labels, history, batchModify, drafts and the
upload endpoint), Microsoft Graph (delta queries, `$batch` with
sub-request headers, createReply/createForward, attachments and upload
sessions, categories) and OpenRouter (Decisions, models, chat).
`app_e2e_test.go` drives the bound `MailService` against them and
exercises the production code below the Wails bridge:

- signing in through the real loopback flow;
- syncing both providers and listing conversations;
- bodies and attachments;
- optimistic actions and label write-back;
- AI labels;
- sending with attachments;
- saving and deleting drafts;
- incremental sync;
- a revoked sign-in (401, then the refresh fails with `invalid_grant`,
  then `needsReauth`) followed by `ReauthAccount`.

Coverage (`go test -cover ./...`):

| Package | Coverage |
|---|---|
| store | 76% |
| ai | 79% |
| provider/gmail | 70% |
| provider/outlook | 70% |
| mailmime | 85% |
| syncer | 88% |
| core | 72% |
| auth | 78% |
| demo | 94% |
| secrets | 67% |

Benchmarks on the reference box, a 4-vCPU Xeon:

| Operation | Time |
|---|---|
| Storing 5,000 synced messages | 1.3 s (folder kinds computed in the upsert, membership diffs, batched contacts and vocabulary) |
| Initial sync of a 5,000-message demo account, end to end | 1.7 s |
| `ListMessages` on a 50k inbox, conversation view | 7 ms (maintained `folder_threads` counters) |
| `ListMessages` on a 50k inbox, message view | 3 ms |
| Cold start to the first page with 50k messages | 25 ms (open: 3 ms) |
| `GetLabelCounts` on 50k messages | 5 ms (denormalized and index-only) |
| Search on 50k messages | 8–25 ms per query |
| List plus counts on a 5k account | 13 ms |

## Troubleshooting

- **Linux containers or CI: WebKitGTK aborts with SIGTRAP** ("Failed to
  fully launch dbus-proxy" or "bwrap: Can't find source path ...
  bus-proxy-..."). This happens when a session bus exists but the
  environment can't run WebKit's bubblewrap sandbox, for example without
  user namespaces. It is an environment limitation, not a gomail bug, and
  gomail doesn't change WebKit's sandbox. For local testing, start it with
  `GTK_A11Y=none WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 gomail`. Never
  set these variables for real use.
- **"gomail is already running"** with no window: another process holds
  `<data dir>/instance.lock`. Quit that process, or kill it if it hangs; the
  lock is released when it exits.
- **Damaged settings**: fields that still decode are kept and the rest go
  back to defaults; the original JSON is saved under the `settings.damaged`
  key in the database. A damaged `secrets.key` is never replaced while
  `secrets.enc` exists. gomail asks you to sign in again instead of
  silently losing the stored tokens.
- **Single-instance warning in the log** ("single-instance mode is
  unavailable"): there is no D-Bus session bus. gomail still works, but a
  second launch shows the "already running" dialog instead of focusing the
  open window.

## Known limitations

- Outlook attachments over 3 MB are sent with upload sessions. Gmail messages are capped
  at 35 MB, and the backend caps outgoing attachments at 25 MB in total.
- Mail fetched with `LoadOlder` or server search is not followed by Graph's delta queries,
  which are restricted to the initial window, so later changes made elsewhere to those
  older messages show up only when they are fetched again.
- Gmail's Trash can't be emptied from gomail (see Folder operations).
- Gmail user labels are labels, not folders, so `MoveToFolder` to a Gmail label isn't
  offered.
- The encrypted-file secret fallback keeps its key next to the data. It protects against
  casual disclosure, not against malware running as the same user.
