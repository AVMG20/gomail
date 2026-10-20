import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  chipLabelIds, groupByName, groupCount, labelInAccount, labelInMailbox, labelsForAccount, labelsForAccounts, nameTaken, newLabelId, sameNameIds, viewLabelId,
} from '@/lib/labelScope'
import { mergeLabelImport } from '@/lib/labelsIO'
import type { Label, LabelCount, Settings } from '@/lib/types'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

const L = (id: string, name: string, accountIds?: string[], source = 'user'): Label => ({ id, name, description: '', color: '#000000', source, accountIds })
// Every mailbox has its own labels; a1 and a2 both have a "News" label.
const labels = [
  L('work', 'Work', ['a1']),
  L('news', 'News', ['a1']),
  L('home', 'Home', ['a2']),
  L('news-2', 'news', ['a2']),
  L('junk', 'Junk', [], 'system'),
  L('receipts', 'Receipts', ['a1'], 'provider'),
]
const ids = (ls: Label[]) => ls.map((l) => l.id)

describe('labels per mailbox', () => {
  it('offers a mailbox only its own labels and Junk', () => {
    expect(ids(labelsForAccount(labels, 'a1'))).toEqual(['work', 'news', 'junk', 'receipts'])
    expect(ids(labelsForAccount(labels, 'a2'))).toEqual(['home', 'news-2', 'junk'])
    expect(labelInAccount(L('junk', 'Junk', ['a1'], 'system'), 'a2')).toBe(true)
  })

  it('groups labels by name in the unified view', () => {
    expect(ids(labelsForAccount(labels, ''))).toEqual(['work', 'news', 'home', 'junk', 'receipts'])
    expect(ids(groupByName(labels))).toEqual(ids(labelsForAccount(labels, '')))
    expect(sameNameIds(labels, ['news']).sort()).toEqual(['news', 'news-2'])
    expect(sameNameIds(labels, ['work', 'gone'])).toEqual(['work', 'gone'])
  })

  it('offers a selection its mailbox’s labels, or one per name for several mailboxes', () => {
    expect(ids(labelsForAccounts(labels, ['a2', 'a2']))).toEqual(['home', 'news-2', 'junk'])
    expect(ids(labelsForAccounts(labels, ['a1', 'a2']))).toEqual(['work', 'news', 'home', 'junk', 'receipts'])
    expect(ids(labelsForAccounts(labels, []))).toEqual(ids(groupByName(labels)))
  })

  it('finds a mailbox’s own label with the same name', () => {
    expect(labelInMailbox(labels, labels[1], 'a2')?.id).toBe('news-2')
    expect(labelInMailbox(labels, labels[1], 'a1')?.id).toBe('news')
    expect(labelInMailbox(labels, labels[0], 'a2')).toBeUndefined()
    expect(labelInMailbox(labels, labels[4], 'a2')?.id).toBe('junk')
  })

  it('keeps view filters on the view’s labels', () => {
    expect(viewLabelId(labels, 'news-2', '')).toBe('news') // the group's entry in the unified view
    expect(viewLabelId(labels, 'news', 'a2')).toBe('news-2') // follows the name into the mailbox
    expect(viewLabelId(labels, 'work', 'a2')).toBeUndefined()
    expect(viewLabelId(labels, 'gone', 'a1')).toBeUndefined()
  })

  it('sums counts over the labels of a name', () => {
    const counts = new Map<string, LabelCount>([['news', { labelId: 'news', total: 3, unread: 1 }], ['news-2', { labelId: 'news-2', total: 2, unread: 2 }]])
    expect(groupCount(labels, counts, labels[1])).toEqual({ total: 5, unread: 3 })
    expect(groupCount(labels, counts, labels[0])).toBeUndefined()
  })

  it('makes unused ids and checks names per mailbox', () => {
    expect(newLabelId(labels, 'News')).toBe('news-3')
    expect(newLabelId(labels, 'Side Project!')).toBe('side-project')
    expect(newLabelId(labels, 'Junk')).toBe('junk-label')
    expect(nameTaken(labels, 'a2', 'work')).toBe(false)
    expect(nameTaken(labels, 'a1', ' WORK ')).toBe(true)
    expect(nameTaken(labels, 'a2', 'junk')).toBe(true)
    expect(nameTaken(labels, 'a1', 'work', 'work')).toBe(false)
  })

  it('imports a label file into one mailbox', () => {
    const r = mergeLabelImport(JSON.stringify({ labels: [{ name: 'News', description: 'Digests', color: '#111111' }, { name: 'Travel' }] }), labels, (i) => `new${i}`, 'a2')
    if (!r.ok) throw new Error(r.error)
    const next = r.patch.labels!
    expect(next.find((l) => l.id === 'news-2')?.description).toBe('Digests')
    expect(next.find((l) => l.id === 'news')?.description).toBe('')
    expect(next.find((l) => l.name === 'Travel')?.accountIds).toEqual(['a2'])
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
    expect(ids(settings.labelsForAccount('a2'))).toEqual(['home', 'news-2', 'junk'])
    expect(settings.labelInAccountOf('news', 'a2')?.id).toBe('news-2')
    expect(settings.labelGroupIds(['news-2']).sort()).toEqual(['news', 'news-2'])
  })

  it('renames within the mailbox only', () => {
    const settings = useSettingsStore()
    settings.settings = { labels } as unknown as Settings
    expect(settings.renameLabel('home', 'Work')).toEqual({ name: 'Work', deduped: false }) // a1's Work doesn't count
    expect(settings.renameLabel('news-2', 'work')).toEqual({ name: 'work 2', deduped: true }) // a2's now does
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

  it('filters the unified view by every label of a name', () => {
    const settings = useSettingsStore()
    settings.settings = { labels } as unknown as Settings
    const mail = useMailStore()
    mail.mailbox = { accountId: '', folderId: '', kind: 'inbox' }
    mail.setLabelFilter({ labels: ['news'], unlabeled: false, hidden: [] })
    expect([...(mail.query.labels ?? [])].sort()).toEqual(['news', 'news-2'])
  })
})

describe('chipLabelIds', () => {
  const kinds: Record<string, string> = { f_spam: 'spam', f_inbox: 'inbox' }
  it('drops the Junk chip only for mail in a Junk folder', () => {
    expect(chipLabelIds(['junk', 'news'], ['f_spam'], (f) => kinds[f])).toEqual(['news'])
    expect(chipLabelIds(['junk', 'news'], ['f_inbox'], (f) => kinds[f])).toEqual(['junk', 'news'])
    expect(chipLabelIds(['news'], undefined, (f) => kinds[f])).toEqual(['news'])
  })
})

describe('copyLabelsTo', () => {
  it('copies user labels of one mailbox, skipping names the other has', async () => {
    const { copyLabelsTo } = await import('@/lib/labelScope')
    const labels = [
      { id: 'news', name: 'News', description: 'd', color: '#111111', source: 'user', accountIds: ['a'], then: 'archive' },
      { id: 'work', name: 'Work', description: '', color: '#222222', source: 'user', accountIds: ['a'] },
      { id: 'work-2', name: 'work', description: '', color: '#333333', source: 'user', accountIds: ['b'] },
      { id: 'cat', name: 'Red category', description: '', color: '#444444', source: 'provider', accountIds: ['a'] },
    ] as import('@/lib/types').Label[]
    const r = copyLabelsTo(labels, 'a', 'b')
    expect(r.copied).toBe(1)
    const copy = r.labels.at(-1)!
    expect(copy).toMatchObject({ name: 'News', description: 'd', then: 'archive', accountIds: ['b'] })
    expect(copy.id).not.toBe('news')
  })
})
