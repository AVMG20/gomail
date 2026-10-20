// "Clean up by sender": grouping, filtering and the rule state of a row.
import { describe, expect, it } from 'vitest'
import { filterSenders, groupSenders, senderRowKey, senderRules, senderScope, senderSearch, senderStats, senderTitle, type SenderStat } from '@/lib/senders'
import { mock } from '@/lib/mock/backend'
import type { LabelRule } from '@/lib/types'

const stat = (accountId: string, address: string, count: number, newest: string, over: Partial<SenderStat> = {}): SenderStat =>
  ({ accountId, address, name: address.split('@')[0], count, unread: 0, newest, unsubscribe: false, ...over })

const own = (id: string) => (id === 'a' ? 'me@contoso.example' : 'you@fabrikam.example')

describe('groupSenders', () => {
  const stats = [
    stat('a', 'promo@deals.example', 5, '2026-03-01T00:00:00Z', { unread: 4, name: 'Deals' }),
    stat('a', 'news@deals.example', 3, '2026-03-05T00:00:00Z', { unread: 1, name: 'Deals News', unsubscribe: true }),
    stat('a', 'someone@gmail.com', 2, '2026-02-01T00:00:00Z'),
    stat('a', 'other@gmail.com', 2, '2026-02-02T00:00:00Z'),
    stat('a', 'colleague@contoso.example', 6, '2026-01-01T00:00:00Z'),
    stat('b', 'promo@deals.example', 1, '2026-03-02T00:00:00Z'),
  ]

  it('lists addresses, the most mail first, then the newest', () => {
    const rows = groupSenders(stats, 'address', own)
    expect(rows.map((r) => `${r.accountId}:${r.key}`)).toEqual([
      'a:colleague@contoso.example', 'a:promo@deals.example', 'a:news@deals.example', 'a:other@gmail.com', 'a:someone@gmail.com', 'b:promo@deals.example',
    ])
    expect(rows[1]).toMatchObject({ address: 'promo@deals.example', domain: 'deals.example', addresses: 1, count: 5, unread: 4 })
  })

  it('groups company domains per mailbox, never shared mail services or the own domain', () => {
    const rows = groupSenders(stats, 'domain', own)
    const deals = rows.find((r) => r.accountId === 'a' && r.key === '@deals.example')!
    // The newest address names the group.
    expect(deals).toMatchObject({ count: 8, unread: 5, addresses: 2, address: 'news@deals.example', name: 'Deals News', unsubscribe: true, newest: '2026-03-05T00:00:00Z' })
    expect(rows[0]).toBe(deals)
    expect(rows.some((r) => r.key === '@gmail.com' || r.key === '@contoso.example')).toBe(false)
    expect(rows.filter((r) => r.key.endsWith('@gmail.com'))).toHaveLength(2)
    expect(rows.find((r) => r.accountId === 'b')?.key).toBe('@deals.example')
  })

  it('caps the rows', () => {
    expect(groupSenders(stats, 'address', own, 2)).toHaveLength(2)
  })
})

describe('sender rows', () => {
  const [domainRow] = groupSenders([stat('a', 'promo@deals.example', 5, '2026-03-01T00:00:00Z'), stat('a', 'news@deals.example', 1, '2026-01-01T00:00:00Z')], 'domain', own)
  const [addressRow] = groupSenders([stat('a', 'Promo@Deals.example', 214, '2026-03-01T00:00:00Z', { unread: 180, name: 'Deals' })], 'address', own)

  it('junks a domain group as the domain, anything else as the address', () => {
    expect(senderScope(domainRow, 'me@contoso.example')).toBe('domain')
    expect(senderScope(addressRow, 'me@contoso.example')).toBe('address')
    // A domain row of the mailbox's own domain is never junked as a whole.
    expect(senderScope({ ...domainRow, key: '@contoso.example', address: 'x@contoso.example' }, 'me@contoso.example')).toBe('address')
  })

  it('reads as name, stats and search', () => {
    expect(senderTitle(addressRow)).toBe('Deals')
    expect(senderTitle(domainRow)).toBe('deals.example')
    expect(senderStats(addressRow, new Date('2026-03-03T12:00:00Z').getTime())).toBe('214 mails · 180 unread · last 2 days ago')
    expect(senderStats({ ...addressRow, count: 1, unread: 0 }, new Date('2026-03-01T05:00:00Z').getTime())).toBe('1 mail · last today')
    expect(senderSearch(addressRow)).toBe('from:promo@deals.example')
    expect(senderSearch(domainRow)).toBe('from:deals.example')
  })

  it('filters by name, address or domain, without kept rows', () => {
    const list = [addressRow, { ...domainRow, accountId: 'b' }]
    expect(filterSenders(list, 'DEALS')).toHaveLength(2)
    const alex = { ...addressRow, key: 'alex@example.com', address: 'alex@example.com', name: 'Alex', domain: 'example.com' }
    expect(filterSenders([addressRow, alex], 'alex')).toEqual([alex])
    expect(filterSenders([addressRow, alex], 'example.com')).toEqual([alex])
    expect(filterSenders(list, '', new Set([senderRowKey(addressRow)]))).toHaveLength(1)
  })

  it('shows the rules covering the whole row', () => {
    const rules: LabelRule[] = [
      { id: 'r1', accountId: 'a', field: 'from', contains: 'promo@deals.example', action: 'junk', labelId: '' },
      { id: 'r2', accountId: 'a', field: 'from', contains: '@deals.example', action: 'label', labelId: 'l1' },
      { id: 'r3', accountId: 'b', field: 'from', contains: '@deals.example', action: 'junk', labelId: '' },
    ] as LabelRule[]
    expect(senderRules(rules, addressRow).junk.map((r) => r.id)).toEqual(['r1'])
    expect(senderRules(rules, addressRow).label.map((r) => r.id)).toEqual(['r2'])
    // An address rule doesn't cover the whole domain.
    expect(senderRules(rules, domainRow)).toMatchObject({ junk: [], label: [{ id: 'r2' }] })
  })
})

describe('mock TopSenders', () => {
  it('counts inbox senders and resolves their mail exactly', async () => {
    const accounts = await mock.GetAccounts()
    const rows = await mock.TopSenders('', 'inbox', 'address', 0)
    expect(rows.length).toBeGreaterThan(0)
    for (let i = 1; i < rows.length; i++) expect(rows[i - 1].count).toBeGreaterThanOrEqual(rows[i].count)
    expect(rows.some((r) => accounts.some((a) => a.id === r.accountId && a.email.toLowerCase() === r.address))).toBe(false)
    const top = rows[0]
    const ids = await mock.SenderMessageIDs(top.accountId, 'inbox', top.key)
    expect(ids).toHaveLength(top.count)
  })
})

describe('SenderCleanup dialog', () => {
  it('lists senders and acts from the keyboard', async () => {
    const { createApp, nextTick } = await import('vue')
    const { createPinia, setActivePinia } = await import('pinia')
    const { vi } = await import('vitest')
    const { api } = await import('@/lib/api')
    const { useAccountsStore } = await import('@/stores/accounts')
    const { useCleanupStore } = await import('@/stores/cleanup')
    const { useSenderRulesStore } = await import('@/stores/senderRules')
    const { useSettingsStore } = await import('@/stores/settings')
    const SenderCleanup = (await import('@/components/mail/SenderCleanup.vue')).default

    const pinia = createPinia()
    setActivePinia(pinia)
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: 'me@contoso.example', displayName: 'Work' }] as never
    accounts.folders = { a: [{ id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox' }] } as never
    vi.spyOn(accounts, 'refreshCounts').mockResolvedValue(undefined as never)
    useSettingsStore().settings = { labels: [], labelRules: [] } as never
    const rows = groupSenders([
      stat('a', 'promo@deals.example', 9, '2026-03-01T00:00:00Z', { name: 'Deals' }),
      stat('a', 'news@shop.example', 4, '2026-03-02T00:00:00Z', { name: 'Shop' }),
      stat('a', 'alex@example.com', 2, '2026-03-03T00:00:00Z', { name: 'Alex' }),
    ], 'address', own)
    const lookup = vi.spyOn(api, 'senderMessageIds').mockResolvedValue(['m1', 'm2'])
    const archive = vi.spyOn(api, 'archive').mockResolvedValue()
    // The archived sender leaves the inbox.
    const top = vi.spyOn(api, 'topSenders').mockImplementation(async () => (archive.mock.calls.length ? rows.filter((r) => r.name !== 'Shop') : rows))
    const junk = vi.spyOn(useSenderRulesStore(), 'junkSender').mockResolvedValue()

    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(SenderCleanup)
    app.use(pinia)
    useCleanupStore().show('a')
    app.mount(el)
    const flush = async () => { for (let i = 0; i < 6; i++) await nextTick(); await new Promise((r) => setTimeout(r, 0)) }
    await flush()
    expect(top).toHaveBeenCalledWith('a', 'inbox', 'address', 300)
    const names = () => [...document.querySelectorAll('[role="option"] [data-sender-title]')].map((n) => n.textContent?.trim())
    expect(names()).toEqual(['Deals', 'Shop', 'Alex'])
    expect(document.body.textContent).toContain('9 mails')

    const list = document.querySelector<HTMLElement>('[role="listbox"]')!
    const press = (key: string) => list.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
    press('ArrowDown')
    await flush()
    expect(document.querySelector('[role="option"][aria-selected="true"] [data-sender-title]')?.textContent?.trim()).toBe('Shop')
    press('a')
    await flush()
    expect(lookup).toHaveBeenCalledWith('a', 'inbox', 'news@shop.example')
    expect(archive).toHaveBeenCalledWith(['m1', 'm2'])
    expect(names()).toEqual(['Deals', 'Alex'])
    press('k') // the next sender moved up into the same position
    await flush()
    expect(names()).toEqual(['Deals'])
    press('Home')
    press('j')
    await flush()
    expect(junk).toHaveBeenCalledWith([], 'address', { accountId: 'a', address: 'promo@deals.example' })
    app.unmount()
    el.remove()
  })
})
