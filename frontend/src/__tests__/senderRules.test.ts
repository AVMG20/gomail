// Sender rules from the mail: "Always send <sender> to Junk" (with Undo) and "Stop junking" after "Not junk".
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

type Opts = { action?: { label: string; onClick: () => unknown }; cancel?: { label: string } }
const toasts: { msg: string; opts?: Opts }[] = []
vi.mock('vue-sonner', () => {
  const rec = (msg: string, opts?: Opts) => void toasts.push({ msg, opts })
  return { toast: Object.assign(rec, { error: rec, success: rec, warning: rec, dismiss: () => {} }) }
})

import { api } from '@/lib/api'
import type { MessageSummary, Settings } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSenderRulesStore } from '@/stores/senderRules'
import { useSettingsStore } from '@/stores/settings'

const row = (id: string, over: Partial<MessageSummary> = {}) =>
  ({ id, accountId: 'a', threadId: id, folderIds: ['inbox1'], from: { name: '', email: 'promo@deals.example' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary

beforeEach(() => {
  localStorage.clear()
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => vi.restoreAllMocks())

function setup() {
  const accounts = useAccountsStore()
  accounts.accounts = [{ id: 'a', email: 'me@contoso.example' }, { id: 'b', email: 'you@contoso.example' }] as never
  accounts.folders = {
    a: [{ id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox' }, { id: 'junk1', accountId: 'a', kind: 'spam', name: 'Junk' }],
    b: [{ id: 'inbox2', accountId: 'b', kind: 'inbox', name: 'Inbox' }, { id: 'junk2', accountId: 'b', kind: 'spam', name: 'Junk' }],
  } as never
  vi.spyOn(accounts, 'refreshCounts').mockResolvedValue(undefined as never)
  const settings = useSettingsStore()
  settings.settings = { labels: [], labelRules: [] } as unknown as Settings
  const rule = { id: 'r1', accountId: 'a', field: 'from' as const, contains: 'promo@deals.example', action: 'junk' as const, labelId: '' }
  vi.spyOn(api, 'getSettings').mockResolvedValue({ labels: [], labelRules: [rule] } as unknown as Settings)
  const mail = useMailStore()
  vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
  vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
  return { mail, settings, rules: useSenderRulesStore() }
}

describe('sender rules', () => {
  it('“Always send to Junk” junks each sender per mailbox, refreshes the rules, and Undo puts everything back', async () => {
    const { mail, settings, rules } = setup()
    mail.setMailbox({ accountId: '', folderId: '', kind: 'inbox' })
    mail.messages = [row('m1'), row('m2', { from: { name: '', email: 'me@contoso.example' } }), row('m3', { accountId: 'b', folderIds: ['inbox2'] })]
    const junk = vi.spyOn(api, 'junkSender').mockImplementation(async (accountId) => ({
      rule: { id: `r-${accountId}`, accountId, contains: 'promo@deals.example', action: 'junk', labelId: '' },
      created: true, count: 2, moved: [`old-${accountId}`], unread: [`old-${accountId}`],
    }))
    await rules.junkSender(['m1', 'm2', 'm3'])
    // Own mail (m2) has no sender to junk.
    expect(junk.mock.calls).toEqual([['a', 'promo@deals.example', 'address', ['m1']], ['b', 'promo@deals.example', 'address', ['m3']]])
    expect(mail.messages.map((m) => m.id)).toEqual(['m2'])
    expect(settings.settings?.labelRules?.map((r) => r.id)).toEqual(['r1'])
    const t = toasts.find((x) => x.msg.includes('to Junk from now on'))!
    expect(t.msg).toBe('2 senders go to Junk from now on')
    expect(t.opts?.cancel?.label).toBe('Edit rules')

    const remove = vi.spyOn(api, 'removeRules').mockResolvedValue(undefined)
    const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
    const read = vi.spyOn(api, 'markRead').mockResolvedValue(undefined)
    await t.opts!.action!.onClick()
    expect(remove).toHaveBeenCalledWith(['r-a', 'r-b'])
    expect(move.mock.calls).toEqual(expect.arrayContaining([[['m1'], 'inbox1'], [['m3'], 'inbox2'], [['old-a'], 'inbox1'], [['old-b'], 'inbox2']]))
    expect(read).toHaveBeenCalledWith(['old-a', 'old-b'], false)
  })

  it('“Not junk” offers to stop a junk rule that still matches the sender', async () => {
    const { mail } = setup()
    mail.setMailbox({ accountId: '', folderId: '', kind: 'spam' })
    mail.messages = [row('m1', { folderIds: ['junk1'] })]
    const rule = { id: 'r1', accountId: 'a', contains: '@deals.example', action: 'junk' as const, labelId: '' }
    vi.spyOn(api, 'notJunk').mockResolvedValue({ count: 1, rules: [rule], more: [] })
    await mail.notJunk(['m1'])
    const t = toasts.find((x) => x.msg.startsWith('A rule still sends'))!
    expect(t.msg).toBe('A rule still sends deals.example to Junk')
    expect(t.opts?.action?.label).toBe('Stop junking deals.example')
    const remove = vi.spyOn(api, 'removeRules').mockResolvedValue(undefined)
    await t.opts!.action!.onClick()
    await vi.waitFor(() => expect(remove).toHaveBeenCalledWith(['r1']))
  })

  it('“Not junk” remembers the sender and offers to move the rest of their Junk to the Inbox', async () => {
    const { mail, settings, rules } = setup()
    mail.setMailbox({ accountId: '', folderId: '', kind: 'spam' })
    mail.messages = [row('m1', { folderIds: ['junk1'] })]
    vi.spyOn(api, 'getSettings').mockResolvedValue({ labels: [], labelRules: [], notJunkSenders: { a: ['promo@deals.example'] } } as unknown as Settings)
    vi.spyOn(api, 'notJunk').mockResolvedValue({ count: 1, rules: [], more: [{ accountId: 'a', email: 'promo@deals.example', count: 3 }] })
    await mail.notJunk(['m1'])
    await vi.waitFor(() => expect(rules.junkLeft).toEqual([{ accountId: 'a', email: 'promo@deals.example', count: 3 }]))
    expect(settings.settings?.notJunkSenders).toEqual({ a: ['promo@deals.example'] })

    const all = vi.spyOn(api, 'notJunkSender').mockResolvedValue(3)
    await rules.moveJunkLeft()
    expect(all).toHaveBeenCalledWith('a', 'promo@deals.example')
    expect(rules.junkLeft).toEqual([])
    expect(toasts.some((t) => t.msg === '3 more messages moved to Inbox')).toBe(true)
  })

  it('no dialog when the sender has nothing else in Junk', async () => {
    const { mail, rules } = setup()
    mail.messages = [row('m1', { folderIds: ['junk1'] })]
    vi.spyOn(api, 'notJunk').mockResolvedValue({ count: 1, rules: [], more: [] })
    await mail.notJunk(['m1'])
    await new Promise((r) => setTimeout(r, 0))
    expect(rules.junkLeft).toEqual([])
  })
})
