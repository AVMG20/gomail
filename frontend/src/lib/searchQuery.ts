// Search query language shared by the palette (completions) and the browser mock (parsing):
// free terms, "quoted phrases" and operators from: to: subject: label: in: is: has: before: after:.
import { fuzzyFilter } from './fuzzy'

export interface ParsedSearch {
  terms: string[]
  phrases: string[]
  from: string[]
  to: string[]
  subject: string[]
  labels: string[]
  inKind: string
  unread?: boolean
  starred?: boolean
  hasAttachment?: boolean
  before?: number
  after?: number
}

export function parseSearch(q: string): ParsedSearch {
  const out: ParsedSearch = { terms: [], phrases: [], from: [], to: [], subject: [], labels: [], inKind: '' }
  const re = /(\w+):("([^"]*)"|\S+)|"([^"]*)"|(\S+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(q))) {
    if (m[1]) {
      const key = m[1].toLowerCase()
      const val = (m[3] ?? m[2]).toLowerCase()
      if (key === 'from') out.from.push(val)
      else if (key === 'to') out.to.push(val)
      else if (key === 'subject') out.subject.push(val)
      else if (key === 'label') out.labels.push(val)
      else if (key === 'in') out.inKind = val
      else if (key === 'is' && val === 'unread') out.unread = true
      else if (key === 'is' && val === 'read') out.unread = false
      else if (key === 'is' && val === 'starred') out.starred = true
      else if (key === 'has' && val.startsWith('attach')) out.hasAttachment = true
      else if (key === 'before') out.before = Date.parse(val)
      else if (key === 'after') out.after = Date.parse(val)
      else out.terms.push(`${key}:${val}`)
    } else if (m[4] !== undefined) {
      if (m[4]) out.phrases.push(m[4].toLowerCase())
    } else if (m[5]) {
      out.terms.push(m[5].toLowerCase())
    }
  }
  return out
}

export interface Completion { value: string; label: string; hint?: string; color?: string }

export const OPERATOR_STARTERS = ['from:', 'label:', 'is:unread', 'has:attachment', 'in:']
const OPERATOR_VALUES: Record<string, string[]> = {
  is: ['unread', 'read', 'starred'],
  has: ['attachment'],
  in: ['inbox', 'sent', 'drafts', 'archive', 'spam', 'trash', 'starred'],
}

/** The token being typed at the end of the query ("" after a space). */
export function lastToken(query: string): string {
  const m = query.match(/(\S*)$/)
  return m ? m[1] : ''
}

/** Completions for the last token: operator names, label names, senders, or fixed values. */
export function operatorCompletions(
  query: string,
  sources: { labels: { name: string; color?: string }[]; contacts: { name: string; email: string }[] },
): Completion[] {
  const tok = lastToken(query).toLowerCase()
  const m = tok.match(/^(label|from|is|has|in):(.*)$/)
  if (!m) {
    if (tok.length < 2) return []
    return OPERATOR_STARTERS.filter((o) => o.startsWith(tok) && o !== tok).map((o) => ({ value: o, label: o }))
  }
  const [, op, rest] = m
  if (op === 'label') {
    return fuzzyFilter(rest.replace(/^"/, ''), sources.labels, (l) => l.name, 8).map((l) => ({ value: `label:${l.name.includes(' ') ? `"${l.name}"` : l.name}`, label: l.name, color: l.color }))
  }
  if (op === 'from') {
    // Prefer real substring matches; scattered-letter fuzzy hits only when nothing contains the text.
    const key = (a: { name: string; email: string }) => `${a.name} ${a.email}`
    const exact = rest ? sources.contacts.filter((a) => key(a).toLowerCase().includes(rest)) : sources.contacts
    return fuzzyFilter(rest, exact.length ? exact : sources.contacts, key, 8).map((a) => ({ value: `from:${a.email}`, label: a.name || a.email, hint: a.name ? a.email : '' }))
  }
  return (OPERATOR_VALUES[op] ?? []).filter((o) => o.startsWith(rest) && o !== rest).map((o) => ({ value: `${op}:${o}`, label: `${op}:${o}` }))
}

/** Replace the last token with a completion (and add a trailing space). */
export function applyCompletion(query: string, value: string): string {
  return query.replace(/(\S*)$/, `${value} `)
}
