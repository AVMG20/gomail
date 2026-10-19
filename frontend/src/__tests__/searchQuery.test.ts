import { describe, expect, it } from 'vitest'
import { applyCompletion, lastToken, operatorCompletions, parseSearch } from '@/lib/searchQuery'

describe('parseSearch', () => {
  it('splits terms, phrases and operators', () => {
    const p = parseSearch('invoice "next week" from:Priya label:"Big Clients" is:unread has:attachments in:inbox')
    expect(p.terms).toEqual(['invoice'])
    expect(p.phrases).toEqual(['next week'])
    expect(p.from).toEqual(['priya'])
    expect(p.labels).toEqual(['big clients'])
    expect(p.unread).toBe(true)
    expect(p.hasAttachment).toBe(true)
    expect(p.inKind).toBe('inbox')
  })

  it('handles read/starred, to:, subject: and dates', () => {
    const p = parseSearch('is:read is:starred to:me subject:roadmap after:2026-01-01 before:2026-02-01')
    expect(p.unread).toBe(false)
    expect(p.starred).toBe(true)
    expect(p.to).toEqual(['me'])
    expect(p.subject).toEqual(['roadmap'])
    expect(p.after).toBe(Date.parse('2026-01-01'))
    expect(p.before).toBe(Date.parse('2026-02-01'))
  })

  it('keeps unknown operators as plain terms and ignores empty phrases', () => {
    const p = parseSearch('foo:bar "" hello')
    expect(p.terms).toEqual(['foo:bar', 'hello'])
    expect(p.phrases).toEqual([])
  })
})

describe('operator completions', () => {
  const sources = {
    labels: [{ name: 'Finance', color: '#0f0' }, { name: 'Big Clients', color: '#00f' }, { name: 'Family' }],
    contacts: [{ name: 'Priya Raman', email: 'priya@acme.example' }, { name: '', email: 'billing@cloud.example' }],
  }

  it('finds the last token', () => {
    expect(lastToken('hello fr')).toBe('fr')
    expect(lastToken('hello ')).toBe('')
  })

  it('suggests operator names after two characters', () => {
    expect(operatorCompletions('f', sources)).toEqual([])
    expect(operatorCompletions('report fr', sources).map((c) => c.value)).toEqual(['from:'])
    expect(operatorCompletions('is', sources).map((c) => c.value)).toEqual(['is:unread'])
  })

  it('completes labels, quoting names with spaces', () => {
    const c = operatorCompletions('label:big', sources)
    expect(c[0]).toMatchObject({ value: 'label:"Big Clients"', label: 'Big Clients', color: '#00f' })
    expect(operatorCompletions('label:fa', sources).map((x) => x.label)).toContain('Family')
  })

  it('completes senders with the email as value', () => {
    const c = operatorCompletions('from:pri', sources)
    expect(c[0]).toMatchObject({ value: 'from:priya@acme.example', label: 'Priya Raman', hint: 'priya@acme.example' })
    expect(operatorCompletions('from:bill', sources)[0]).toMatchObject({ label: 'billing@cloud.example', hint: '' })
  })

  it('prefers substring sender matches over scattered fuzzy ones', () => {
    const c = operatorCompletions('from:pri', { labels: [], contacts: [{ name: 'Priya', email: 'p@x.io' }, { name: '', email: 'no-reply@spotify.example' }] })
    expect(c.map((x) => x.label)).toEqual(['Priya'])
    expect(operatorCompletions('from:spfy', { labels: [], contacts: [{ name: '', email: 'no-reply@spotify.example' }] })).toHaveLength(1)
  })

  it('completes fixed values and skips exact matches', () => {
    expect(operatorCompletions('is:', sources).map((c) => c.value)).toEqual(['is:unread', 'is:read', 'is:starred'])
    expect(operatorCompletions('in:tr', sources).map((c) => c.value)).toEqual(['in:trash'])
    expect(operatorCompletions('has:attachment', sources)).toEqual([])
  })

  it('applies a completion to the last token', () => {
    expect(applyCompletion('invoice fr', 'from:')).toBe('invoice from: ')
    expect(applyCompletion('label:fi', 'label:Finance')).toBe('label:Finance ')
  })
})
