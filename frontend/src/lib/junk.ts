// Sender rules from the mail ("Always send <sender> to Junk", "Always label <sender> as …",
// "Not junk"), shared by the list menu, the reading pane and the bulk bar.
import type { LabelRule } from './types'

/** A sender rule of a mailbox (see internal/core/junk.go). */
export interface RuleRef {
  id: string
  accountId: string
  /** The address, or "@domain". */
  contains: string
  action: 'junk' | 'label'
  labelId: string
}
export interface JunkSenderResult {
  rule: RuleRef
  /** false: an existing rule already covered the sender. */
  created: boolean
  /** Messages moved to Junk. */
  count: number
  /** Inbox mail moved besides the picked messages (Undo: back to the inbox). */
  moved: string[]
  /** Messages that were unread (Undo: unread again). */
  unread: string[]
}
export interface LabelSenderResult {
  rule: RuleRef
  /** false: an existing rule already labeled the sender. */
  created: boolean
  /** Mail that got the label now (Undo takes it off). */
  labeled: string[]
}
export interface NotJunkResult {
  count: number
  /** Junk rules that still send these senders' new mail to Junk. */
  rules: RuleRef[]
}
export type SenderScope = 'address' | 'domain'

// Shared mail services: junking the whole domain would junk unrelated people.
// Keep in sync with freeMailDomains in internal/core/junk.go.
const FREE_MAIL = new Set([
  'gmail.com', 'googlemail.com', 'outlook.com', 'hotmail.com', 'live.com', 'msn.com',
  'yahoo.com', 'ymail.com', 'rocketmail.com', 'icloud.com', 'me.com', 'mac.com',
  'aol.com', 'proton.me', 'protonmail.com', 'pm.me', 'gmx.com', 'gmx.net', 'gmx.de',
  'mail.com', 'zoho.com', 'yandex.com', 'yandex.ru', 'mail.ru', 'fastmail.com',
  'tutanota.com', 'tuta.io', 'hey.com', 'web.de', 't-online.de', 'orange.fr',
  'free.fr', 'laposte.net', 'libero.it', 'qq.com', '163.com', '126.com',
  'ziggo.nl', 'kpnmail.nl', 'hetnet.nl', 'planet.nl', 'home.nl', 'xs4all.nl',
  'telenet.be', 'skynet.be',
])
const FREE_MAIL_PREFIXES = ['gmail.', 'hotmail.', 'outlook.', 'live.', 'yahoo.', 'msn.', 'windowslive.']

export function isFreeMailDomain(domain: string): boolean {
  const d = domain.trim().toLowerCase()
  return FREE_MAIL.has(d) || FREE_MAIL_PREFIXES.some((p) => d.startsWith(p))
}

/** The domain of an address ("" when it has none). */
export function domainOf(address: string): string {
  const a = address.trim().toLowerCase()
  const at = a.lastIndexOf('@')
  return at > 0 && at < a.length - 1 ? a.slice(at + 1) : ''
}

/** The sender's domain when "everything from <domain>" makes sense: not a shared mail service and
 *  not the mailbox's own domain (that would junk colleagues). */
export function junkableDomain(address: string, ownEmail = ''): string {
  const d = domainOf(address)
  if (!d || isFreeMailDomain(d) || domainOf(ownEmail) === d) return ''
  return d
}

/** How a rule's text reads in a toast: "alex@example.com" or "example.com". */
export function ruleTarget(contains: string): string {
  return contains.startsWith('@') ? contains.slice(1) : contains
}

/** One sender per mailbox among the picked rows (the bulk bar junks each). Rows without a valid
 *  address, and mail the mailbox sent itself, are left out. */
export function sendersOf(rows: { id: string; accountId: string; from: { email: string } }[], ownEmail: (accountId: string) => string): { accountId: string; address: string; ids: string[] }[] {
  const out = new Map<string, { accountId: string; address: string; ids: string[] }>()
  for (const r of rows) {
    const address = r.from.email.trim().toLowerCase()
    if (!domainOf(address) || address === ownEmail(r.accountId).toLowerCase()) continue
    const key = `${r.accountId}\n${address}`
    const cur = out.get(key)
    if (cur) cur.ids.push(r.id)
    else out.set(key, { accountId: r.accountId, address, ids: [r.id] })
  }
  return [...out.values()]
}

/** The mailbox's sender rules that match this sender (by address; a domain rule matches every
 *  address at the domain), like the backend's rule matching. Rules for every mailbox count too. */
export function rulesForSender(rules: LabelRule[] | undefined, accountId: string, address: string): LabelRule[] {
  const a = address.trim().toLowerCase()
  if (!a) return []
  return (rules ?? []).filter((r) => r.field === 'from' && r.contains.trim() && (!r.accountId || r.accountId === accountId) && a.includes(r.contains.trim().toLowerCase()))
}
