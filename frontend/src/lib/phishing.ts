// Quick phishing checks for mail that is already in Junk. Pure functions (unit tested).
//
// Built to rarely misfire: every check looks for something a real sender can't or
// wouldn't do (an address that isn't a valid address, a random-character domain, your
// own username at someone else's domain, hidden characters in the name, links to bare
// IP addresses). Softer hints, like a brand in the name or a link whose text shows
// another site, are left out: legit newsletters do those all the time.

export interface PhishingCheck {
  suspicious: boolean
  /** Plain-language reasons, one per check that fired. */
  reasons: string[]
}

interface Checked {
  from: { name?: string; email: string }
  labels?: readonly string[]
  bodyHtml?: string
  bodyText?: string
}

/** Only mail in Junk is checked (the provider's Spam folder, or labeled Junk). */
export function isJunk(m: { labels?: readonly string[] }): boolean {
  return !!m.labels?.includes('junk')
}

// Two-letter country codes that sell names one level down (example.co.uk, example.com.au).
const SECOND_LEVEL = new Set(['co', 'com', 'net', 'org', 'gov', 'edu', 'ac', 'or', 'ne', 'go', 'ltd', 'plc'])

/** "mail.news.example.co.uk" → "example.co.uk": the part of a host someone registered. */
export function siteOf(host: string): string {
  const labels = host.toLowerCase().replace(/\.$/, '').split('.')
  if (labels.length >= 3 && labels.at(-1)!.length === 2 && SECOND_LEVEL.has(labels.at(-2)!)) return labels.slice(-3).join('.')
  return labels.slice(-2).join('.')
}

const LABEL = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i
const TLD = /^(?:[a-z]{2,24}|xn--[a-z0-9-]{1,59})$/i

/** The domain can't be a real one: styled look-alike letters (𝗔𝗻𝘄𝗯), labels that start
 *  or end with "-", an empty label, or a made-up ending (".nl-winner-4452…"). */
export function malformedDomain(domain: string): boolean {
  if (!domain) return true
  // Real international domains are already in normal form; styled letters are not.
  if (domain.normalize('NFKC') !== domain) return true
  if (/[^\x21-\x7e]/.test(domain)) return false // international (unicode) domain: don't judge its labels
  const labels = domain.replace(/\.$/, '').split('.')
  if (labels.length < 2) return true
  return !labels.every((l) => LABEL.test(l)) || !TLD.test(labels.at(-1)!)
}

/** A registered name made of random characters, like "90lluqjtuph21rvyrn6vr68xryfh".
 *  Long, with several digits mixed in between the letters: real brands don't look like that. */
export function randomName(label: string): boolean {
  const s = label.toLowerCase()
  if (s.length < 16 || !/^[a-z0-9]+$/.test(s)) return false
  const digits = s.replace(/[^0-9]/g, '').length
  const letters = s.length - digits
  let switches = 0
  for (let i = 1; i < s.length; i++) if (/\d/.test(s[i]) !== /\d/.test(s[i - 1])) switches++
  return digits >= 3 && letters >= 6 && switches >= 4
}

// Characters nobody types in a name; spammers use them to slip past filters.
// (Not U+200C/U+200D: those join letters and emoji in real names.)
const INVISIBLE = /[​⁠﻿­᠎]/

/** Links to a bare IP address (http://185.12.4.9/login): real companies link to their domain. */
export function ipLinks(html: string, text = ''): number {
  return (html || text).match(/\bhttps?:\/\/(?:\d{1,3}\.){3}\d{1,3}(?=[:/?#"'\s<>]|$)/gi)?.length ?? 0
}

/** Runs the checks. own: the mailbox's address (to spot your username at another domain);
 *  mine: all your addresses (your own mail never counts as that trick). */
export function checkPhishing(m: Checked, own = '', mine: readonly string[] = []): PhishingCheck {
  const reasons: string[] = []
  if (!isJunk(m)) return { suspicious: false, reasons }
  const email = (m.from?.email ?? '').trim()
  const at = email.lastIndexOf('@')
  const local = at > 0 ? email.slice(0, at) : ''
  const domain = at > 0 ? email.slice(at + 1) : ''

  if (email && malformedDomain(domain)) {
    reasons.push(`The sender’s address (${email}) isn’t a real address. Its domain can’t exist.`)
  } else if (domain && randomName(siteOf(domain).split('.')[0])) {
    reasons.push(`The sender’s domain (${siteOf(domain)}) is a string of random characters, not a company name.`)
  }

  const ownAt = own.lastIndexOf('@')
  const ownLocal = ownAt > 0 ? own.slice(0, ownAt).toLowerCase() : ''
  const ownDomain = ownAt > 0 ? own.slice(ownAt + 1).toLowerCase() : ''
  const fromMe = mine.some((a) => a.toLowerCase() === email.toLowerCase())
  if (ownLocal.length >= 6 && !fromMe && local.toLowerCase() === ownLocal && domain.toLowerCase() !== ownDomain) {
    reasons.push(`It’s sent from your own username at another domain (${email}), a trick to look familiar.`)
  }

  if (INVISIBLE.test(m.from?.name ?? '')) {
    reasons.push('The sender’s name contains hidden characters, a trick to get past spam filters.')
  }

  if (ipLinks(m.bodyHtml ?? '', m.bodyText ?? '') > 0) {
    reasons.push('It links to a bare IP address instead of a website name.')
  }

  return { suspicious: reasons.length > 0, reasons }
}
