import { describe, expect, it } from 'vitest'
import { domainOf, isFreeMailDomain, junkableDomain, ruleTarget, rulesForSender, sendersOf } from '@/lib/junk'
import type { LabelRule } from '@/lib/types'

describe('sender rules helpers', () => {
  it('knows shared mail services, including country variants', () => {
    for (const d of ['gmail.com', 'Outlook.com', 'hotmail.co.uk', 'yahoo.fr', 'icloud.com', 'ziggo.nl']) expect(isFreeMailDomain(d)).toBe(true)
    for (const d of ['deals.example', 'news.example.com']) expect(isFreeMailDomain(d)).toBe(false)
  })

  it('offers the domain only for company domains other than the mailbox’s own', () => {
    expect(domainOf('Alex@Deals.Example')).toBe('deals.example')
    expect(domainOf('not-an-address')).toBe('')
    expect(junkableDomain('promo@deals.example', 'me@contoso.example')).toBe('deals.example')
    expect(junkableDomain('someone@gmail.com', 'me@contoso.example')).toBe('')
    expect(junkableDomain('colleague@contoso.example', 'me@contoso.example')).toBe('')
  })

  it('names a rule by address or domain', () => {
    expect(ruleTarget('alex@example.com')).toBe('alex@example.com')
    expect(ruleTarget('@example.com')).toBe('example.com')
  })

  it('finds the mailbox’s rules matching a sender, domain rules included', () => {
    const rules: LabelRule[] = [
      { id: 'a', accountId: 'acct1', field: 'from', contains: 'promo@deals.example', action: 'junk', labelId: '' },
      { id: 'b', accountId: 'acct1', field: 'from', contains: '@deals.example', action: 'label', labelId: 'news' },
      { id: 'c', accountId: 'acct2', field: 'from', contains: 'promo@deals.example', action: 'junk', labelId: '' },
      { id: 'd', accountId: 'acct1', field: 'subject', contains: 'deals', action: 'junk', labelId: '' },
      { id: 'e', accountId: '', field: 'from', contains: 'PROMO@', action: 'junk', labelId: '' },
    ]
    expect(rulesForSender(rules, 'acct1', 'Promo@Deals.example').map((r) => r.id)).toEqual(['a', 'b', 'e'])
    expect(rulesForSender(rules, 'acct1', 'other@deals.example').map((r) => r.id)).toEqual(['b'])
    expect(rulesForSender(rules, 'acct1', '')).toEqual([])
  })

  it('groups picked rows by sender and mailbox, skipping own mail', () => {
    const rows = [
      { id: '1', accountId: 'a', from: { email: 'promo@deals.example' } },
      { id: '2', accountId: 'a', from: { email: 'PROMO@deals.example' } },
      { id: '3', accountId: 'b', from: { email: 'promo@deals.example' } },
      { id: '4', accountId: 'a', from: { email: 'me@contoso.example' } },
      { id: '5', accountId: 'a', from: { email: '' } },
    ]
    const own = (acct: string) => (acct === 'a' ? 'me@contoso.example' : 'you@contoso.example')
    expect(sendersOf(rows, own)).toEqual([
      { accountId: 'a', address: 'promo@deals.example', ids: ['1', '2'] },
      { accountId: 'b', address: 'promo@deals.example', ids: ['3'] },
    ])
  })
})
