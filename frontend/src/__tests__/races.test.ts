import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import type { MessagePage, MessageSummary, Settings } from '@/lib/types'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}
const row = (id: string, over: Partial<MessageSummary> = {}) => ({ id, accountId: 'a', threadId: id, folderIds: [], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary
const page = (rows: MessageSummary[]): MessagePage => ({ messages: rows, nextCursor: '', total: rows.length, hasMoreRemote: false })

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})
afterEach(() => vi.useRealTimers())

describe('mail list races', () => {
  it('drops a list response for a mailbox that is no longer shown', async () => {
    const mail = useMailStore()
    const first = deferred<MessagePage>()
    const second = deferred<MessagePage>()
    const list = vi.spyOn(api, 'listMessages').mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const a = mail.reload()
    const b = mail.reload()
    second.resolve(page([row('new')]))
    await b
    first.resolve(page([row('old')]))
    await a
    expect(list).toHaveBeenCalledTimes(2)
    expect(mail.messages.map((m) => m.id)).toEqual(['new'])
  })

  it('ignores a refresh that started before an optimistic update', async () => {
    vi.useFakeTimers()
    const mail = useMailStore()
    mail.messages = [row('m1')]
    mail.status = 'ready'
    const stale = deferred<MessagePage>()
    vi.spyOn(api, 'listMessages').mockReturnValueOnce(stale.promise).mockResolvedValue(page([row('m1', { starred: true })]))
    const refresh = mail.softRefresh()
    mail.patchLocal(['m1'], { starred: true }) // optimistic star while the refresh is in flight
    stale.resolve(page([row('m1', { starred: false })]))
    await refresh
    expect(mail.messages[0].starred).toBe(true)
    await vi.advanceTimersByTimeAsync(200) // the retry sees the server state
    expect(mail.messages[0].starred).toBe(true)
  })

  it('marks the auto-advanced message read after archiving the open one', async () => {
    const mail = useMailStore()
    mail.messages = [row('a', { unread: false }), row('b', { unread: true })]
    const markRead = vi.spyOn(api, 'markRead').mockResolvedValue(undefined)
    vi.spyOn(api, 'archive').mockResolvedValue(undefined)
    mail.openId = 'a'
    await mail.archive(['a'])
    await Promise.resolve()
    expect(mail.openId).toBe('b')
    expect(mail.messages.find((m) => m.id === 'b')?.unread).toBe(false)
    expect(markRead).toHaveBeenCalledWith(['b'], true)
  })

  it('rolls back a failed star per message', async () => {
    const mail = useMailStore()
    mail.messages = [row('a', { starred: true }), row('b', { starred: false })]
    vi.spyOn(api, 'setStarred').mockRejectedValue(new Error('offline'))
    await mail.setStarred(['a', 'b'], true)
    expect(mail.messages.map((m) => m.starred)).toEqual([true, false])
  })
})

describe('settings writes', () => {
  it('serialises saves, sends the latest state and resolves every caller', async () => {
    vi.useFakeTimers()
    const settings = useSettingsStore()
    settings.settings = { theme: 'light', density: 'comfortable' } as Settings
    const sent: string[] = []
    const gate = deferred<void>()
    vi.spyOn(api, 'saveSettings').mockImplementation(async (s) => {
      sent.push(s.theme)
      if (sent.length === 1) await gate.promise
    })
    const p1 = settings.update({ theme: 'dark' }, 100)
    const p2 = settings.update({ theme: 'system' }, 100) // replaces the pending debounce
    await vi.advanceTimersByTimeAsync(150)
    const p3 = settings.update({ density: 'compact' }, 100)
    await vi.advanceTimersByTimeAsync(150) // second save waits for the first
    expect(sent).toEqual(['system'])
    gate.resolve()
    await Promise.all([p1, p2, p3])
    expect(sent).toEqual(['system', 'system'])
    expect(settings.settings?.density).toBe('compact')
  })
})

describe('compose: autosave vs send', () => {
  it('waits for an in-flight server draft save so the send carries its id', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    const saveGate = deferred<string>()
    const order: string[] = []
    vi.spyOn(api, 'saveDraft').mockImplementation(async () => {
      order.push('save:start')
      const id = await saveGate.promise
      order.push('save:end')
      return id
    })
    const send = vi.spyOn(api, 'sendMessage').mockImplementation(async (req) => {
      order.push(`send:${req.draftId}`)
    })
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Hi'
    await vi.advanceTimersByTimeAsync(700) // persist → schedules the server save
    await vi.advanceTimersByTimeAsync(3100) // server save starts
    expect(order).toEqual(['save:start'])
    const sending = compose.send(d.id, 0)
    await vi.advanceTimersByTimeAsync(10)
    expect(send).not.toHaveBeenCalled()
    saveGate.resolve('draft:42')
    await sending
    expect(order).toEqual(['save:start', 'save:end', 'send:draft:42'])
  })

  it('moves the server draft when the From account changes', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    vi.spyOn(api, 'saveDraft').mockResolvedValueOnce('gmail-draft').mockResolvedValueOnce('outlook-draft')
    const del = vi.spyOn(api, 'deleteDraft').mockResolvedValue(undefined)
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.accountId = 'gmail'
    d.subject = 'Hi'
    await vi.advanceTimersByTimeAsync(4000) // saved in the Gmail account
    expect(d.remoteId).toBe('gmail-draft')
    d.accountId = 'outlook'
    await compose.send(d.id, 0)
    expect(del).toHaveBeenCalledWith('gmail-draft')
    expect(send.mock.calls[0][0]).toMatchObject({ accountId: 'outlook', draftId: '' })
  })

  it('persists a draft once per change, not in a loop', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:1')
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Hi'
    const writes = vi.spyOn(localStorage, 'setItem')
    await vi.advanceTimersByTimeAsync(10_000)
    const draftWrites = writes.mock.calls.filter(([k]) => k === 'gomail.drafts.v1').length
    expect(draftWrites).toBeGreaterThan(0)
    expect(draftWrites).toBeLessThanOrEqual(3) // initial + after the server id arrives
  })

  it('flags sends interrupted by closing the app', () => {
    localStorage.setItem('gomail.drafts.v1', JSON.stringify([{ id: 'q', mode: 'new', accountId: 'a', to: [{ name: '', email: 'a@b.io' }], cc: [], bcc: [], showCc: false, subject: 'Late', html: '', attachments: [], replyToId: '', minimized: false, expanded: false, sending: false, queued: true, version: 0 }]))
    setActivePinia(createPinia())
    const compose = useComposeStore()
    expect(compose.drafts[0]).toMatchObject({ id: 'q', queued: false, interrupted: true, minimized: true })
  })
})
