import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { displayName, escapeHtml, formatAddress, htmlHasText, htmlToText, isEmail, longDate, signatureBlock, textToHtml } from '@/lib/format'
import { uid } from '@/lib/platform'
import type { Address, ComposeMode, Message } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useMailStore } from './mail'

export interface DraftAttachment {
  id: string
  filename: string
  mimeType: string
  size: number
  dataBase64: string
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
}

const DRAFTS_KEY = 'gomail.drafts.v1'
const MAX_PERSIST_ATTACH = 1_500_000

function loadDrafts(): Draft[] {
  try {
    const raw = JSON.parse(localStorage.getItem(DRAFTS_KEY) || '[]') as Draft[]
    // Only drafts that were still open in the dock are stored; empty ones (just a signature) are purged.
    // They come back minimised; anything that was mid-send becomes a normal draft again.
    const list = (Array.isArray(raw) ? raw : []).filter((d) => d && Array.isArray(d.to) && draftHasContent(d))
    if (list.length !== (Array.isArray(raw) ? raw.length : 0)) localStorage.setItem(DRAFTS_KEY, JSON.stringify(list))
    // Sends still waiting out the undo delay when the app closed come back as drafts, flagged.
    return list.map((d) => ({ ...d, cc: d.cc ?? [], bcc: d.bcc ?? [], attachments: d.attachments ?? [], minimized: true, expanded: false, interrupted: !!(d.queued || d.sending), sending: false, queued: false }))
  } catch {
    return []
  }
}

/** Recipients, subject, attachments or typed body text (the auto-inserted signature doesn't count). */
export function draftHasContent(d: Pick<Draft, 'to' | 'cc' | 'bcc' | 'subject' | 'html' | 'attachments'>): boolean {
  return !!(d.to?.length || d.cc?.length || d.bcc?.length || d.subject?.trim() || d.attachments?.length || htmlHasText(d.html ?? '', true))
}

function prefixSubject(prefix: string, subject: string) {
  const re = new RegExp(`^(${prefix}|re|fwd?|aw|sv)\\s*:\\s*`, 'i')
  return re.test(subject) && subject.toLowerCase().startsWith(prefix.toLowerCase()) ? subject : `${prefix}: ${subject.replace(re, '')}`
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
  function scheduleRemote(d: Draft) {
    if (!hasMethod('SaveDraft') || d.queued || d.sending || !draftHasContent(d)) return
    const sig = JSON.stringify([d.accountId, d.to, d.cc, d.bcc, d.subject, d.html, d.attachments.length])
    if (lastRemote.get(d.id) === sig || scheduledRemote.get(d.id) === sig) return
    scheduledRemote.set(d.id, sig)
    const t = remoteTimers.get(d.id)
    if (t) clearTimeout(t)
    remoteTimers.set(d.id, setTimeout(async () => {
      remoteTimers.delete(d.id)
      scheduledRemote.delete(d.id)
      const cur = drafts.value.find((x) => x.id === d.id)
      if (!cur || cur.queued || cur.sending) return
      const run = (async () => {
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

  function persist() {
    drafts.value.forEach(scheduleRemote)
    const list = drafts.value.filter((d) => draftHasContent(d)).map((d) => {
      const size = d.attachments.reduce((n, a) => n + a.dataBase64.length, 0)
      return { ...d, attachments: size > MAX_PERSIST_ATTACH ? [] : d.attachments }
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
    // Minimise other windows; keep at most four drafts in the dock (queued sends are never dropped).
    drafts.value.forEach((x) => (x.minimized = true))
    const next = [...drafts.value, d]
    const visible = next.filter((x) => !x.queued)
    if (visible.length > 4) {
      const drop = visible[0]
      drafts.value = next.filter((x) => x !== drop)
    } else drafts.value = next
    return drafts.value[drafts.value.length - 1]
  }

  function compose(to: Address[] = []) {
    return create({ to })
  }

  /** Prefilled new message (mailto: links, compose:open from the backend). */
  function composeWith(p: { to?: Address[]; cc?: Address[]; bcc?: Address[]; subject?: string; body?: string }) {
    const accounts = useAccountsStore()
    const mail = useMailStore()
    const sig = accounts.accountMap.get(mail.mailbox.accountId || accounts.accounts[0]?.id || '')?.signature ?? ''
    const body = p.body ? textToHtml(p.body) : '<p></p>'
    const cc = p.cc ?? []
    const bcc = p.bcc ?? []
    return create({ to: p.to ?? [], cc, bcc, showCc: cc.length + bcc.length > 0, subject: p.subject ?? '', html: `${body}${sig ? signatureBlock(sig) : ''}` })
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
    const header = mode === 'forward'
      ? `<p>---------- Forwarded message ----------<br>From: ${escapeHtml(formatAddress(m.from))}<br>Date: ${escapeHtml(longDate(m.date))}<br>Subject: ${escapeHtml(m.subject)}<br>To: ${escapeHtml(m.to.map(formatAddress).join(', '))}</p>`
      : `<p>On ${escapeHtml(longDate(m.date))}, ${escapeHtml(displayName(m.from))} wrote:</p>`
    const sig = accounts.accountMap.get(m.accountId)?.signature ?? ''
    const html = `<p></p>${signatureBlock(sig)}<p></p>${header}<blockquote>${await quotedBody(m)}</blockquote>`
    return create({
      mode, accountId: m.accountId, to, cc, showCc: cc.length > 0,
      subject: prefixSubject(mode === 'forward' ? 'Fwd' : 'Re', m.subject), html, replyToId: m.id,
    })
  }

  /** Continue editing a draft from the Drafts folder. */
  function editDraft(m: Message) {
    const existing = drafts.value.find((d) => d.remoteId === m.id)
    if (existing) return focus(existing.id)
    create({
      mode: 'new', accountId: m.accountId, to: m.to, cc: m.cc, bcc: m.bcc, showCc: m.cc.length + m.bcc.length > 0,
      subject: m.subject === '(no subject)' ? '' : m.subject, html: m.bodyHtml || textToHtml(m.bodyText), remoteId: m.id, remoteAccountId: m.accountId,
    })
  }

  function close(id: string) {
    drafts.value = drafts.value.filter((d) => d.id !== id)
  }

  /** Close the window: keep the draft on the server when supported. Returns false if it must be confirmed. */
  async function saveAndClose(id: string): Promise<boolean> {
    const d = drafts.value.find((x) => x.id === id)
    if (!d) return true
    if (!draftHasContent(d)) {
      close(id)
      return true
    }
    if (!hasMethod('SaveDraft')) return false
    try {
      dropForeignRemote(d)
      const accountId = d.accountId
      d.remoteId = await api.saveDraft(toRequest(d), d.remoteId ?? '')
      d.remoteAccountId = accountId
      close(id)
      toast('Draft saved', { description: 'You can find it in Drafts.' })
      void useMailStore().softRefresh()
      return true
    } catch (e) {
      toast.error('Couldn’t save the draft', { description: errorMessage(e) })
      return false
    }
  }

  /** Discard: also remove the server copy. */
  function discard(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    const t = remoteTimers.get(id)
    if (t) clearTimeout(t)
    if (d?.remoteId && hasMethod('DeleteDraft')) void api.deleteDraft(d.remoteId).catch(() => {})
    close(id)
  }

  function focus(id: string) {
    for (const d of drafts.value) if (d.id === id) d.minimized = false
  }

  function toRequest(d: Draft) {
    return {
      accountId: d.accountId, to: d.to, cc: d.cc, bcc: d.bcc, subject: d.subject,
      bodyHtml: d.html, bodyText: htmlToText(d.html), replyToId: d.replyToId, mode: d.mode,
      attachments: d.attachments.map(({ filename, mimeType, dataBase64 }) => ({ filename, mimeType, dataBase64 })),
      draftId: d.remoteId ?? '',
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

  async function send(id: string, delaySeconds = 0) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d || d.sending || d.queued) return
    if (!d.to.length && !d.cc.length && !d.bcc.length) {
      toast.error('Add at least one recipient')
      return
    }
    const invalid = [...d.to, ...d.cc, ...d.bcc].filter((a) => !isEmail(a.email))
    if (invalid.length) {
      toast.error(`Check ${invalid.length === 1 ? 'this address' : 'these addresses'}: ${invalid.map((a) => a.email).join(', ')}`)
      return
    }
    if (delaySeconds > 0) {
      d.queued = true
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

  return { drafts, compose, composeWith, respond, editDraft, close, discard, saveAndClose, focus, send, undoSend }
})
