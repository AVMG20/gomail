// Trust decisions for email content: which links open without asking and
// which senders may load remote images. Pure functions (unit tested).
import { isSafeExternalUrl } from './format'

const stripWww = (h: string) => h.replace(/^www\./, '')
const HOST_RE = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

/** "https://www.Example.com/x", "*.example.com" or "example.com." → "example.com"; "" when it is no host name. */
export function normalizeDomain(input: string): string {
  let s = (input ?? '').trim().toLowerCase()
  if (!s) return ''
  if (/^[a-z][a-z0-9+.-]*:\/\//.test(s)) {
    try {
      s = new URL(s).hostname
    } catch {
      return ''
    }
  }
  s = s.split(/[/?#]/)[0].replace(/:\d+$/, '').replace(/^\*\./, '').replace(/\.$/, '')
  s = stripWww(s)
  return HOST_RE.test(s) ? s : ''
}

/** Valid, unique domains (first occurrence wins). */
export function normalizeDomains(list: unknown): string[] {
  if (!Array.isArray(list)) return []
  return [...new Set(list.map((d) => normalizeDomain(String(d ?? ''))).filter(Boolean))]
}

/** The host of an http(s) URL in its real (punycode) form, without "www."; "" when invalid. */
export function linkHost(url: string): string {
  if (!isSafeExternalUrl(url)) return ''
  try {
    return stripWww(new URL(url.trim()).hostname.toLowerCase())
  } catch {
    return ''
  }
}

/** host is a trusted domain or one of its subdomains. */
export function domainTrusted(host: string, trusted: readonly string[] | undefined): boolean {
  const h = stripWww((host ?? '').toLowerCase())
  if (!h || !trusted?.length) return false
  return trusted.some((d) => !!d && (h === d || h.endsWith(`.${d}`)))
}

/** Same site: equal, or one is a subdomain of the other. */
export function hostsMatch(a: string, b: string): boolean {
  const x = stripWww(a.toLowerCase())
  const y = stripWww(b.toLowerCase())
  return x === y || x.endsWith(`.${y}`) || y.endsWith(`.${x}`)
}

const URLISH = /^(?:https?:\/\/)?(?:[a-z0-9-]+\.)+[a-z]{2,}(?::\d+)?(?:[/?#]\S*)?$/i

/** When the visible link text itself looks like an address ("www.bank.com", "https://…"), its host. */
export function textHost(text: string): string {
  const t = (text ?? '').trim().replace(/[.,;:!?)]+$/, '')
  if (!t || /\s/.test(t) || !URLISH.test(t) || /^[^/]*@/.test(t)) return ''
  try {
    return stripWww(new URL(/^https?:\/\//i.test(t) ? t : `https://${t}`).hostname.toLowerCase())
  } catch {
    return ''
  }
}

/** Splits a URL so the host can be highlighted: prefix + host + rest === url. */
export function splitUrl(url: string): { prefix: string; host: string; rest: string } {
  try {
    const u = new URL(url)
    const auth = u.username ? `${u.username}${u.password ? `:${u.password}` : ''}@` : ''
    const prefix = `${u.protocol}//${auth}`
    if (u.href.startsWith(prefix + u.host)) return { prefix, host: u.host, rest: u.href.slice(prefix.length + u.host.length) }
  } catch {
    /* fall through */
  }
  return { prefix: '', host: '', rest: url }
}

export type LinkDecision =
  /** Empty or in-page (#…) link: nothing to do. */
  | { kind: 'ignore' }
  | { kind: 'mailto'; href: string }
  /** javascript:, file:, data:, custom schemes, malformed URLs. */
  | { kind: 'blocked'; href: string }
  /** Trusted domain: open in the browser without asking. */
  | { kind: 'open'; url: string; host: string }
  /** Ask first. mismatch: the link text shows another site (textHost). */
  | { kind: 'confirm'; url: string; host: string; textHost: string; mismatch: boolean; lookalike: boolean }

/** What clicking a link in an email does. */
export function decideLink(href: string, text: string, trusted: readonly string[] | undefined): LinkDecision {
  const raw = (href ?? '').trim()
  if (!raw || raw.startsWith('#')) return { kind: 'ignore' }
  if (/^mailto:/i.test(raw)) return { kind: 'mailto', href: raw }
  if (!/^https?:\/\//i.test(raw) || !isSafeExternalUrl(raw)) return { kind: 'blocked', href: raw }
  const url = new URL(raw).href
  const host = linkHost(url)
  if (!host) return { kind: 'blocked', href: raw }
  if (domainTrusted(host, trusted)) return { kind: 'open', url, host }
  const shown = textHost(text)
  return {
    kind: 'confirm', url, host, textHost: shown,
    mismatch: !!shown && !hostsMatch(shown, host),
    lookalike: host.split('.').some((l) => l.startsWith('xn--')),
  }
}

/** Lowercased email address; "" when it doesn't look like one. */
export function normalizeSender(email: string): string {
  const e = (email ?? '').trim().toLowerCase()
  const at = e.lastIndexOf('@')
  return at > 0 && at < e.length - 1 && !/\s/.test(e) ? e : ''
}

/** Valid, unique sender addresses (first occurrence wins). */
export function normalizeSenders(list: unknown): string[] {
  if (!Array.isArray(list)) return []
  return [...new Set(list.map((e) => normalizeSender(String(e ?? ''))).filter(Boolean))]
}

/** The sender is on the "always load images" list. */
export function remoteImagesTrusted(trusted: readonly string[] | undefined, email: string): boolean {
  const e = normalizeSender(email)
  return !!e && !!trusted?.some((t) => t === e)
}

/** Remote images are shown for this message: the global switch, a trusted sender, or "Load images" for its conversation. */
export function remoteAllowedFor(
  m: { threadId: string; from: { email: string } },
  opts: { showRemoteImages?: boolean; trustedImageSenders?: readonly string[]; allowedThreads?: ReadonlySet<string> },
): boolean {
  return !!opts.showRemoteImages || remoteImagesTrusted(opts.trustedImageSenders, m.from?.email ?? '') || !!opts.allowedThreads?.has(m.threadId)
}

/** Messages whose body the backend stripped although images are now allowed: fetch them again. */
export function needsRemoteRefetch<T extends { remoteContentBlocked?: boolean; hasRemoteContent?: boolean }>(m: T, allowed: boolean): boolean {
  return allowed && (!!m.remoteContentBlocked || (m.remoteContentBlocked === undefined && !!m.hasRemoteContent))
}
