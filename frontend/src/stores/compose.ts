import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage } from '@/lib/api'
import { displayName, escapeHtml, formatAddress, htmlToText, longDate, textToHtml } from '@/lib/format'
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
}

const DRAFTS_KEY = 'gomail.drafts.v1'
const MAX_PERSIST_ATTACH = 1_500_000

function loadDrafts(): Draft[] {
  try {
    const list = JSON.parse(localStorage.getItem(DRAFTS_KEY) || '[]') as Draft[]
    // Restore minimised; anything that was mid-send becomes a normal draft again.
    return list.map((d) => ({ ...d, minimized: true, expanded: false, sending: false, queued: false }))
  } catch {
    return []
  }
}

export function draftHasContent(d: Draft): boolean {
  return !!(d.to.length || d.cc.length || d.bcc.length || d.subject.trim() || htmlToText(d.html).trim() || d.attachments.length)
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
  watch(drafts, () => {
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(persist, 600)
  }, { deep: true })

  function persist() {
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

  async function respond(mode: Exclude<ComposeMode, 'new'>, m: Message) {
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
    const html = `<p></p><p></p>${header}<blockquote>${await quotedBody(m)}</blockquote>`
    return create({
      mode, accountId: m.accountId, to, cc, showCc: cc.length > 0,
      subject: prefixSubject(mode === 'forward' ? 'Fwd' : 'Re', m.subject), html, replyToId: m.id,
    })
  }

  function close(id: string) {
    drafts.value = drafts.value.filter((d) => d.id !== id)
  }

  function focus(id: string) {
    for (const d of drafts.value) if (d.id === id) d.minimized = false
  }

  async function deliver(d: Draft) {
    await api.sendMessage({
      accountId: d.accountId, to: d.to, cc: d.cc, bcc: d.bcc, subject: d.subject,
      bodyHtml: d.html, bodyText: htmlToText(d.html), replyToId: d.replyToId, mode: d.mode,
      attachments: d.attachments.map(({ filename, mimeType, dataBase64 }) => ({ filename, mimeType, dataBase64 })),
    })
  }

  async function send(id: string, delaySeconds = 0) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d || d.sending || d.queued) return
    if (!d.to.length && !d.cc.length && !d.bcc.length) {
      toast.error('Add at least one recipient')
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

  return { drafts, compose, respond, close, focus, send, undoSend }
})
