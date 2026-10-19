import type { Address } from './types'

const timeFmt = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })
const weekdayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short' })
const monthDayFmt = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' })
const fullDateFmt = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
const longFmt = new Intl.DateTimeFormat(undefined, {
  weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
})

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
}

/** Compact list date: time today, "Yesterday", weekday this week, "Mar 4", or full date. */
export function listDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const days = Math.round((startOfDay(now) - startOfDay(d)) / 86400000)
  if (days <= 0) return timeFmt.format(d)
  if (days === 1) return 'Yesterday'
  if (days < 7) return weekdayFmt.format(d)
  if (d.getFullYear() === now.getFullYear()) return monthDayFmt.format(d)
  return fullDateFmt.format(d)
}

export function longDate(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : longFmt.format(d)
}

export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return 'never'
  const d = new Date(iso).getTime()
  if (Number.isNaN(d)) return 'never'
  const s = Math.round((Date.now() - d) / 1000)
  if (s < 45) return 'just now'
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} h ago`
  return listDate(new Date(d).toISOString())
}

export function displayName(a: Address | undefined | null): string {
  if (!a) return ''
  return a.name?.trim() || a.email
}

export function formatAddress(a: Address): string {
  return a.name ? `${a.name} <${a.email}>` : a.email
}

export function initials(nameOrEmail: string, email = ''): string {
  // Letters only (Unicode-aware): "Alex (Gmail)" -> "AG", "Zoë" -> "Z"; fall back to the email local part.
  const words = (s: string) => (s.replace(/<.*>/, '').split('@')[0].match(/[\p{L}\p{N}]+/gu) ?? []).filter((w) => /\p{L}/u.test(w))
  let parts = words(nameOrEmail || '')
  if (!parts.length && email) parts = words(email)
  if (!parts.length) return '?'
  const first = (w: string) => (w.match(/\p{L}/u)?.[0] ?? '').toUpperCase()
  if (parts.length === 1) return first(parts[0])
  return first(parts[0]) + first(parts[parts.length - 1])
}

export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

export function formatPrice(perMillion: number): string {
  if (!perMillion) return 'free'
  if (perMillion < 0.01) return `$${perMillion.toFixed(4)}`
  if (perMillion < 1) return `$${perMillion.toFixed(3)}`
  return `$${perMillion.toFixed(2)}`
}

export function formatContext(n: number): string {
  if (!n) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0)}M`
  return `${Math.round(n / 1000)}K`
}

export function formatUSD(n: number | undefined): string {
  if (!n) return '$0.00'
  if (n < 0.01) return `$${n.toFixed(4)}`
  return `$${n.toFixed(2)}`
}

/** Deterministic pleasant color for an address without an account color. */
export function colorForString(s: string): string {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return `hsl(${h % 360} 45% 52%)`
}

export function parseAddressList(input: string): Address[] {
  return input
    .split(/[,;\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map(parseAddress)
    .filter((a): a is Address => !!a)
}

export function parseAddress(s: string): Address | null {
  const m = s.match(/^\s*"?([^"<]*?)"?\s*<([^>]+)>\s*$/)
  if (m) return isEmail(m[2]) ? { name: m[1].trim(), email: m[2].trim() } : null
  return isEmail(s.trim()) ? { name: '', email: s.trim() } : null
}

export function isEmail(s: string): boolean {
  return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(s)
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string)
}

const URL_RE = /\b((?:https?:\/\/|www\.)[^\s<>"']+[^\s<>"'.,;:!?)\]])|\b([A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,})\b/gi

/** Escape plain text and turn URLs / email addresses into anchors. */
export function linkify(text: string): string {
  let out = ''
  let last = 0
  for (const m of text.matchAll(URL_RE)) {
    const idx = m.index ?? 0
    out += escapeHtml(text.slice(last, idx))
    if (m[1]) {
      const href = m[1].startsWith('www.') ? `https://${m[1]}` : m[1]
      out += `<a href="${escapeHtml(href)}">${escapeHtml(m[1])}</a>`
    } else {
      out += `<a href="mailto:${escapeHtml(m[2])}">${escapeHtml(m[2])}</a>`
    }
    last = idx + m[0].length
  }
  out += escapeHtml(text.slice(last))
  return out
}

export function htmlToText(html: string): string {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  doc.querySelectorAll('br').forEach((br) => br.replaceWith('\n'))
  doc.querySelectorAll('p,div,li,h1,h2,h3,h4,blockquote,tr').forEach((el) => el.append('\n'))
  return (doc.body.textContent ?? '').replace(/\n{3,}/g, '\n\n').trim()
}

export function textToHtml(text: string): string {
  return text
    .split(/\n{2,}/)
    .map((p) => `<p>${escapeHtml(p).replace(/\n/g, '<br>')}</p>`)
    .join('')
}
