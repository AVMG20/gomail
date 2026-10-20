import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mailboxKey, useMailStore } from '@/stores/mail'

describe('label filter state per mailbox', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('builds stable mailbox keys', () => {
    expect(mailboxKey({ accountId: 'a', folderId: 'f1', kind: 'inbox' })).toBe('a|f1')
    expect(mailboxKey({ accountId: '', folderId: '', kind: 'inbox' })).toBe('|inbox')
  })

  it('keeps a separate filter for each mailbox and persists it', () => {
    const mail = useMailStore()
    mail.mailbox = { accountId: 'acc1', folderId: 'inbox1', kind: 'inbox' }
    mail.toggleLabelFilter('work')
    mail.toggleLabelFilter('finance')
    expect(mail.labelFilter.labels).toEqual(['work', 'finance'])
    expect(mail.hasLabelFilter).toBe(true)

    mail.mailbox = { accountId: 'acc2', folderId: 'inbox2', kind: 'inbox' }
    expect(mail.labelFilter).toEqual({ labels: [], unlabeled: false, hidden: [] })
    mail.toggleUnlabeledFilter()
    expect(mail.labelFilter.unlabeled).toBe(true)

    mail.mailbox = { accountId: 'acc1', folderId: 'inbox1', kind: 'inbox' }
    mail.toggleLabelFilter('work')
    expect(mail.labelFilter.labels).toEqual(['finance'])

    // A fresh store (app restart) restores the filters.
    setActivePinia(createPinia())
    const again = useMailStore()
    again.mailbox = { accountId: 'acc2', folderId: 'inbox2', kind: 'inbox' }
    expect(again.labelFilter.unlabeled).toBe(true)
  })

  it('clears only the current mailbox', () => {
    const mail = useMailStore()
    mail.mailbox = { accountId: 'a', folderId: 'x', kind: 'inbox' }
    mail.toggleLabelFilter('l1')
    mail.mailbox = { accountId: 'b', folderId: 'y', kind: 'inbox' }
    mail.toggleLabelFilter('l2')
    mail.clearLabelFilter()
    expect(mail.hasLabelFilter).toBe(false)
    mail.mailbox = { accountId: 'a', folderId: 'x', kind: 'inbox' }
    expect(mail.labelFilter.labels).toEqual(['l1'])
  })
})

describe('hidden labels', () => {
  it('hides a label, keeps it exclusive with show-only, and sends excludeLabels', async () => {
    const { createPinia, setActivePinia } = await import('pinia')
    setActivePinia(createPinia())
    const { useMailStore } = await import('@/stores/mail')
    const mail = useMailStore()
    mail.toggleLabelFilter('work')
    mail.toggleHiddenLabel('work')
    expect(mail.labelFilter).toMatchObject({ labels: [], hidden: ['work'] })
    expect(mail.query.excludeLabels).toEqual(['work'])
    expect(mail.hasLabelFilter).toBe(true)
    mail.toggleLabelFilter('work')
    expect(mail.labelFilter).toMatchObject({ labels: ['work'], hidden: [] })
    mail.toggleHiddenLabel('news')
    mail.clearLabelFilter()
    expect(mail.labelFilter).toEqual({ labels: [], unlabeled: false, hidden: [] })
  })
})
