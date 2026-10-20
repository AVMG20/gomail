// Sidebar label counts follow the box on screen, and replies are kept.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'

beforeEach(() => setActivePinia(createPinia()))

describe('label counts', () => {
  it('keeps the counts for the box that asked', async () => {
    const spy = vi.spyOn(api, 'getViewLabelCounts').mockResolvedValue([{ labelId: 'news', total: 5, unread: 2 }])
    vi.spyOn(api, 'getLabelCounts').mockResolvedValue([{ labelId: 'news', total: 8, unread: 3 }, { labelId: 'junk', total: 4, unread: 0 }])
    const accounts = useAccountsStore()
    await accounts.loadLabelCounts({ accountId: 'a1', folderId: 'f-all', folderKind: '' })
    expect(spy).toHaveBeenCalledWith({ accountId: 'a1', folderId: 'f-all', folderKind: '' })
    expect(accounts.labelCounts.get('news')).toEqual({ labelId: 'news', total: 5, unread: 2 })
    // Which labels to list doesn't depend on the box: Junk is listed in the Inbox too.
    expect(accounts.labelTotals.get('junk')?.total).toBe(4)
  })

  it('drops a reply for a box that is no longer on screen', async () => {
    let resolveOld!: (v: { labelId: string; total: number; unread: number }[]) => void
    vi.spyOn(api, 'getLabelCounts').mockResolvedValue([])
    vi.spyOn(api, 'getViewLabelCounts')
      .mockImplementationOnce(() => new Promise((r) => (resolveOld = r)))
      .mockResolvedValueOnce([{ labelId: 'news', total: 1, unread: 1 }])
    const accounts = useAccountsStore()
    const old = accounts.loadLabelCounts({ accountId: 'a1', folderId: 'inbox', folderKind: '' })
    await accounts.loadLabelCounts({ accountId: 'a1', folderId: 'f-all', folderKind: '' })
    resolveOld([{ labelId: 'news', total: 9, unread: 9 }])
    await old
    expect(accounts.labelCounts.get('news')?.unread).toBe(1)
  })
})
