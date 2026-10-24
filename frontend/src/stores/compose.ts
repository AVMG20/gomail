import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { displayName, escapeHtml, formatAddress, htmlHasText, htmlToText, isEmail, longDate, signatureBlock, textToHtml } from '@/lib/format'
import { deleteDraftFiles, loadDraftFiles, pruneDraftFiles, saveDraftFiles } from '@/lib/draftFiles'
import { uid } from '@/lib/platform'
import type { Address, ComposeMode, Message } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useMailStore } from './mail'
import { useSettingsStore } from './settings'
import { useUiStore } from './ui'

export interface DraftAttachment {
  id: string
  filename: string
  mimeType: string
  size: number
  dataBase64: string
  /** Loaded from a reopened draft: its attachment id on the server draft (pairs the file there). */
  remoteId?: string
}

export interface Draft {
  id: string
  mode: ComposeMode
  accountId: string
  to: Address[]
  cc: Address[]
  bcc: Address[]
  showCc: boolean
  subject: string
  html: string
  attachments: DraftAttachment[]
  replyToId: string
  minimized: boolean
  expanded: boolean
  sending: boolean
  /** Waiting out the undo-send delay; hidden from the dock. */
  queued?: boolean
  /** Bumped when html is replaced programmatically (so the editor reloads). */
  version: number
  savedAt?: number
  /** Server-side draft id once SaveDraft has run (backend pass 2). */
  remoteId?: string
  /** Account that server draft lives in (the From account can change afterwards). */
  remoteAccountId?: string
  /** Restored after the app closed during the undo-send delay (never sent). */
  interrupted?: boolean
  /** Forward: the original's files, which the server attaches itself (shown read-only). */
  forwarded?: { filename: string; size: number }[]
  /** Reply/forward as prefilled (quoted text, recipients): closing it unchanged saves nothing. */
  pristine?: string
  /** Restored after a restart: the files' bytes are still being read back from IndexedDB. */
  filesPending?: boolean
  /** Restored after a restart: files whose bytes couldn't be read back (they must be re-added). */
  missingFiles?: string[]
}

const DRAFTS_KEY = 'gomail.drafts.v1'
/** The backend refuses more than this in attachments (core.maxAttachmentBytes). */
export const MAX_ATTACH_BYTES = 25 * 1024 * 1024

function loadDrafts(): Draft[] {
  try {
    const raw = JSON.parse(localStorage.getItem(DRAFTS_KEY) || '[]') as Draft[]
    // Only drafts that were still open in the dock are stored; empty ones (just a signature) are purged.
    // They come back minimised; anything that was mid-send becomes a normal draft again.
    const list = (Array.isArray(raw) ? raw : []).filter((d) => d && Array.isArray(d.to) && draftHasContent(d))
    if (list.length !== (Array.isArray(raw) ? raw.length : 0)) localStorage.setItem(DRAFTS_KEY, JSON.stringify(list))
    // Sends still waiting out the undo delay when the app closed come back as drafts, flagged.
    // Attachment bytes live in IndexedDB (restoreFiles reads them back); older versions kept them inline.
    return list.map((d) => ({
      ...d, cc: d.cc ?? [], bcc: d.bcc ?? [], attachments: d.attachments ?? [], minimized: true, expanded: false,
      interrupted: !!(d.queued || d.sending), sending: false, queued: false,
      filesPending: (d.attachments ?? []).some((a) => !a.dataBase64) || undefined,
    }))
  } catch {
    return []
  }
}

function contentSig(d: Pick<Draft, 'to' | 'cc' | 'bcc' | 'subject' | 'html' | 'attachments'>): string {
  // The body's text, not its markup: the editor normalises the prefilled HTML as soon as it
  // loads (classes, a trailing paragraph), which is not the user changing anything.
  const text = htmlToText(d.html ?? '').replace(/\s+/g, ' ').trim()
  return JSON.stringify([d.to, d.cc, d.bcc, d.subject, text, d.attachments?.length ?? 0])
}

/** Recipients, subject, attachments or typed body text (the auto-inserted signature doesn't count).
 *  A reply or forward the user hasn't changed has nothing of their own in it. */
export function draftHasContent(d: Pick<Draft, 'to' | 'cc' | 'bcc' | 'subject' | 'html' | 'attachments'> & { pristine?: string }): boolean {
  if (d.pristine !== undefined && d.pristine === contentSig(d)) return false
  return !!(d.to?.length || d.cc?.length || d.bcc?.length || d.subject?.trim() || d.attachments?.length || htmlHasText(d.html ?? '', true))
}

/** The words people use for "see the attached file" (English, Dutch, German, French, Spanish). */
const ATTACH_WORDS = /\b(attach(ed|ing|ment|ments)?|enclosed|bijlage[n]?|bijgevoegd|meegestuurd|anhang|angehängt|pièce[s]? jointe[s]?|ci-joint|adjunto|adjunta)\b/i

/** The text the user wrote themselves: without the signature and the quoted message. */
export function ownText(html: string): string {
  if (!html || typeof DOMParser === 'undefined') return htmlToText(html ?? '')
  const doc = new DOMParser().parseFromString(html, 'text/html')
  doc.querySelectorAll('[data-signature], blockquote').forEach((el) => el.remove())
  // htmlToText keeps paragraphs apart ("…attached</p><p>Thanks" must not become one word).
  return htmlToText(doc.body.innerHTML).replace(/\s+/g, ' ').trim()
}

/** Reasons to ask before sending: an empty subject, or text that mentions an attachment while
 *  none is attached (forwarded files count). Empty when there is nothing to ask. */
export function sendWarnings(d: Pick<Draft, 'subject' | 'html' | 'attachments' | 'forwarded'>): ('subject' | 'attachment')[] {
  const out: ('subject' | 'attachment')[] = []
  if (!d.subject.trim()) out.push('subject')
  // Not the subject: a reply's "Re: Attached invoice" mentions the original's file.
  if (!d.attachments.length && !d.forwarded?.length && ATTACH_WORDS.test(ownText(d.html))) out.push('attachment')
  return out
}

/** The message a reply or forward answers: `prefer` (the message opened on its own, with
 *  conversations off) when it is in the thread, else the newest one that isn't a draft (an
 *  autosaved reply sits at the end of the thread; answering it would reply to yourself). */
export function replyTarget(thread: Message[], isDraft: (m: Message) => boolean, prefer?: string): Message | undefined {
  const own = prefer ? thread.find((m) => m.id === prefer) : undefined
  if (own && !isDraft(own)) return own
  for (let i = thread.length - 1; i >= 0; i--) if (!isDraft(thread[i])) return thread[i]
  return thread[thread.length - 1]
}

/** "Re: …" / "Fwd: …". A subject that already has the prefix (or a localised one: AW, SV, Antw,
 *  WG, Doorst) keeps it; another one stays too, so a reply to "Fwd: Plans" is "Re: Fwd: Plans". */
export function prefixSubject(prefix: 'Re' | 'Fwd', subject: string): string {
  const s = subject.trim() === '(no subject)' ? '' : subject.trim()
  const same = prefix === 'Re' ? /^(re|aw|sv|antw)\s*:/i : /^(fwd?|wg|doorst)\s*:/i
  if (same.test(s)) return s
  return s ? `${prefix}: ${s}` : `${prefix}:`
}

async function quotedBody(m: Message): Promise<string> {
  if (!m.bodyHtml) return textToHtml(m.bodyText)
  // Loaded on demand to keep DOMPurify out of the main bundle.
  const { default: DOMPurify } = await import('dompurify')
  return DOMPurify.sanitize(m.bodyHtml, { FORBID_TAGS: ['style', 'img', 'script'], FORBID_ATTR: ['style', 'class'] })
}

export const useComposeStore = defineStore('compose', () => {
  const drafts = ref<Draft[]>(loadDrafts())
  const timers = new Map<string, ReturnType<typeof setTimeout>>()

  // Local-first autosave (the backend has no SaveDraft yet).
  let saveTimer: ReturnType<typeof setTimeout> | null = null
  // Watch what is persisted, not bookkeeping like savedAt (persist() writes that,
  // and watching it re-triggered persist every 600 ms forever).
  watch(() => JSON.stringify(drafts.value.map(({ savedAt: _s, interrupted: _i, ...rest }) => rest)), () => {
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(persist, 600)
  })

  // Server drafts (when the backend supports them), throttled per draft.
  const remoteTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const lastRemote = new Map<string, string>()
  /** SaveDraft calls in flight: sending waits for them so the server draft is replaced, not orphaned. */
  const remoteInflight = new Map<string, Promise<void>>()
  const scheduledRemote = new Map<string, string>()
  /** Files of a restored draft aren't all back: saving or sending now would drop them. */
  function filesIncomplete(d: Draft): boolean {
    return !!d.filesPending || !!d.missingFiles?.length
  }

  function remoteSig(d: Draft): string {
    return JSON.stringify([d.accountId, d.to, d.cc, d.bcc, d.subject, d.html, d.attachments.length])
  }
  /** The server copy has the draft's latest edits (a failed or pending save means it doesn't). */
  function savedRemotely(d: Draft): boolean {
    return !!d.remoteId && !remoteTimers.has(d.id) && !remoteInflight.has(d.id) && !filesIncomplete(d) && lastRemote.get(d.id) === remoteSig(d)
  }

  function scheduleRemote(d: Draft) {
    // A draft whose files aren't back must not replace the server copy (which still has them).
    if (!hasMethod('SaveDraft') || d.queued || d.sending || !draftHasContent(d) || filesIncomplete(d)) return
    const sig = remoteSig(d)
    if (lastRemote.get(d.id) === sig || scheduledRemote.get(d.id) === sig) return
    scheduledRemote.set(d.id, sig)
    const t = remoteTimers.get(d.id)
    if (t) clearTimeout(t)
    remoteTimers.set(d.id, setTimeout(async () => {
      remoteTimers.delete(d.id)
      scheduledRemote.delete(d.id)
      const cur = drafts.value.find((x) => x.id === d.id)
      if (!cur || cur.queued || cur.sending || filesIncomplete(cur)) return
      // A save still running (large files take a while) must finish first: until it
      // returns the draft has no server id, and this save would create a second draft.
      const prev = remoteInflight.get(d.id)
      const run = (async () => {
        if (prev) await prev
        if (cur.queued || cur.sending || filesIncomplete(cur)) return
        try {
          dropForeignRemote(cur)
          const accountId = cur.accountId
          cur.remoteId = await api.saveDraft(toRequest(cur), cur.remoteId ?? '')
          cur.remoteAccountId = accountId
          lastRemote.set(d.id, sig)
        } catch {
          /* keep the local copy; retry on the next edit */
        }
      })()
      remoteInflight.set(d.id, run)
      await run
      if (remoteInflight.get(d.id) === run) remoteInflight.delete(d.id)
    }, 3000))
  }

  /** The From account changed: the old server draft belongs to another mailbox, so delete it
   *  and let the next save create one in the new account (otherwise it lingers in Drafts). */
  function dropForeignRemote(d: Draft) {
    if (!d.remoteId || !d.remoteAccountId || d.remoteAccountId === d.accountId) return
    const old = d.remoteId
    d.remoteId = undefined
    d.remoteAccountId = undefined
    if (hasMethod('DeleteDraft')) void api.deleteDraft(old).catch(() => {})
  }

  /** Attachment ids last written to IndexedDB, per draft (to skip rewriting unchanged files). */
  const storedFiles = new Map<string, string>()
  function persistFiles(d: Draft) {
    if (d.filesPending) return // not read back yet: keep what is stored
    const sig = d.attachments.map((a) => a.id).join(',')
    if (storedFiles.get(d.id) === sig) return
    storedFiles.set(d.id, sig)
    const done = d.attachments.length ? saveDraftFiles(d.id, d.attachments) : deleteDraftFiles(d.id).then(() => true)
    void done.then((ok) => {
      if (!ok && storedFiles.get(d.id) === sig) storedFiles.delete(d.id) // retry on the next change
    })
  }

  function persist() {
    drafts.value.forEach(scheduleRemote)
    // localStorage only gets the files' names and sizes; their bytes go to IndexedDB.
    const list = drafts.value.filter((d) => draftHasContent(d)).map((d) => {
      persistFiles(d)
      return { ...d, attachments: d.attachments.map(({ dataBase64: _b, ...meta }) => meta) }
    })
    try {
      localStorage.setItem(DRAFTS_KEY, JSON.stringify(list))
      const now = Date.now()
      for (const d of drafts.value) if (draftHasContent(d)) d.savedAt = now
    } catch {
      /* quota: keep in memory only */
    }
  }

  function create(partial: Partial<Draft>): Draft {
    const accounts = useAccountsStore()
    const mail = useMailStore()
    const d: Draft = {
      id: uid('draft'), mode: 'new',
      accountId: mail.mailbox.accountId || accounts.accounts[0]?.id || '',
      to: [], cc: [], bcc: [], showCc: false, subject: '', html: '', attachments: [], replyToId: '',
      minimized: false, expanded: false, sending: false, version: 0,
      ...partial,
    }
    // New messages start with the From account's signature.
    if (!partial.html && d.mode === 'new') {
      const sig = accounts.accountMap.get(d.accountId)?.signature ?? ''
      d.html = sig ? `<p></p>${signatureBlock(sig)}` : ''
    }
    // Minimise other windows; keep at most four drafts in the dock. Sends in progress are never
    // dropped, nor drafts whose latest edits aren't on the server yet (the dock grows instead).
    drafts.value.forEach((x) => (x.minimized = true))
    const next = [...drafts.value, d]
    const others = next.filter((x) => x !== d && !x.queued && !x.sending)
    const drop = others.length > 3 ? others.find((x) => !draftHasContent(x) || savedRemotely(x)) : undefined
    if (drop) {
      drafts.value = next.filter((x) => x !== drop)
      if (draftHasContent(drop)) toast('Draft moved to Drafts', { description: drop.subject || '(no subject)' })
    } else drafts.value = next
    return drafts.value[drafts.value.length - 1]
  }

  function compose(to: Address[] = []) {
    return create({ to })
  }

  /** Prefilled new message (mailto: links, compose:open from the backend). */
  function composeWith(p: { to?: Address[]; cc?: Address[]; bcc?: Address[]; subject?: string; body?: string; accountId?: string }) {
    const accounts = useAccountsStore()
    const mail = useMailStore()
    // From: the given mailbox (e.g. the calendar event's), else the one being viewed.
    const accountId = (p.accountId && accounts.accountMap.has(p.accountId) ? p.accountId : '') || mail.mailbox.accountId || accounts.accounts[0]?.id || ''
    const sig = accounts.accountMap.get(accountId)?.signature ?? ''
    const body = p.body ? textToHtml(p.body) : '<p></p>'
    const cc = p.cc ?? []
    const bcc = p.bcc ?? []
    return create({ accountId, to: p.to ?? [], cc, bcc, showCc: cc.length + bcc.length > 0, subject: p.subject ?? '', html: `${body}${sig ? signatureBlock(sig) : ''}` })
  }

  async function respond(mode: Exclude<ComposeMode, 'new'>, m: Message) {
    // Pressing r twice (or Reply on the same message again) reopens the existing reply.
    const open = drafts.value.find((d) => d.replyToId === m.id && d.mode === mode && !d.queued && !d.sending)
    if (open) return focus(open.id)
    const accounts = useAccountsStore()
    const own = accounts.accountMap.get(m.accountId)?.email.toLowerCase() ?? ''
    const notMe = (a: Address) => a.email.toLowerCase() !== own
    let to: Address[] = []
    let cc: Address[] = []
    if (mode === 'reply' || mode === 'replyAll') {
      const fromMe = m.from.email.toLowerCase() === own
      to = m.replyTo.length ? m.replyTo : fromMe ? m.to : [m.from]
      if (mode === 'replyAll') {
        const seen = new Set(to.map((a) => a.email.toLowerCase()))
        cc = [...m.to, ...m.cc].filter((a) => notMe(a) && !seen.has(a.email.toLowerCase()) && (seen.add(a.email.toLowerCase()), true))
      }
    }
    // Settings → Mail → Quote the original in replies; forwards always carry the message.
    const quoting = mode === 'forward' ? 'header' : useSettingsStore().settings?.replyQuote || 'header'
    const header = mode === 'forward'
      ? `<p>---------- Forwarded message ----------<br>From: ${escapeHtml(formatAddress(m.from))}<br>Date: ${escapeHtml(longDate(m.date))}<br>Subject: ${escapeHtml(m.subject)}<br>To: ${escapeHtml(m.to.map(formatAddress).join(', '))}</p>`
      : quoting === 'header' ? `<p>On ${escapeHtml(longDate(m.date))}, ${escapeHtml(displayName(m.from))} wrote:</p>` : ''
    const sig = accounts.accountMap.get(m.accountId)?.signature ?? ''
    const quote = quoting === 'none' ? '' : `<p></p>${header}<blockquote>${await quotedBody(m)}</blockquote>`
    const html = `<p></p>${signatureBlock(sig)}${quote}`
    // Forwards carry the original's files (attached by the backend), minus images embedded by Content-ID.
    const forwarded = mode === 'forward' ? (m.attachments ?? []).filter((a) => !(a.inline && a.contentId)).map((a) => ({ filename: a.filename, size: a.size })) : undefined
    const d = create({
      mode, accountId: m.accountId, to, cc, showCc: cc.length > 0,
      subject: prefixSubject(mode === 'forward' ? 'Fwd' : 'Re', m.subject), html, replyToId: m.id, forwarded,
    })
    d.pristine = contentSig(d)
    return d
  }

  /** A draft: flagged by the backend or kept in a Drafts folder. */
  function isDraftMessage(m: Message): boolean {
    const accounts = useAccountsStore()
    return !!m.isDraft || (m.folderIds ?? []).some((f) => accounts.folderMap.get(f)?.kind === 'drafts')
  }

  /** What replying to list row / open message `id` answers (see replyTarget). In conversation
   *  view that is the conversation's newest message; with conversations off, the message itself. */
  function replyTargetFor(thread: Message[], id: string): Message | undefined {
    const single = useSettingsStore().settings?.conversationView === false
    return replyTarget(thread, isDraftMessage, single ? id : undefined)
  }

  /** Reply to / forward list row or open message `id` (r, a, f, the palette, the row menu). */
  async function respondToThread(mode: Exclude<ComposeMode, 'new'>, id: string) {
    const m = replyTargetFor(await useMailStore().loadThread(id), id)
    if (m) return respond(mode, m)
  }

  /** Continue editing a draft from the Drafts folder. Its files are loaded first: the next
   *  autosave replaces the server draft, which would otherwise lose them. */
  async function editDraft(m: Message) {
    const existing = drafts.value.find((d) => d.remoteId === m.id)
    if (existing) return focus(existing.id)
    const files = (m.attachments ?? []).filter((a) => !(a.inline && a.contentId))
    let attachments: DraftAttachment[] = []
    try {
      attachments = await Promise.all(files.map(async (a) => {
        const data = await api.getAttachmentData(m.id, a.id)
        return { id: uid('att'), filename: data.filename || a.filename, mimeType: data.mimeType || a.mimeType, size: data.size ?? a.size, dataBase64: data.dataBase64, remoteId: a.id }
      }))
    } catch (e) {
      toast.error('Couldn’t open the draft', { description: `Its attachments couldn’t be loaded: ${errorMessage(e)}` })
      return
    }
    if (drafts.value.some((d) => d.remoteId === m.id)) return // opened meanwhile
    create({
      mode: 'new', accountId: m.accountId, to: m.to, cc: m.cc, bcc: m.bcc, showCc: m.cc.length + m.bcc.length > 0,
      subject: m.subject === '(no subject)' ? '' : m.subject, html: m.bodyHtml || textToHtml(m.bodyText), remoteId: m.id, remoteAccountId: m.accountId,
      attachments,
    })
  }

  function close(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    drafts.value = drafts.value.filter((x) => x.id !== id)
    storedFiles.delete(id)
    void deleteDraftFiles(id)
    // Only a window the user was in hands focus back (a send finishing after the undo delay
    // happens in the background and must not swallow the keys of whatever they do next).
    const inDock = typeof document !== 'undefined' && !!document.activeElement?.closest?.('[data-compose-dock]')
    if (d && !d.queued && (!d.minimized || inDock)) afterStepAway()
  }

  /** Minimise a compose window (Esc, the minimise button). */
  function minimize(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d || d.minimized) return
    d.minimized = true
    afterStepAway(id)
  }

  /** A compose window closed or was minimised: focus must not fall to the page body, where
   *  single-key shortcuts act on the open mail (Backspace would trash it). Focus goes to the
   *  minimised tab (no shortcuts there) or the message list, and shortcuts pause briefly. */
  function afterStepAway(id?: string) {
    if (typeof document === 'undefined') return
    useUiStore().guardKeys()
    setTimeout(() => {
      const active = document.activeElement
      if (active && active !== document.body && document.contains(active)) return
      const tab = id ? document.querySelector<HTMLElement>(`[data-dock-tab="${id}"]`) : null
      ;(tab ?? document.querySelector<HTMLElement>('[role="listbox"]'))?.focus({ preventScroll: true })
    }, 0)
  }

  /** After a restart: read the drafts' attachment bytes back. Files that can't be found are
   *  listed in missingFiles; such drafts are neither autosaved nor sent until they're re-added. */
  let restoring: Promise<void> | null = null
  function restoreFiles(): Promise<void> {
    restoring ??= restoreFilesOnce()
    return restoring
  }
  async function restoreFilesOnce() {
    const ids = new Set(drafts.value.map((d) => d.id))
    for (const d of drafts.value.filter((x) => x.filesPending)) {
      const stored = await loadDraftFiles(d.id)
      const cur = drafts.value.find((x) => x.id === d.id)
      if (!cur?.filesPending) continue
      const byId = new Map((stored ?? []).map((f) => [f.id, f]))
      const missing: string[] = []
      const back = cur.attachments.flatMap((a) => {
        if (a.dataBase64) return [a]
        const f = byId.get(a.id)
        if (f?.dataBase64) return [{ ...a, dataBase64: f.dataBase64 }]
        missing.push(a.filename)
        return []
      })
      cur.attachments = back
      cur.missingFiles = missing.length ? missing : undefined
      cur.filesPending = undefined
      storedFiles.set(cur.id, back.map((a) => a.id).join(','))
    }
    void pruneDraftFiles(new Set([...ids, ...drafts.value.map((d) => d.id)]))
  }
  if (typeof window !== 'undefined') void restoreFiles()

  /** The user re-added a file, or chose to go on without the missing ones. */
  function resolveMissing(id: string, filename?: string) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d?.missingFiles) return
    const rest = filename === undefined ? [] : d.missingFiles.filter((n) => n !== filename)
    d.missingFiles = rest.length ? rest : undefined
    if (!d.missingFiles) scheduleRemote(d)
  }

  /** Close the window: keep the draft on the server when supported. Returns false if it must be confirmed. */
  async function saveAndClose(id: string): Promise<boolean> {
    const d = drafts.value.find((x) => x.id === id)
    if (!d) return true
    if (!draftHasContent(d)) {
      // Emptied again after an autosave: drop that server copy too (it still has the old text).
      if (d.remoteId || remoteTimers.has(id) || remoteInflight.has(id)) await discard(id)
      else close(id)
      return true
    }
    if (filesIncomplete(d)) {
      // Saving now would replace the server draft without those files: keep it in the dock.
      d.minimized = true
      toast.warning('Draft kept in the dock', { description: d.filesPending ? 'Its attachments are still loading.' : `Re-add ${d.missingFiles!.join(', ')} first, or continue without them.` })
      return true
    }
    if (!hasMethod('SaveDraft')) return false
    try {
      // A first autosave still running would otherwise create a second server draft.
      const t = remoteTimers.get(id)
      if (t) clearTimeout(t)
      remoteTimers.delete(id)
      await remoteInflight.get(id)
      dropForeignRemote(d)
      const accountId = d.accountId
      d.remoteId = await api.saveDraft(toRequest(d), d.remoteId ?? '')
      d.remoteAccountId = accountId
      close(id)
      toast('Draft saved', { description: 'You can find it in Drafts.' })
      void useMailStore().softRefresh()
      return true
    } catch (e) {
      // Keep the window open: offering "Discard?" here would lose the message.
      toast.error('Couldn’t save the draft', { description: `${errorMessage(e)} The message is still open, so nothing is lost.` })
      d.minimized = false
      return true
    }
  }

  /** Discard: also remove the server copy (once a save in flight has told us its id). */
  async function discard(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    const t = remoteTimers.get(id)
    if (t) clearTimeout(t)
    remoteTimers.delete(id)
    close(id)
    await remoteInflight.get(id)
    if (d?.remoteId && hasMethod('DeleteDraft')) void api.deleteDraft(d.remoteId).catch(() => {})
  }

  function focus(id: string) {
    for (const d of drafts.value) if (d.id === id) d.minimized = false
  }

  function toRequest(d: Draft) {
    return {
      accountId: d.accountId, to: d.to, cc: d.cc, bcc: d.bcc, subject: d.subject,
      bodyHtml: d.html, bodyText: htmlToText(d.html), replyToId: d.replyToId, mode: d.mode,
      // Files loaded from the server draft go back with their id there: a new file of the same
      // name (a newer version) is uploaded instead of being taken for the old one.
      attachments: d.attachments.map(({ remoteId, filename, mimeType, dataBase64 }) => ({ id: remoteId, filename, mimeType, dataBase64 })),
      draftId: d.remoteId ?? '',
      // This is the whole file list (saves and sends wait until restored files are back), so
      // files removed here are removed from the server draft too.
      attachmentsComplete: !filesIncomplete(d),
    }
  }

  async function deliver(d: Draft) {
    const t = remoteTimers.get(d.id)
    if (t) clearTimeout(t)
    remoteTimers.delete(d.id)
    // Let a SaveDraft in flight finish so its id goes out with the send; the
    // backend then removes that server draft instead of leaving an orphan.
    await remoteInflight.get(d.id)
    dropForeignRemote(d)
    await api.sendMessage(toRequest(d))
  }

  /** The window is going away during an undo-send delay: send now rather than drop it. */
  function flushQueued() {
    for (const [id, t] of timers) {
      clearTimeout(t)
      timers.delete(id)
      void reallySend(id)
    }
    persist()
  }
  if (typeof window !== 'undefined') window.addEventListener('pagehide', flushQueued)

  // Tell the user about sends the previous session couldn't finish.
  const interrupted = drafts.value.filter((d) => d.interrupted)
  if (interrupted.length && typeof window !== 'undefined') {
    setTimeout(() => {
      toast.warning(interrupted.length === 1 ? 'A message wasn’t sent before gomail closed' : `${interrupted.length} messages weren’t sent before gomail closed`, {
        description: 'They are back in your drafts.',
        duration: 15000,
        action: { label: 'Review', onClick: () => interrupted.forEach((d) => focus(d.id)) },
      })
      interrupted.forEach((d) => (d.interrupted = false))
    }, 1500)
  }

  /** Whether the draft can be sent as it is; if not, says what to fix. The compose window asks
   *  this before its "no subject" / "attachment" questions, so "Send anyway" can't be followed
   *  by "Check this address". */
  function canSend(id: string): boolean {
    const d = drafts.value.find((x) => x.id === id)
    if (!d) return false
    if (!d.to.length && !d.cc.length && !d.bcc.length) {
      toast.error('Add at least one recipient')
      return false
    }
    const invalid = [...d.to, ...d.cc, ...d.bcc].filter((a) => !isEmail(a.email))
    if (invalid.length) {
      toast.error(`Check ${invalid.length === 1 ? 'this address' : 'these addresses'}: ${invalid.map((a) => a.email).join(', ')}`)
      return false
    }
    const bytes = d.attachments.reduce((n, a) => n + (a.size || 0), 0)
    if (bytes > MAX_ATTACH_BYTES) {
      toast.error('Attachments are larger than 25 MB', { description: 'Remove some files or share them with a link instead.' })
      return false
    }
    // Never send without files the user attached.
    if (d.filesPending) {
      toast('Attachments are still loading', { description: 'Try again in a moment.' })
      return false
    }
    if (d.missingFiles?.length) {
      toast.error(`Re-add ${d.missingFiles.length === 1 ? 'this attachment' : 'these attachments'}: ${d.missingFiles.join(', ')}`, {
        description: 'They couldn’t be restored after gomail closed. Re-add them, or choose “Continue without them”.',
      })
      return false
    }
    return true
  }

  async function send(id: string, delaySeconds = 0) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d || d.sending || d.queued || !canSend(id)) return
    if (delaySeconds > 0) {
      const visible = !d.minimized
      d.queued = true
      if (visible) afterStepAway() // the window disappears like a close: focus back to the list
      const toastId = toast('Sending…', {
        description: d.subject || '(no subject)',
        duration: delaySeconds * 1000,
        action: { label: 'Undo', onClick: () => undoSend(id) },
      })
      timers.set(id, setTimeout(() => {
        timers.delete(id)
        toast.dismiss(toastId)
        void reallySend(id)
      }, delaySeconds * 1000))
      return
    }
    d.sending = true
    await reallySend(id)
  }

  function undoSend(id: string) {
    const t = timers.get(id)
    if (t) clearTimeout(t)
    timers.delete(id)
    const d = drafts.value.find((x) => x.id === id)
    if (d) {
      d.queued = false
      d.minimized = false
    }
    toast('Sending cancelled', { description: 'Your message is back in the composer.' })
  }

  async function reallySend(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d) return
    d.sending = true
    try {
      await deliver(d)
      close(id)
      toast.success('Message sent')
      void useMailStore().softRefresh()
    } catch (e) {
      d.sending = false
      d.queued = false
      d.minimized = false
      toast.error('Could not send', { description: errorMessage(e) })
    }
  }

  return { drafts, compose, composeWith, respond, respondToThread, replyTargetFor, isDraftMessage, editDraft, close, minimize, discard, saveAndClose, focus, canSend, send, undoSend, resolveMissing, restoreFiles }
})
