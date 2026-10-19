import { describe, expect, it } from 'vitest'
import { htmlHasText, initials, parseMailto, signatureBlock } from '@/lib/format'

describe('initials', () => {
  it('uses first and last word letters', () => {
    expect(initials('Priya Raman')).toBe('PR')
    expect(initials('Anna Maria van der Berg')).toBe('AB')
  })
  it('ignores punctuation and parentheses', () => {
    expect(initials('Alex (Gmail)')).toBe('AG')
    expect(initials('"Mom"')).toBe('M')
  })
  it('is Unicode aware', () => {
    expect(initials('Zoë')).toBe('Z')
    expect(initials('Émile Ölund')).toBe('ÉÖ')
    expect(initials('李 小龙')).toBe('李小')
  })
  it('falls back to the email local part', () => {
    expect(initials('', 'billing.team@example.com')).toBe('BT')
    expect(initials('noreply@github.com')).toBe('N')
  })
  it('skips numbers-only words and returns ? when nothing is left', () => {
    expect(initials('123 456')).toBe('?')
    expect(initials('Team 42 Ops')).toBe('TO')
  })
})

describe('parseMailto', () => {
  it('parses recipients and headers', () => {
    const r = parseMailto('mailto:a@x.io,b@y.io?cc=c@z.io&subject=Hello%20there&body=Line%201%0ALine%202')
    expect(r.to.map((a) => a.email)).toEqual(['a@x.io', 'b@y.io'])
    expect(r.cc.map((a) => a.email)).toEqual(['c@z.io'])
    expect(r.subject).toBe('Hello there')
    expect(r.body).toBe('Line 1\nLine 2')
  })
  it('handles encoded names, + as space and bad escapes', () => {
    const r = parseMailto('mailto:%22Jane%20Doe%22%20%3Cjane@x.io%3E?subject=a+b&body=100%')
    expect(r.to[0]).toEqual({ name: 'Jane Doe', email: 'jane@x.io' })
    expect(r.subject).toBe('a b')
    expect(r.body).toBe('100%')
  })
  it('drops invalid addresses', () => {
    expect(parseMailto('mailto:not-an-address').to).toEqual([])
  })
})

describe('htmlHasText', () => {
  it('detects visible text and images', () => {
    expect(htmlHasText('<p></p>')).toBe(false)
    expect(htmlHasText('<p> hi </p>')).toBe(true)
    expect(htmlHasText('<p><img src="data:x"></p>')).toBe(true)
  })
  it('can ignore the signature block', () => {
    const html = `<p></p>${signatureBlock('<p>Alex</p>')}`
    expect(htmlHasText(html)).toBe(true)
    expect(htmlHasText(html, true)).toBe(false)
  })
})
