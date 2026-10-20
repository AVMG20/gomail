// Regression tests for the mail audit (compose, list and settings fixes).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const toasts: { kind: string; msg: string; opts?: { action?: { onClick: () => unknown } } }[] = []
vi.mock('vue-sonner', () => {
  const rec = (kind: string) => (msg: string, opts?: never) => {
    toasts.push({ kind, msg, opts })
  }
  const toast = Object.assign(rec('info'), { error: rec('error'), success: rec('success'), warning: rec('warning'), info: rec('info'), dismiss: () => {} })
  return { toast }
})

import { api } from '@/lib/api'
import { parseAddressList, splitAddressList } from '@/lib/format'
import { applyCompletion } from '@/lib/searchQuery'
import type { Message, MessagePage, MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { ACTION_CHUNK, useMailStore } from '@/stores/mail'
import RecipientField from '@/components/compose/RecipientField.vue'

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}
const row = (id: string, over: Partial<MessageSummary> = {}) =>
  ({ id, accountId: 'a', threadId: id, folderIds: ['inbox1'], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary
const page = (rows: MessageSummary[], nextCursor = '', total = rows.length): MessagePage => ({ messages: rows, nextCursor, total, hasMoreRemote: false })

beforeEach(() => {
  localStorage.clear()
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('recipients', () => {
  it('keeps "$&" in completions literal', () => {
    expect(applyCompletion('label:wo', 'label:R&$&D')).toBe('label:R&$&D ')
  })

  it('does not split quoted display names on commas', () => {
    expect(parseAddressList('"Doe, John" <john@x.com>; ann@x.com')).toEqual([{ name: 'Doe, John', email: 'john@x.com' }, { name: '', email: 'ann@x.com' }])
    expect(splitAddressList('a@x.com,\n b@x.com ;')).toEqual(['a@x.com', 'b@x.com'])
  })

  it.each([['anna@acme.com', 'anna@acme.com'], ['Anna Smith <anna@acme.com>', 'anna@acme.com'], ['"Doe, John" <john@x.com>', 'john@x.com']])('turns typed %s into a chip on blur', async (typed, email) => {
    vi.useFakeTimers()
    let value: { name: string; email: string }[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
    app.use(createPinia())
    app.mount(el)
    const input = el.querySelector('input')!
    input.focus()
    input.value = typed
    input.dispatchEvent(new Event('input'))
    await nextTick()
    input.dispatchEvent(new Event('blur'))
    input.dispatchEvent(new Event('focusout'))
    await vi.advanceTimersByTimeAsync(300)
    expect(value.map((a) => a.email)).toEqual([email])
    app.unmount()
  })

  it('commits typed text immediately when asked (Send clicked before the blur timer)', async () => {
    let value: { name: string; email: string }[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
    app.use(createPinia())
    const vm = app.mount(el) as unknown as { commit: () => boolean }
    const input = el.querySelector('input')!
    input.value = 'Anna Smith <anna@acme.com>'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    vm.commit()
    expect(value).toEqual([{ name: 'Anna Smith', email: 'anna@acme.com' }])
    app.unmount()
  })
})

describe('drafts', () => {
  it('reopening a draft keeps its attachments in the next autosave', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    const save = vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:1')
    const data = vi.spyOn(api, 'getAttachmentData').mockResolvedValue({ filename: 'invoice.pdf', mimeType: 'application/pdf', size: 3, dataBase64: 'UERG' })
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const m = {
      ...row('d1'), cc: [], bcc: [], replyTo: [], to: [{ name: '', email: 'to@x.io' }], bodyHtml: '<p>hi <img src="cid:logo"></p>', bodyText: 'hi',
      attachments: [
        { id: '2', filename: 'invoice.pdf', mimeType: 'application/pdf', size: 3, inline: false, contentId: '' },
        { id: '3', filename: 'logo.png', mimeType: 'image/png', size: 9, inline: true, contentId: 'logo' },
      ],
    } as unknown as Message
    await compose.editDraft(m)
    expect(data).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5000)
    expect(save).toHaveBeenCalled()
    expect(save.mock.calls[0][0].attachments).toEqual([{ filename: 'invoice.pdf', mimeType: 'application/pdf', dataBase64: 'UERG' }])
  })

  it('does not open (and overwrite) a draft whose attachments could not be loaded', async () => {
    const compose = useComposeStore()
    vi.spyOn(api, 'getAttachmentData').mockRejectedValue(new Error('offline'))
    const m = { ...row('d1'), cc: [], bcc: [], replyTo: [], bodyHtml: '<p>hi</p>', bodyText: 'hi', attachments: [{ id: '2', filename: 'a.pdf', mimeType: 'application/pdf', size: 3, inline: false, contentId: '' }] } as unknown as Message
    await compose.editDraft(m)
    expect(compose.drafts).toHaveLength(0)
    expect(toasts.some((t) => t.kind === 'error')).toBe(true)
  })

  it('discard waits for the first server save in flight and deletes what it created', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    const gate = deferred<string>()
    vi.spyOn(api, 'saveDraft').mockReturnValue(gate.promise)
    const del = vi.spyOn(api, 'deleteDraft').mockResolvedValue(undefined)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Hi'
    await vi.advanceTimersByTimeAsync(3800) // first save in flight
    const done = compose.discard(d.id)
    expect(compose.drafts).toHaveLength(0) // the window closes right away
    gate.resolve('draft:a:orphan')
    await done
    expect(del).toHaveBeenCalledWith('draft:a:orphan')
  })

  it('save & close waits for the first server save instead of creating a second draft', async () => {
    vi.useFakeTimers()
    const compose = useComposeStore()
    const gate = deferred<string>()
    const save = vi.spyOn(api, 'saveDraft').mockReturnValueOnce(gate.promise).mockResolvedValue('draft:a:1')
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Hi'
    await vi.advanceTimersByTimeAsync(3800)
    const closing = compose.saveAndClose(d.id)
    gate.resolve('draft:a:1')
    expect(await closing).toBe(true)
    expect(save.mock.calls.map((c) => c[1])).toEqual(['', 'draft:a:1'])
  })

  it('never drops a draft that is being sent to make room in the dock', () => {
    const compose = useComposeStore()
    const first = compose.compose()
    first.subject = 'Sending now'
    first.sending = true
    for (let i = 0; i < 4; i++) compose.compose().subject = `n${i}`
    expect(compose.drafts.some((x) => x.id === first.id)).toBe(true)
  })

  it('shows the original files on a forward', async () => {
    const compose = useComposeStore()
    const m = { ...row('m1'), cc: [], bcc: [], replyTo: [], bodyHtml: '<p>x</p>', bodyText: 'x',
      attachments: [{ id: '1', filename: 'a.pdf', mimeType: 'application/pdf', size: 5, inline: true, contentId: '' }, { id: '2', filename: 'logo.png', mimeType: 'image/png', size: 1, inline: true, contentId: 'l' }] } as unknown as Message
    await compose.respond('forward', m)
    expect(compose.drafts[0].forwarded).toEqual([{ filename: 'a.pdf', size: 5 }])
  })
})

describe('mail list', () => {
  function inboxSetup() {
    const accounts = useAccountsStore()
    accounts.folders = { a: [
      { id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox' },
      { id: 'sent1', accountId: 'a', kind: 'sent', name: 'Sent' },
      { id: 'trash1', accountId: 'a', kind: 'trash', name: 'Trash' },
    ] } as never
    const mail = useMailStore()
    vi.spyOn(api, 'listMessages').mockResolvedValue(page([]))
    mail.setMailbox({ accountId: 'a', folderId: 'inbox1', kind: 'inbox' })
    return mail
  }

  it('undo sends messages back to where they were, not to the folder shown now', async () => {
    const mail = inboxSetup()
    vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
    vi.spyOn(api, 'archive').mockResolvedValue(undefined)
    const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
    mail.messages = [row('m1')]
    await mail.archive(['m1'])
    const undo = toasts.find((t) => t.opts?.action)!.opts!.action!.onClick
    mail.setMailbox({ accountId: 'a', folderId: 'sent1', kind: 'sent' })
    mail.messages = []
    await undo()
    expect(move).toHaveBeenCalledWith(['m1'], 'inbox1')
    expect(mail.messages).toEqual([]) // no phantom row in Sent
  })

  it('# in Trash does not pretend to move anything', async () => {
    const mail = inboxSetup()
    mail.setMailbox({ accountId: 'a', folderId: 'trash1', kind: 'trash' })
    const trash = vi.spyOn(api, 'trash').mockResolvedValue(undefined)
    mail.messages = [row('m1')]
    await mail.trash(['m1'])
    expect(trash).not.toHaveBeenCalled()
    expect(toasts.some((t) => /Already in Trash/.test(t.msg))).toBe(true)
  })

  it('soft refresh keeps rows loaded beyond the backend page size', async () => {
    const mail = inboxSetup()
    await mail.reload()
    const rows = Array.from({ length: 300 }, (_, i) => row(`m${String(i).padStart(3, '0')}`))
    mail.messages = rows
    const list = vi.spyOn(api, 'listMessages').mockImplementation(async (q) => {
      const start = q.cursor ? Number(q.cursor) : 0
      const n = Math.min(q.limit ?? 50, 200) // the backend clamps
      return page(rows.slice(start, start + n), start + n < 400 ? String(start + n) : '', 400)
    })
    await mail.softRefresh()
    expect(mail.messages).toHaveLength(300)
    expect(list).toHaveBeenCalledTimes(2)
    expect(mail.nextCursor).toBe('300')
  })

  it('select all acts on every message, in chunks the backend accepts', async () => {
    const mail = inboxSetup()
    const all = Array.from({ length: 7539 }, (_, i) => `m${i}`)
    vi.spyOn(api, 'resolveQueryIds').mockResolvedValue(all)
    const archive = vi.spyOn(api, 'archive').mockResolvedValue(undefined)
    vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
    mail.selectAllQuery = true
    const ids = await mail.targetIds()
    await mail.archive(ids)
    expect(archive.mock.calls.flatMap((c) => c[0])).toHaveLength(7539)
    expect(Math.max(...archive.mock.calls.map((c) => c[0].length))).toBeLessThanOrEqual(ACTION_CHUNK)
  })
})

describe('calendar meeting links', () => {
  it('sends the removal of a cleared meeting link', async () => {
    const { toWireInput } = await import('@/lib/api')
    const wire = await toWireInput({ calendarId: 'c', title: 't', start: '2026-10-20T10:00:00Z', end: '2026-10-20T11:00:00Z', allDay: false, timeZone: '',
      location: '', description: '', attendees: [], meetingUrl: '', removeMeetingUrl: true, recurrence: '', reminders: [] })
    expect(wire).toMatchObject({ meetingUrl: '', removeMeetingUrl: true })
  })
})
