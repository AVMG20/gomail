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
  └─ outlook         Microsoft Graph v1.0 client (delta queries, $batch)
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

**Notifications.** A single message shows the sender and subject.
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

**Actions** (read, star, archive, trash, move) update SQLite first, emit
`messages:changed`, then go to the provider. Gmail uses `batchModify`
(1000 ids per call) and Graph uses JSON `$batch` (20 per call). If the
provider call fails, the local change is reverted and a readable error is
returned.

**Labels.** Labels are gomail's own concept, separate from folders. A
message can have several labels. Each assignment has a source: `manual`,
`ai`, `provider` (Gmail user labels and Outlook categories, merged across
accounts by case-insensitive name) or `system` (the built-in **Junk** label,
which every message in Spam/Junk gets). Gmail system labels (INBOX, SPAM,
TRASH, SENT, DRAFT, STARRED, IMPORTANT, CATEGORY_*) are folders or flags, never
labels.

The AI classifier (OpenRouter Decisions API, model `typesafe/jev-1.13`)
asks one `noul` question per label, in chunks of 20 labels per request. It
works as follows:

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

When `showRemoteImages` is off, `GetMessage` and `GetThread` also strip
remote `src`, `background`, `srcset` and CSS `url()` references. In that
case `remoteContentBlocked` is true, and `GetMessageWithRemoteContent(id)`
returns the unblocked body. Render bodies in a sandboxed `<iframe srcdoc>`
so their `<style>` rules cannot affect the app.

**Security notes.**

- Tokens and keys are never logged. Logs carry account ids, not
  addresses.
- `OpenExternalURL` only opens http(s) and mailto.
- The OAuth loopback server listens only on loopback: 127.0.0.1, plus ::1
  for Microsoft's `localhost` redirect. It shuts down as soon as the flow
  ends or times out.

## Data directory

| OS      | Default                                   |
|---------|-------------------------------------------|
| Linux   | `~/.config/gomail`                        |
| macOS   | `~/Library/Application Support/gomail`    |
| Windows | `%AppData%\gomail`                        |

The directory contains:

- `gomail.db`: SQLite in WAL mode, with migrations tracked by `PRAGMA user_version`.
- `gomail.log`: the log, rotated at 5 MB.
- `openrouter-models.json`: the model list cache (1 h).
- `attachments/`: the attachment cache (LRU, 500 MB).
- `secrets.enc` and `secrets.key`: the encrypted fallback secret store,
  created only when the OS keyring can't be used.

OAuth refresh tokens and the OpenRouter key go into the OS keyring
(Keychain, Credential Manager or Secret Service). They fall back to the
encrypted file when no keyring is available or a value is too large for it.

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
2. Go to **APIs & Services → Library**, search for **Gmail API** and click **Enable**.
3. Go to **APIs & Services → OAuth consent screen** (Google Auth Platform) and set it up:
   - Pick the **External** user type and fill in the app name and support email.
   - Under **Data access**, add the scopes `openid`, `email`, `profile` and
     `https://www.googleapis.com/auth/gmail.modify`.
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
   `Mail.Send` and `MailboxSettings.Read`. The last one is used to read the colors of
   Outlook categories.
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

Benchmarks on the reference box, a 4-vCPU Xeon:

| Operation | Time |
|---|---|
| Storing 5,000 synced messages | 1.9 s |
| Initial sync of a 5,000-message demo account, end to end | 2.2 s |
| `ListMessages` on a 50k inbox, conversation view | 7 ms (maintained `folder_threads` counters) |
| `ListMessages` on a 50k inbox, message view | 3 ms |
| Cold start to the first page with 50k messages | 25 ms (open: 3 ms) |
| `GetLabelCounts` on 50k messages | 5 ms (denormalized and index-only) |
| Search on 50k messages | 8–25 ms per query |
| List plus counts on a 5k account | 13 ms |

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
