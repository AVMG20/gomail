import { defineStore } from 'pinia'
import { ref } from 'vue'
import DOMPurify from 'dompurify'
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
  /** Bumped when html is replaced programmatically (so the editor reloads). */
  version: number
}

function prefixSubject(prefix: string, subject: string) {
  const re = new RegExp(`^(${prefix}|re|fwd?|aw|sv)\\s*:\\s*`, 'i')
  return re.test(subject) && subject.toLowerCase().startsWith(prefix.toLowerCase()) ? subject : `${prefix}: ${subject.replace(re, '')}`
}

function quotedBody(m: Message): string {
  const body = m.bodyHtml
    ? DOMPurify.sanitize(m.bodyHtml, { FORBID_TAGS: ['style', 'img', 'script'], FORBID_ATTR: ['style', 'class'] })
    : textToHtml(m.bodyText)
  return body
}

export const useComposeStore = defineStore('compose', () => {
  const drafts = ref<Draft[]>([])

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
    // Keep at most three windows; minimise older ones.
    drafts.value.forEach((x) => (x.minimized = true))
    drafts.value = [...drafts.value, d].slice(-3)
    return d
  }

  function compose(to: Address[] = []) {
    return create({ to })
  }

  function respond(mode: Exclude<ComposeMode, 'new'>, m: Message) {
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
    const html = `<p></p><p></p>${header}<blockquote>${quotedBody(m)}</blockquote>`
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

  async function send(id: string) {
    const d = drafts.value.find((x) => x.id === id)
    if (!d || d.sending) return
    if (!d.to.length && !d.cc.length && !d.bcc.length) {
      toast.error('Add at least one recipient')
      return
    }
    d.sending = true
    try {
      await api.sendMessage({
        accountId: d.accountId, to: d.to, cc: d.cc, bcc: d.bcc, subject: d.subject,
        bodyHtml: d.html, bodyText: htmlToText(d.html), replyToId: d.replyToId, mode: d.mode,
        attachments: d.attachments.map(({ filename, mimeType, dataBase64 }) => ({ filename, mimeType, dataBase64 })),
      })
      close(id)
      toast.success('Message sent')
      void useMailStore().softRefresh()
    } catch (e) {
      d.sending = false
      toast.error('Could not send', { description: errorMessage(e) })
    }
  }

  return { drafts, compose, respond, close, focus, send }
})
