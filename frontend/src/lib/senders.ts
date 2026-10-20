// "Clean up by sender": who fills a mailbox's inbox, biggest first (see internal/core/senders.go).
import { domainOf, isFreeMailDomain, junkableDomain, rulesForSender, type SenderScope } from './junk'
import type { LabelRule } from './types'

/** One sender of a mailbox's folder: an address, or every address at a company domain. */
export interface SenderSummary {
  accountId: string
  /** The address, or "@domain" for a domain group. */
  key: string
  /** Lower-cased; for a domain group, the newest message's address. */
  address: string
  /** Display name of the newest message. */
  name: string
  domain: string
  /** Distinct addresses in the group (1 for an address). */
  addresses: number
  count: number
  unread: number
  /** Date of the newest message (ISO). */
  newest: string
  /** The newest message offers List-Unsubscribe. */
  unsubscribe: boolean
}

export type SenderGrouping = 'address' | 'domain'

/** Per-address counts of one mailbox (what the store aggregates). */
export interface SenderStat {
  accountId: string
  address: string
  name: string
  count: number
  unread: number
  newest: string
  unsubscribe: boolean
}

/** Groups per-address counts like the backend: per address, or per company domain (shared mail
 *  services and the mailbox's own domain stay per address), the most mail first, at most `limit`. */
export function groupSenders(stats: SenderStat[], by: SenderGrouping, ownEmail: (accountId: string) => string, limit = 200): SenderSummary[] {
  const groups = new Map<string, SenderSummary>()
  for (const st of stats) {
    const address = st.address.trim().toLowerCase()
    const domain = domainOf(address)
    const key = by === 'domain' && domain && !isFreeMailDomain(domain) && domain !== domainOf(ownEmail(st.accountId)) ? `@${domain}` : address
    const id = `${st.accountId}\n${key}`
    const g = groups.get(id)
    if (!g) {
      groups.set(id, { accountId: st.accountId, key, address, name: st.name, domain, addresses: 1, count: st.count, unread: st.unread, newest: st.newest, unsubscribe: st.unsubscribe })
      continue
    }
    g.addresses++
    g.count += st.count
    g.unread += st.unread
    if (st.newest > g.newest) Object.assign(g, { newest: st.newest, address, name: st.name, unsubscribe: st.unsubscribe })
  }
  return [...groups.values()]
    .sort((a, b) => b.count - a.count || (a.newest < b.newest ? 1 : a.newest > b.newest ? -1 : 0) || a.key.localeCompare(b.key) || a.accountId.localeCompare(b.accountId))
    .slice(0, limit)
}

/** Identifies a row across reloads ("Keep" hides it for the session). */
export function senderRowKey(s: Pick<SenderSummary, 'accountId' | 'key'>): string {
  return `${s.accountId}\n${s.key}`
}

/** A domain group's rules cover the whole domain; anything else is one address. A shared mail
 *  service or the mailbox's own domain is never junked as a whole. */
export function senderScope(s: SenderSummary, ownEmail = ''): SenderScope {
  return s.key.startsWith('@') && junkableDomain(s.address, ownEmail) ? 'domain' : 'address'
}

/** How the row is called: the name, or the domain for a domain group. */
export function senderTitle(s: SenderSummary): string {
  if (s.key.startsWith('@')) return s.domain
  return s.name.trim() || s.address
}

/** "214 mails · 180 unread · last 2 days ago". */
export function senderStats(s: SenderSummary, now = Date.now()): string {
  const parts = [`${s.count.toLocaleString()} ${s.count === 1 ? 'mail' : 'mails'}`]
  if (s.unread) parts.push(`${s.unread.toLocaleString()} unread`)
  const when = ago(s.newest, now)
  if (when) parts.push(`last ${when}`)
  return parts.join(' · ')
}

function ago(iso: string, now: number): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const days = Math.floor((now - t) / 86_400_000)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 14) return `${days} days ago`
  if (days < 60) return `${Math.floor(days / 7)} weeks ago`
  if (days < 730) return `${Math.floor(days / 30)} months ago`
  return `${Math.floor(days / 365)} years ago`
}

/** Rows matching a filter text (name, address or domain), without the ones dismissed. */
export function filterSenders(list: SenderSummary[], text: string, dismissed: ReadonlySet<string> = new Set()): SenderSummary[] {
  const q = text.trim().toLowerCase()
  return list.filter((s) => !dismissed.has(senderRowKey(s)) && (!q || s.name.toLowerCase().includes(q) || s.address.includes(q) || s.domain.includes(q)))
}

/** The mailbox's sender rules covering the whole row (a domain row: rules for the domain). */
export function senderRules(rules: LabelRule[] | undefined, s: SenderSummary): { junk: LabelRule[]; label: LabelRule[] } {
  const matching = rulesForSender(rules, s.accountId, s.key)
  return { junk: matching.filter((r) => r.action === 'junk'), label: matching.filter((r) => r.action === 'label') }
}

/** The list search that shows the row's mail (from: matches the address or the domain). */
export function senderSearch(s: SenderSummary): string {
  return `from:${s.key.startsWith('@') ? s.domain : s.address}`
}
