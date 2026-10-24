// The sidebar's label list leaves out the built-in Junk label (its mail is in the Junk folder).
import { describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import type { Label, Settings } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import FolderSidebar from '@/components/mail/FolderSidebar.vue'

const L = (id: string, name: string, source = 'user'): Label => ({ id, name, description: '', color: '#000000', source, accountIds: source === 'system' ? [] : ['a'] })

describe('sidebar labels', () => {
  it('lists the mailbox’s labels but not the built-in Junk label, even when mail has it', async () => {
    vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
    const pinia = createPinia()
    setActivePinia(pinia)
    useSettingsStore().settings = { labels: [L('work', 'Work'), L('junk', 'Junk', 'system'), L('receipts', 'Receipts', 'provider')] } as unknown as Settings
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: 'alex@example.com' }] as never
    accounts.folders = { a: [{ id: 'a:inbox', accountId: 'a', kind: 'inbox', name: 'Inbox', unread: 0, total: 1 }, { id: 'a:spam', accountId: 'a', kind: 'spam', name: 'Spam', unread: 0, total: 4 }] } as never
    accounts.labelTotals = new Map([['junk', { labelId: 'junk', total: 4, unread: 0 }], ['receipts', { labelId: 'receipts', total: 2, unread: 0 }]])
    useMailStore().mailbox = { accountId: 'a', folderId: 'a:inbox', kind: 'inbox' }
    const el = document.createElement('div')
    const app = createApp(FolderSidebar)
    app.use(pinia)
    app.mount(el)
    await nextTick()
    const labels = [...el.querySelectorAll('[aria-label="Folders"] li')].map((li) => li.textContent?.trim() ?? '')
    expect(labels.some((t) => t.startsWith('Work'))).toBe(true)
    expect(labels.some((t) => t.startsWith('Receipts'))).toBe(true)
    // The Junk folder is still there; the Junk label isn't.
    expect(labels.filter((t) => t.startsWith('Junk'))).toHaveLength(1)
    app.unmount()
  })
})
