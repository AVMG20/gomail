import { describe, expect, it } from 'vitest'
import {
  decideLink, domainTrusted, needsRemoteRefetch, normalizeDomain, normalizeDomains, normalizeSenders, remoteAllowedFor,
  remoteImagesTrusted, splitUrl, textHost,
} from '@/lib/trust'
import { normSettings } from '@/lib/api'
import type { Settings } from '@/lib/types'

describe('link decisions', () => {
  it('asks before opening an untrusted web link', () => {
    const d = decideLink('https://shop.example.com/deal?id=1', 'See the deal', [])
    expect(d).toEqual({ kind: 'confirm', url: 'https://shop.example.com/deal?id=1', host: 'shop.example.com', textHost: '', mismatch: false, lookalike: false })
  })

  it('opens trusted domains (and their subdomains) without asking', () => {
    expect(decideLink('https://www.example.com/a', '', ['example.com'])).toMatchObject({ kind: 'open', host: 'example.com' })
    expect(decideLink('https://news.example.com/a', '', ['example.com'])).toMatchObject({ kind: 'open' })
    // A look-alike suffix is not a subdomain.
    expect(decideLink('https://evilexample.com/a', '', ['example.com'])).toMatchObject({ kind: 'confirm' })
    expect(decideLink('https://example.com.evil.test/a', '', ['example.com'])).toMatchObject({ kind: 'confirm' })
  })

  it('keeps mailto in the composer and ignores in-page anchors', () => {
    expect(decideLink('mailto:sam@example.com?subject=Hi', 'Mail Sam', [])).toEqual({ kind: 'mailto', href: 'mailto:sam@example.com?subject=Hi' })
    expect(decideLink('#section-2', '', []).kind).toBe('ignore')
    expect(decideLink('', '', []).kind).toBe('ignore')
  })

  it('blocks dangerous and unknown schemes', () => {
    for (const href of ['javascript:alert(1)', 'JAVASCRIPT:alert(1)', 'file:///etc/passwd', 'data:text/html,<b>x</b>', 'vbscript:x', 'ms-settings:', 'about:blank', 'https://', '//example.com/x', 'ftp://example.com']) {
      expect(decideLink(href, '', ['example.com']).kind, href).toBe('blocked')
    }
  })

  it('warns when the link text shows another domain', () => {
    const d = decideLink('https://login.evil.test/x', 'www.bank.example', [])
    expect(d).toMatchObject({ kind: 'confirm', host: 'login.evil.test', textHost: 'bank.example', mismatch: true })
    expect(decideLink('https://track.mailer.test/r?u=1', 'https://example.com/offer', [])).toMatchObject({ mismatch: true, textHost: 'example.com' })
    // Same site (or a subdomain of it) is fine.
    expect(decideLink('https://www.example.com/offer', 'example.com', [])).toMatchObject({ mismatch: false })
    expect(decideLink('https://go.example.com/offer', 'https://example.com/offer', [])).toMatchObject({ mismatch: false })
    // Ordinary text is no domain.
    expect(decideLink('https://example.com', 'Read more.', [])).toMatchObject({ mismatch: false, textHost: '' })
  })

  it('shows the real host for user-info tricks and punycode', () => {
    const d = decideLink('https://bank.example@evil.test/login', 'bank.example', [])
    expect(d).toMatchObject({ kind: 'confirm', host: 'evil.test', mismatch: true })
    expect(decideLink('https://аpple.example/', '', [])).toMatchObject({ kind: 'confirm', lookalike: true })
  })

  it('splits a URL to highlight its host', () => {
    expect(splitUrl('https://www.example.com/a?b=1')).toEqual({ prefix: 'https://', host: 'www.example.com', rest: '/a?b=1' })
    const p = splitUrl('https://bank.example@evil.test/x')
    expect(p.host).toBe('evil.test')
    expect(p.prefix + p.host + p.rest).toBe('https://bank.example@evil.test/x')
  })

  it('detects domains in link text', () => {
    expect(textHost('www.Example.com')).toBe('example.com')
    expect(textHost('https://example.com/path')).toBe('example.com')
    expect(textHost('example.com.')).toBe('example.com')
    expect(textHost('Click here')).toBe('')
    expect(textHost('sam@example.com')).toBe('')
  })

  it('normalizes and matches trusted domains', () => {
    expect(normalizeDomain(' https://WWW.Example.com/x ')).toBe('example.com')
    expect(normalizeDomain('*.example.com')).toBe('example.com')
    expect(normalizeDomain('not a domain')).toBe('')
    expect(normalizeDomains(['Example.com', 'www.example.com', '', 'news.example.org', 42])).toEqual(['example.com', 'news.example.org', '42'])
    expect(normalizeDomains(null)).toEqual([])
    expect(domainTrusted('www.example.com', ['example.com'])).toBe(true)
    expect(domainTrusted('example.com', ['news.example.com'])).toBe(false)
    expect(domainTrusted('example.com', undefined)).toBe(false)
  })
})

describe('remote image decisions', () => {
  const msg = (email: string, threadId = 't1') => ({ threadId, from: { name: '', email } })

  it('trusts senders case-insensitively', () => {
    expect(remoteImagesTrusted(['news@example.com'], 'News@Example.COM')).toBe(true)
    expect(remoteImagesTrusted(['news@example.com'], 'other@example.com')).toBe(false)
    expect(remoteImagesTrusted(undefined, 'news@example.com')).toBe(false)
    expect(normalizeSenders([' Sam@Example.com', 'sam@example.com', 'nope', null])).toEqual(['sam@example.com'])
  })

  it('allows by global switch, trusted sender or loaded conversation', () => {
    expect(remoteAllowedFor(msg('a@example.com'), {})).toBe(false)
    expect(remoteAllowedFor(msg('a@example.com'), { showRemoteImages: true })).toBe(true)
    expect(remoteAllowedFor(msg('a@example.com'), { trustedImageSenders: ['a@example.com'] })).toBe(true)
    expect(remoteAllowedFor(msg('a@example.com', 't9'), { allowedThreads: new Set(['t9']) })).toBe(true)
    expect(remoteAllowedFor(msg('a@example.com', 't9'), { allowedThreads: new Set(['t1']) })).toBe(false)
  })

  it('refetches stripped bodies only once images are allowed', () => {
    expect(needsRemoteRefetch({ remoteContentBlocked: true }, false)).toBe(false)
    expect(needsRemoteRefetch({ remoteContentBlocked: true }, true)).toBe(true)
    // The backend already kept them (trusted sender / global switch).
    expect(needsRemoteRefetch({ remoteContentBlocked: false, hasRemoteContent: true }, true)).toBe(false)
    // Older backends don't report blocking.
    expect(needsRemoteRefetch({ hasRemoteContent: true }, true)).toBe(true)
  })

  it('normalizes the trusted lists in settings', () => {
    const s = normSettings({ trustedLinkDomains: ['WWW.Example.com', 'example.com'], trustedImageSenders: null } as unknown as Settings)
    expect(s.trustedLinkDomains).toEqual(['example.com'])
    expect(s.trustedImageSenders).toEqual([])
  })
})
