// Opening mail from ⌘K / notifications, sync toasts, the thread cache and list focus.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import type { EventName, EventPayloads, Message, MessagePage, MessageSummary, Settings } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

const { toast } = vi.hoisted(() => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() }) }))
vi.mock('vue-sonner', () => ({ toast }))

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}
const row = (id: string, over: Partial<MessageSummary> = {}) => ({ id, accountId: 'a', threadId: id, folderIds: ['a:inbox'], from: { name: 'Alex', email: 'alex@example.com' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary
const msg = (id: string, over: Partial<Message> = {}) => ({ ...row(id), cc: [], bcc: [], replyTo: [], bodyHtml: '', bodyText: '', ...over }) as Message
const page = (rows: MessageSummary[]): MessagePage => ({ messages: rows, nextCursor: '', total: rows.length, hasMoreRemote: false })
const flush = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

function setupFolders() {
  const accounts = useAccountsStore()
  accounts.accounts = [{ id: 'a', email: 'alex@example.com' }, { id: 'b', email: 'sam@example.com' }] as never
  accounts.folders = {
    a: [{ id: 'a:inbox', accountId: 'a', kind: 'inbox', name: 'Inbox', unread: 3, total: 9 }, { id: 'a:archive', accountId: 'a', kind: 'archive', name: 'Archive', unread: 0, total: 9 }],
    b: [{ id: 'b:inbox', accountId: 'b', kind: 'inbox', name: 'Inbox', unread: 1, total: 9 }],
  } as never
  return accounts
}
function readMode(mode: 'immediate' | 'delay' | 'manual') {
  useSettingsStore().settings = { markReadMode: mode } as Settings
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  toast.mockClear()
  toast.error.mockClear()
})
afterEach(() => vi.useRealTimers())

describe('opening mail that is not in the list yet', () => {
  it('marks it read once the new mailbox list is back, and j/k go on from it', async () => {
    setupFolders()
    readMode('immediate')
    const mail = useMailStore()
    mail.mailbox = { accountId: 'a', folderId: 'a:archive', kind: 'archive' }
    mail.messages = [row('old', { unread: false })]
    mail.status = 'ready'
    const list = deferred<MessagePage>()
    vi.spyOn(api, 'listMessages').mockReturnValueOnce(list.promise).mockResolvedValue(page([]))
    vi.spyOn(api, 'getThread').mockResolvedValue([msg('x')])
    const markRead = vi.spyOn(api, 'markRead').mockResolvedValue()
    const opened = mail.reveal('m2', { accountId: 'a', folderId: 'a:inbox', kind: 'inbox' })
    await flush()
    expect(mail.openId).toBe('m2') // shown right away
    expect(markRead).not.toHaveBeenCalled() // waits for the list instead of racing it
    list.resolve(page([row('m1'), row('m2'), row('m3')]))
    await opened
    expect(markRead).toHaveBeenCalledWith(['m2'], true)
    expect(mail.byId.get('m2')?.unread).toBe(false)
    mail.move(1)
    expect(mail.openId).toBe('m3')
  })

  it('uses the message’s own data when it isn’t in the loaded list', async () => {
    setupFolders()
    readMode('immediate')
    const mail = useMailStore()
    mail.status = 'ready'
    vi.spyOn(api, 'getThread').mockResolvedValue([msg('far', { unread: true })])
    const markRead = vi.spyOn(api, 'markRead').mockResolvedValue()
    mail.open('far')
    await flush()
    expect(markRead).toHaveBeenCalledWith(['far'], true)
    expect(mail.cachedThread('far')?.[0].unread).toBe(false)
  })

  it('leaves already-read unlisted mail alone, and respects manual and delayed modes', async () => {
    vi.useFakeTimers()
    setupFolders()
    const mail = useMailStore()
    mail.status = 'ready'
    vi.spyOn(api, 'getThread').mockImplementation(async (id) => [msg(id, { unread: id !== 'seen' })])
    const markRead = vi.spyOn(api, 'markRead').mockResolvedValue()
    readMode('immediate')
    mail.open('seen')
    await flush()
    expect(markRead).not.toHaveBeenCalled()
    readMode('manual')
    mail.open('m1')
    await vi.advanceTimersByTimeAsync(3000)
    expect(markRead).not.toHaveBeenCalled()
    readMode('delay')
    mail.open('m2')
    await flush()
    expect(markRead).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(2100)
    expect(markRead).toHaveBeenCalledWith(['m2'], true)
  })
})

describe('opening new mail', () => {
  it('stays in All inboxes for inbox mail', async () => {
    setupFolders()
    readMode('manual')
    const mail = useMailStore()
    mail.mailbox = { accountId: '', folderId: '', kind: 'inbox' }
    mail.messages = [row('n1', { accountId: 'b', folderIds: ['b:inbox'] })]
    mail.status = 'ready'
    await mail.revealNew('b', 'n1', ['b:inbox'])
    expect(mail.mailbox).toEqual({ accountId: '', folderId: '', kind: 'inbox' })
    expect(mail.openId).toBe('n1')
  })

  it('goes to the mailbox’s Inbox, not the folder in use', async () => {
    setupFolders()
    readMode('manual')
    const mail = useMailStore()
    mail.mailbox = { accountId: 'b', folderId: 'b:archive', kind: 'archive' }
    vi.spyOn(api, 'listMessages').mockResolvedValue(page([row('n2')]))
    await mail.revealNew('a', 'n2')
    expect(mail.mailbox).toEqual({ accountId: 'a', folderId: 'a:inbox', kind: 'inbox' })
    expect(mail.openId).toBe('n2')
    expect(mail.byId.has('n2')).toBe(true)
  })
})

describe('switching mailboxes', () => {
  it('stays on the same kind of folder', () => {
    setupFolders()
    const mail = useMailStore()
    mail.setMailbox({ accountId: 'b', folderId: 'b:inbox', kind: 'inbox' })
    mail.setMailbox({ accountId: 'a', folderId: 'a:archive', kind: 'archive' })
    mail.switchAccount('')
    expect(mail.mailbox).toEqual({ accountId: '', folderId: '', kind: 'archive' })
    mail.switchAccount('a')
    expect(mail.mailbox).toEqual({ accountId: 'a', folderId: 'a:archive', kind: 'archive' })
    mail.switchAccount('b') // no Archive there
    expect(mail.mailbox).toEqual({ accountId: 'b', folderId: 'b:inbox', kind: 'inbox' })
  })

  it('leaves a folder of its own for the Inbox', () => {
    setupFolders()
    const mail = useMailStore()
    mail.setMailbox({ accountId: 'a', folderId: 'a:work', kind: 'user' })
    mail.switchAccount('b')
    expect(mail.mailbox).toEqual({ accountId: 'b', folderId: 'b:inbox', kind: 'inbox' })
  })
})

describe('thread cache', () => {
  it('evicts the least recently used conversation', async () => {
    const mail = useMailStore()
    const get = vi.spyOn(api, 'getThread').mockImplementation(async (id) => [msg(id)])
    for (let i = 0; i < 40; i++) await mail.loadThread(`t${i}`)
    await mail.loadThread('t0') // used again
    await mail.loadThread('t40')
    expect(mail.cachedThread('t0')).toBeTruthy()
    expect(mail.cachedThread('t1')).toBeUndefined()
    expect(get).toHaveBeenCalledTimes(41)
  })

  it('keeps threads whose rows didn’t change when mail changes', async () => {
    const mail = useMailStore()
    mail.messages = [row('m1'), row('m2'), row('m3')]
    mail.status = 'ready'
    vi.spyOn(api, 'getThread').mockImplementation(async (id) => [msg(id)])
    await Promise.all(['m1', 'm2', 'm3'].map((id) => mail.loadThread(id)))
    mail.threadsChanged({ accountId: 'a' })
    vi.spyOn(api, 'listMessages').mockResolvedValue(page([row('m1'), row('m2', { starred: true })]))
    await mail.softRefresh()
    expect(mail.cachedThread('m1')).toBeTruthy() // unchanged: no refetch when you go back to it
    expect(mail.cachedThread('m2')).toBeUndefined() // changed elsewhere
    expect(mail.cachedThread('m3')).toBeUndefined() // left the list: can't tell
  })

  it('drops only the threads of the changed messages when the event names them', async () => {
    const mail = useMailStore()
    vi.spyOn(api, 'getThread').mockImplementation(async (id) => (id === 'm1' ? [msg('m0'), msg('m1')] : [msg(id)]))
    await mail.loadThread('m1')
    await mail.loadThread('m2')
    mail.threadsChanged({ accountId: 'a', ids: ['m0'] })
    expect(mail.cachedThread('m1')).toBeUndefined()
    expect(mail.cachedThread('m2')).toBeTruthy()
  })

  it('a load that was invalidated midway is returned but not cached', async () => {
    const mail = useMailStore()
    const slow = deferred<Message[]>()
    vi.spyOn(api, 'getThread').mockReturnValueOnce(slow.promise).mockResolvedValue([msg('m1', { subject: 'fresh' })])
    const first = mail.loadThread('m1')
    mail.invalidateThread('m1')
    const second = mail.loadThread('m1')
    slow.resolve([msg('m1', { subject: 'stale' })])
    expect((await first)[0].subject).toBe('stale')
    expect((await second)[0].subject).toBe('fresh')
    expect(mail.cachedThread('m1')?.[0].subject).toBe('fresh')
  })

  it('prefetches the next row once the reader rests on a message, one request at a time', async () => {
    vi.useFakeTimers()
    readMode('manual')
    const mail = useMailStore()
    mail.messages = [row('m1'), row('m2'), row('m3'), row('m4')]
    mail.status = 'ready'
    const get = vi.spyOn(api, 'getThread').mockImplementation(async (id) => [msg(id)])
    mail.open('m1')
    mail.move(1) // holding j
    mail.move(1)
    await vi.advanceTimersByTimeAsync(400)
    expect(get.mock.calls.map((c) => c[0])).toEqual(['m4'])
    expect(mail.cachedThread('m4')).toBeTruthy()
    await mail.loadThread('m4') // opening it needs no request
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('prefetches the message the selection moves to after archiving', async () => {
    vi.useFakeTimers()
    readMode('manual')
    const mail = useMailStore()
    mail.messages = [row('m1'), row('m2'), row('m3')]
    mail.status = 'ready'
    vi.spyOn(api, 'archive').mockResolvedValue()
    const get = vi.spyOn(api, 'getThread').mockImplementation(async (id) => [msg(id)])
    mail.openId = 'm1'
    await mail.archive(['m1'])
    await vi.advanceTimersByTimeAsync(400)
    expect(mail.openId).toBe('m2')
    expect(get.mock.calls.map((c) => c[0])).toEqual(['m3'])
  })
})

describe('sync toasts', () => {
  type Handler = (p: unknown) => void
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
    const accounts = setupFolders()
    app.mount(document.createElement('div'))
    const finished = (accountId: string, error: string) => handlers.get('sync:finished')!({ accountId, newMessages: 0, error })
    return { app, accounts, finished }
  }

  it('background errors that fix themselves stay out of toasts', async () => {
    const { app, finished } = await withEvents()
    finished('a', 'Gmail took too long to respond. gomail will try again.')
    finished('a', 'You’re offline. gomail will sync again when the connection is back.')
    finished('b', "You're offline. gomail will sync again when the connection is back.")
    finished('a', 'Gmail is limiting requests right now. gomail will try again later.')
    finished('a', 'The sign-in for alex@example.com has expired or was revoked. Sign in again to keep syncing.')
    expect(toast.error).not.toHaveBeenCalled()
    app.unmount()
  })

  it('a new error that needs the user toasts once', async () => {
    const { app, finished } = await withEvents()
    const full = "Your disk is full, so gomail can't save mail. Free up some space; syncing resumes automatically."
    finished('a', full)
    finished('a', full)
    finished('a', 'Gmail took too long to respond. gomail will try again.')
    finished('a', full)
    expect(toast.error).toHaveBeenCalledTimes(1)
    finished('a', '')
    finished('a', full) // came back after a good round
    expect(toast.error).toHaveBeenCalledTimes(2)
    app.unmount()
  })

  it('“Sync now” reports its outcome, once for all mailboxes while offline', async () => {
    const { app, accounts, finished } = await withEvents()
    vi.spyOn(api, 'syncNow').mockResolvedValue()
    await accounts.sync('')
    finished('a', "You're offline. gomail will sync again when the connection is back.")
    finished('b', "You're offline. gomail will sync again when the connection is back.")
    const ids = toast.error.mock.calls.map((c) => (c[1] as { id?: string })?.id)
    expect(new Set(ids)).toEqual(new Set(['sync-offline'])) // one toast, replaced in place
    finished('a', 'Gmail took too long to respond. gomail will try again.') // a later background round
    expect(toast.error).toHaveBeenCalledTimes(2)
    await accounts.sync('a')
    finished('a', 'Gmail took too long to respond. gomail will try again.')
    expect(toast.error).toHaveBeenCalledTimes(3)
    app.unmount()
  })

  it('a “Sync now” that fails before its round starts says so once, and never throws', async () => {
    const { app, accounts } = await withEvents()
    vi.spyOn(api, 'syncNow').mockRejectedValue(new Error('account not found'))
    await expect(accounts.sync('a')).resolves.toBe('Account not found')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error.mock.calls[0][0]).toBe('Sync failed for alex@example.com')
    expect(toast.error.mock.calls[0][1]).toMatchObject({ description: 'Account not found' })
    expect(accounts.syncing.has('a')).toBe(false)
    vi.spyOn(api, 'syncNow').mockRejectedValue(new Error("You're offline. gomail will sync again when the connection is back."))
    await accounts.sync('')
    expect(toast.error).toHaveBeenCalledTimes(2)
    expect(toast.error.mock.calls[1]).toMatchObject(['Couldn’t sync', { id: 'sync-offline' }])
    app.unmount()
  })

  it('a round that failed and reported it is not reported again by the failed call', async () => {
    const { app, accounts, finished } = await withEvents()
    const full = "Your disk is full, so gomail can't save mail. Free up some space; syncing resumes automatically."
    vi.spyOn(api, 'syncNow').mockImplementation(async (id) => {
      finished(id, full) // the round's sync:finished comes before the call returns
      throw new Error(full)
    })
    await accounts.sync('a')
    expect(toast.error).toHaveBeenCalledTimes(1)
    expect(toast.error.mock.calls[0]).toMatchObject(['Sync failed for alex@example.com', { id: 'sync-failed:a', description: full }])
    app.unmount()
  })
})

describe('list search box', () => {
  it('Enter moves focus to the list so j/k work right away', async () => {
    vi.spyOn(api, 'listMessages').mockResolvedValue(page([row('m1')]))
    const pinia = createPinia()
    setActivePinia(pinia)
    const { default: MessageList } = await import('@/components/mail/MessageList.vue')
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp({ render: () => h(MessageList) })
    app.use(pinia)
    app.mount(el)
    for (let i = 0; i < 3; i++) await nextTick()
    const input = el.querySelector<HTMLInputElement>('input[type="search"]')!
    input.focus()
    input.value = 'invoice'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await nextTick()
    expect(useMailStore().search).toBe('invoice')
    expect(document.activeElement).toBe(el.querySelector('[role="listbox"]'))
    input.focus()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(useMailStore().search).toBe('')
    expect(document.activeElement).not.toBe(input)
    app.unmount()
  })
})
