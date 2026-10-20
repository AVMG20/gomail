// Regression tests for the UX pass (real-user annoyances).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const toasts: { kind: string; msg: string }[] = []
vi.mock('vue-sonner', () => {
  const rec = (kind: string) => (msg: string) => void toasts.push({ kind, msg })
  return { toast: Object.assign(rec('info'), { error: rec('error'), success: rec('success'), warning: rec('warning'), dismiss: () => {} }) }
})

import { api } from '@/lib/api'
import type { Message, MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { draftHasContent, useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'
import RecipientField from '@/components/compose/RecipientField.vue'

const row = (id: string, over: Partial<MessageSummary> = {}) =>
  ({ id, accountId: 'a', threadId: id, folderIds: ['inbox1'], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary
const msg = (id: string, over: Partial<Message> = {}) => ({ ...row(id), cc: [], bcc: [], replyTo: [], bodyHtml: '<p>Original text</p>', bodyText: 'Original text', attachments: [], ...over }) as unknown as Message

beforeEach(() => {
  localStorage.clear()
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function mountField() {
  let value: { name: string; email: string }[] = []
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
  app.use(createPinia())
  app.mount(el)
  return { input: el.querySelector('input')!, get value() { return value }, unmount: () => app.unmount() }
}

describe('writing', () => {
  it('pasting several addresses keeps all of them', async () => {
    const f = mountField()
    const paste = new Event('paste', { cancelable: true }) as ClipboardEvent
    Object.defineProperty(paste, 'clipboardData', { value: { getData: () => 'a@x.com, b@x.com\nc@x.com' } })
    f.input.dispatchEvent(paste)
    await nextTick()
    expect(f.value.map((a) => a.email)).toEqual(['a@x.com', 'b@x.com', 'c@x.com'])
    f.unmount()
  })

  it('a reply closed without changes is not saved as a draft', async () => {
    const compose = useComposeStore()
    const save = vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:1')
    const d = (await compose.respond('reply', msg('m1')))!
    expect(draftHasContent(d)).toBe(false)
    expect(await compose.saveAndClose(d.id)).toBe(true)
    expect(save).not.toHaveBeenCalled()
    expect(compose.drafts).toHaveLength(0)
  })

  it('a reply the user wrote in is still saved', async () => {
    const compose = useComposeStore()
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const save = vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:1')
    const d = (await compose.respond('reply', msg('m1')))!
    d.html = `<p>Thanks!</p>${d.html}`
    expect(await compose.saveAndClose(d.id)).toBe(true)
    expect(save).toHaveBeenCalledTimes(1)
  })

  it('a failed save & close keeps the message open instead of offering to discard it', async () => {
    const compose = useComposeStore()
    vi.spyOn(api, 'saveDraft').mockRejectedValue(new Error('offline'))
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Important'
    expect(await compose.saveAndClose(d.id)).toBe(true) // true: no "Discard?" prompt
    expect(compose.drafts.some((x) => x.id === d.id)).toBe(true)
    expect(toasts.some((t) => t.kind === 'error')).toBe(true)
  })

  it('refuses to send more than 25 MB of attachments up front', async () => {
    const compose = useComposeStore()
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.attachments.push({ id: 'f', filename: 'big.zip', mimeType: 'application/zip', size: 26 * 1024 * 1024, dataBase64: 'AA==' })
    await compose.send(d.id, 0)
    expect(send).not.toHaveBeenCalled()
    expect(toasts.some((t) => t.kind === 'error' && t.msg.includes('25 MB'))).toBe(true)
  })

  it('"Email guests" writes from the event’s mailbox', () => {
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'gmail', email: 'me@gmail.com' }, { id: 'outlook', email: 'me@outlook.com' }] as never
    const compose = useComposeStore()
    const d = compose.composeWith({ accountId: 'outlook', to: [{ name: '', email: 'g@x.io' }] })
    expect(d.accountId).toBe('outlook')
  })
})

describe('keyboard safety after compose', () => {
  it('Backspace/Delete are ignored right after a compose window closes or minimises', () => {
    vi.useFakeTimers()
    const ui = useUiStore()
    const compose = useComposeStore()
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    compose.minimize(d.id)
    expect(d.minimized).toBe(true)
    vi.advanceTimersByTime(200)
    expect(ui.keyGuarded('Backspace')).toBe(true)
    expect(ui.keyGuarded('Delete')).toBe(true)
    expect(ui.keyGuarded('j')).toBe(false)
    vi.advanceTimersByTime(2000)
    expect(ui.keyGuarded('Backspace')).toBe(false)
  })
})

describe('triage', () => {
  function setup(n = 5) {
    const mail = useMailStore()
    mail.messages = Array.from({ length: n }, (_, i) => row(`m${i}`))
    return mail
  }

  it('shift-click on a selected row deselects the range', () => {
    const mail = setup()
    mail.toggleSelect('m0')
    mail.toggleSelect('m3', { range: true })
    expect([...mail.selection].sort()).toEqual(['m0', 'm1', 'm2', 'm3'])
    mail.toggleSelect('m1')
    mail.toggleSelect('m3', { range: true }) // m3 is selected: the range m1..m3 goes off
    expect([...mail.selection].sort()).toEqual(['m0'])
  })

  it('Move to offers Undo back to the folder it came from', async () => {
    const accounts = useAccountsStore()
    accounts.folders = { a: [{ id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox' }, { id: 'proj', accountId: 'a', kind: 'custom', name: 'Projects' }] } as never
    const mail = useMailStore()
    vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
    vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
    mail.setMailbox({ accountId: 'a', folderId: 'inbox1', kind: 'inbox' })
    mail.messages = [row('m1')]
    const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
    const calls: { msg: string; opts?: { action?: { onClick: () => unknown } } }[] = []
    const sonner = await import('vue-sonner')
    vi.spyOn(sonner, 'toast').mockImplementation(((m: string, o?: never) => void calls.push({ msg: m, opts: o })) as never)
    await mail.moveToFolder(['m1'], 'proj')
    const undo = calls.find((c) => c.opts?.action)?.opts?.action?.onClick
    expect(undo).toBeTruthy()
    await undo!()
    expect(move.mock.calls.map((c) => c[1])).toEqual(['proj', 'inbox1'])
  })

  it('marking read updates the folder counter right away', async () => {
    const accounts = useAccountsStore()
    accounts.folders = { a: [{ id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox', unread: 5, total: 9 }] } as never
    const mail = setup(2)
    vi.spyOn(api, 'markRead').mockReturnValue(new Promise(() => {}))
    void mail.setRead(['m0', 'm1'], true)
    expect(accounts.folderMap.get('inbox1')?.unread).toBe(3)
  })

  it('the header unread count follows a label filter', async () => {
    const mail = useMailStore()
    const list = vi.spyOn(api, 'listMessages').mockImplementation(async (q) => ({ messages: [], nextCursor: '', total: q.unreadOnly ? 4 : 30, hasMoreRemote: false }))
    mail.setLabelFilter({ labels: ['work'], unlabeled: false })
    await mail.reload()
    await Promise.resolve()
    await Promise.resolve()
    expect(mail.viewUnread).toBe(4)
    expect(list.mock.calls.some((c) => c[0].unreadOnly && c[0].labels?.includes('work'))).toBe(true)
  })
})
