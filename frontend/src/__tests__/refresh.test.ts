// Backend events share one round of refreshes: counts are read once per burst, accounts:changed
// reads only the account level, and a read/star change's own messages:changed doesn't refetch the list.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const { toast } = vi.hoisted(() => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn(), dismiss: vi.fn() }) }))
vi.mock('vue-sonner', () => ({ toast }))

import { api } from '@/lib/api'
import type { EventName, EventPayloads, MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'

type Handler = (p: unknown) => void
const row = (id: string, accountId = 'a') => ({ id, accountId, threadId: id, folderIds: [`${accountId}:inbox`], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [] }) as unknown as MessageSummary

function spies() {
  return {
    accounts: vi.spyOn(api, 'getAccounts').mockResolvedValue([{ id: 'a', email: 'alex@example.com' }, { id: 'b', email: 'sam@example.com' }] as never),
    folders: vi.spyOn(api, 'getFolders').mockImplementation(async (id: string) => [{ id: `${id}:inbox`, accountId: id, kind: 'inbox', name: 'Inbox', unread: 1, total: 2 }] as never),
    viewCounts: vi.spyOn(api, 'getViewLabelCounts').mockResolvedValue([]),
    labelCounts: vi.spyOn(api, 'getLabelCounts').mockResolvedValue([]),
    list: vi.spyOn(api, 'listMessages').mockImplementation(async () => ({ messages: [row('m1'), row('m2')], nextCursor: '', total: 2, hasMoreRemote: false })),
    markRead: vi.spyOn(api, 'markRead').mockResolvedValue(undefined),
    star: vi.spyOn(api, 'setStarred').mockResolvedValue(undefined),
  }
}

async function withEvents() {
  const handlers = new Map<string, Handler>()
  vi.spyOn(api, 'on').mockImplementation(((e: EventName, cb: (p: EventPayloads[EventName]) => void) => {
    handlers.set(e, cb as Handler)
    return () => {}
  }) as typeof api.on)
  const { useBackendEvents } = await import('@/composables/useBackendEvents')
  const app = createApp(defineComponent({ setup: () => (useBackendEvents(), () => h('div')) }))
  const pinia = createPinia()
  setActivePinia(pinia)
  app.use(pinia)
  const accounts = useAccountsStore()
  accounts.accounts = [{ id: 'a', email: 'alex@example.com' }, { id: 'b', email: 'sam@example.com' }] as never
  const mail = useMailStore()
  mail.mailbox = { accountId: 'a', folderId: 'a:inbox', kind: 'inbox' }
  app.mount(document.createElement('div'))
  await vi.advanceTimersByTimeAsync(50) // the mailbox's list has loaded
  vi.clearAllMocks()
  const emit = (e: string, p?: unknown) => handlers.get(e)!(p)
  return { app, accounts, mail, emit }
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())

describe('shared refresh', () => {
  it('reads everything asked within the window once, and only the folders asked for', async () => {
    const s = spies()
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: 'alex@example.com' }, { id: 'b', email: 'sam@example.com' }] as never
    const done = accounts.refreshCounts('a')
    void accounts.refresh({ accounts: true, folders: 'a' })
    void accounts.refreshCounts('a')
    await vi.advanceTimersByTimeAsync(400)
    await done
    expect(s.accounts).toHaveBeenCalledTimes(1)
    expect(s.folders.mock.calls).toEqual([['a']])
    expect(s.viewCounts).toHaveBeenCalledTimes(1)
    expect(s.labelCounts).toHaveBeenCalledTimes(1)
    void accounts.refreshCounts('a')
    void accounts.refreshCounts('') // any mailbox: every account's folders
    await vi.advanceTimersByTimeAsync(400)
    expect(s.folders.mock.calls.slice(1).map((c) => c[0]).sort()).toEqual(['a', 'b'])
  })

  it('an older folder reply never overwrites a newer one', async () => {
    spies()
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: 'alex@example.com' }] as never
    let slow!: (v: never) => void
    vi.spyOn(api, 'getFolders')
      .mockImplementationOnce(() => new Promise((r) => (slow = r)))
      .mockResolvedValueOnce([{ id: 'a:inbox', accountId: 'a', kind: 'inbox', name: 'Inbox', unread: 5, total: 9 }] as never)
    const first = accounts.loadFolders(['a'])
    await accounts.loadFolders(['a'])
    slow([{ id: 'a:inbox', accountId: 'a', kind: 'inbox', name: 'Inbox', unread: 7, total: 9 }] as never)
    await first
    expect(accounts.folderOf('a', 'inbox')?.unread).toBe(5)
  })
})

describe('backend events', () => {
  it('messages:changed and accounts:changed in a row read the counts once', async () => {
    const s = spies()
    const { app, emit } = await withEvents()
    emit('messages:changed', { accountId: 'a' })
    emit('accounts:changed', {})
    await vi.advanceTimersByTimeAsync(400)
    expect(s.accounts).toHaveBeenCalledTimes(1)
    expect(s.viewCounts).toHaveBeenCalledTimes(1)
    expect(s.labelCounts).toHaveBeenCalledTimes(1)
    expect(s.folders.mock.calls.map((c) => c[0]).sort()).toEqual(['a', 'b'])
    app.unmount()
  })

  it('accounts:changed for one mailbox reads the account list and its folders, not the label counts', async () => {
    const s = spies()
    const { app, emit } = await withEvents()
    emit('accounts:changed', { accountId: 'b' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.accounts).toHaveBeenCalledTimes(1)
    expect(s.folders.mock.calls).toEqual([['b']])
    expect(s.viewCounts).not.toHaveBeenCalled()
    app.unmount()
  })

  it('a sync round that brought nothing only reads the sync status', async () => {
    const s = spies()
    const { app, emit } = await withEvents()
    emit('sync:finished', { accountId: 'a', newMessages: 0, error: '' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.accounts).toHaveBeenCalledTimes(1)
    expect(s.folders).not.toHaveBeenCalled()
    expect(s.viewCounts).not.toHaveBeenCalled()
    expect(s.list).not.toHaveBeenCalled()
    emit('sync:finished', { accountId: 'a', newMessages: 2, error: '' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).toHaveBeenCalled()
    expect(s.folders.mock.calls).toEqual([['a']])
    app.unmount()
  })

  it('the echo of marking read doesn’t refetch the list; the next change does', async () => {
    const s = spies()
    const { app, mail, emit } = await withEvents()
    mail.messages = [row('m1'), row('m2')]
    mail.status = 'ready'
    await mail.setRead(['m1'], true)
    emit('messages:changed', { accountId: 'a' }) // the backend confirming it
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).not.toHaveBeenCalled()
    expect(s.folders.mock.calls).toEqual([['a']]) // the counts still follow, for that mailbox only
    emit('messages:changed', { accountId: 'a' }) // something else changed (another device)
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).toHaveBeenCalledTimes(1)
    app.unmount()
  })

  it('an echo that never came doesn’t swallow a later change', async () => {
    const s = spies()
    const { app, mail, emit } = await withEvents()
    mail.messages = [row('m1'), row('m2')]
    mail.status = 'ready'
    await mail.setStarred(['m1'], true)
    await vi.advanceTimersByTimeAsync(1500)
    emit('messages:changed', { accountId: 'a' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).toHaveBeenCalledTimes(1)
    app.unmount()
  })

  it('refetches when the change can move rows in or out of the view', async () => {
    const s = spies()
    const { app, mail, emit } = await withEvents()
    mail.messages = [row('m1'), row('m2')]
    mail.status = 'ready'
    mail.unreadOnly = true
    await vi.advanceTimersByTimeAsync(400) // the unread-only list loads
    s.list.mockClear()
    mail.messages = [row('m1'), row('m2')]
    await mail.setRead(['m1'], true)
    emit('messages:changed', { accountId: 'a' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).toHaveBeenCalledTimes(1)
    // A failed change has no echo either.
    mail.unreadOnly = false
    await vi.advanceTimersByTimeAsync(400)
    s.list.mockClear()
    s.markRead.mockRejectedValueOnce(new Error('offline'))
    await mail.setRead(['m2'], true)
    emit('messages:changed', { accountId: 'a' })
    await vi.advanceTimersByTimeAsync(400)
    expect(s.list).toHaveBeenCalledTimes(1)
    app.unmount()
  })
})
