import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  labelAccounts, labelInAccount, labelsForAccount, labelsForAccounts, newLabelAccounts, toggleLabelAccount,
} from '@/lib/labelScope'
import type { Label, Settings } from '@/lib/types'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

const L = (id: string, accountIds?: string[], source = 'user'): Label => ({ id, name: id, description: '', color: '#000000', source, accountIds })
const labels = [
  L('work', ['a1']),
  L('home', ['a2']),
  L('both', ['a1', 'a2']),
  L('legacy'), // saved before labels were per mailbox: every mailbox
  L('junk', [], 'system'),
  L('receipts', ['a1'], 'provider'),
]
const ids = (ls: Label[]) => ls.map((l) => l.id)

describe('labels per mailbox', () => {
  it('offers a mailbox only its own labels, Junk and unscoped labels', () => {
    expect(ids(labelsForAccount(labels, 'a1'))).toEqual(['work', 'both', 'legacy', 'junk', 'receipts'])
    expect(ids(labelsForAccount(labels, 'a2'))).toEqual(['home', 'both', 'legacy', 'junk'])
    expect(labelInAccount(L('junk', ['a1'], 'system'), 'a2')).toBe(true)
  })

  it('offers every label in the unified view', () => {
    expect(ids(labelsForAccount(labels, ''))).toEqual(ids(labels))
  })

  it('offers a mixed selection the labels of any of its mailboxes', () => {
    expect(ids(labelsForAccounts(labels, ['a2', 'a2']))).toEqual(['home', 'both', 'legacy', 'junk'])
    expect(ids(labelsForAccounts(labels, ['a1', 'a2']))).toEqual(ids(labels))
    expect(ids(labelsForAccounts(labels, ['a3']))).toEqual(['legacy', 'junk'])
    expect(ids(labelsForAccounts(labels, []))).toEqual(ids(labels))
  })

  it('gives new labels the mailbox being viewed, or every mailbox in the unified view', () => {
    expect(newLabelAccounts('a2', ['a1', 'a2'])).toEqual(['a2'])
    expect(newLabelAccounts('', ['a1', 'a2'])).toEqual(['a1', 'a2'])
    expect(newLabelAccounts('gone', ['a1', 'a2'])).toEqual(['a1', 'a2'])
  })

  it('edits a label’s mailboxes, keeping at least one', () => {
    const all = ['a1', 'a2', 'a3']
    expect(labelAccounts(L('legacy'), all)).toEqual(all)
    expect(labelAccounts(L('x', ['a3', 'removed', 'a1']), all)).toEqual(['a1', 'a3'])
    expect(toggleLabelAccount(L('legacy'), 'a2', false, all)).toEqual(['a1', 'a3'])
    expect(toggleLabelAccount(L('x', ['a3']), 'a1', true, all)).toEqual(['a1', 'a3'])
    expect(toggleLabelAccount(L('x', ['a3']), 'a3', false, all)).toEqual(['a3'])
  })
})

describe('label scope in the stores', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('filters through the settings store helpers', () => {
    const settings = useSettingsStore()
    settings.settings = { labels } as unknown as Settings
    expect(ids(settings.labelsForAccount('a2'))).toEqual(['home', 'both', 'legacy', 'junk'])
    expect(ids(settings.labelsForAccounts(['a1']))).toEqual(['work', 'both', 'legacy', 'junk', 'receipts'])
  })

  it('knows the mailboxes of the selection', () => {
    const mail = useMailStore()
    mail.mailbox = { accountId: '', folderId: '', kind: 'inbox' }
    const msg = (id: string, accountId: string) => ({ id, accountId, labels: [] }) as unknown as (typeof mail.messages)[number]
    mail.messages = [msg('m1', 'a1'), msg('m2', 'a2'), msg('m3', 'a1')]
    mail.selection = new Set(['m1', 'm3'])
    expect(mail.selectionAccountIds).toEqual(['a1'])
    mail.selection = new Set(['m1', 'm2'])
    expect([...mail.selectionAccountIds].sort()).toEqual(['a1', 'a2'])
    mail.selectAllQuery = true
    expect(mail.selectionAccountIds).toEqual([])
  })
})
