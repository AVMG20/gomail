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
Events: `sync:started`, `sync:finished`, `messages:changed`,
`accounts:changed`, `labels:changed`, `mail:new` (plus a desktop
notification), `classify:progress`, `classify:finished`.

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

**HTML bodies** are sanitized with bluemonday. Tables, inline styles and
images are kept; scripts, iframes, forms and event handlers are removed.
`cid:` images are inlined as `data:` URIs. Remote images are left alone, and
`hasRemoteContent` tells the UI whether a body has any. Render bodies in a
sandboxed `<iframe srcdoc>` so their `<style>` rules cannot affect the app.

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
- `secrets.enc` and `secrets.key`: the encrypted fallback secret store,
  created only when the OS keyring can't be used.

OAuth refresh tokens and the OpenRouter key go into the OS keyring
(Keychain, Credential Manager or Secret Service). They fall back to the
encrypted file when no keyring is available or a value is too large for it.

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
  `ImproveGrammar`, `SummarizeMessage` and `DraftReply`.
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
go test -run xxx -bench Search ./internal/store   # search on 50k messages
```

## Known limitations

- Outlook attachments over 3 MB are sent with upload sessions. Gmail messages are capped
  at 35 MB, and the backend caps outgoing attachments at 25 MB in total.
- Only the first sync window of messages is cached (see Sync above). Older mail is not
  fetched or searchable yet: there is no server-side search fallback.
- Gmail user labels are labels, not folders, so `MoveToFolder` to a Gmail label isn't
  offered.
- The encrypted-file secret fallback keeps its key next to the data. It protects against
  casual disclosure, not against malware running as the same user.
