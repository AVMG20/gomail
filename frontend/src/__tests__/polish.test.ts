// Mail reading & composing polish: reply targets, subject prefixes, send warnings, triage.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const toasts: { kind: string; msg: string }[] = []
vi.mock('vue-sonner', () => {
  const rec = (kind: string) => (msg: string) => void toasts.push({ kind, msg })
  return { toast: Object.assign(rec('info'), { error: rec('error'), success: rec('success'), warning: rec('warning'), dismiss: () => {} }) }
})

import { api } from '@/lib/api'
import { signatureBlock } from '@/lib/format'
import type { Message, MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { ownText, prefixSubject, replyTarget, sendWarnings } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import RecipientField from '@/components/compose/RecipientField.vue'

const row = (id: string, over: Partial<MessageSummary> = {}) =>
  ({ id, accountId: 'a', threadId: id, folderIds: ['inbox1'], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: true, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary
const msg = (id: string, over: Partial<Message> = {}) => ({ ...row(id), cc: [], bcc: [], replyTo: [], bodyHtml: '', bodyText: '', attachments: [], ...over }) as unknown as Message

beforeEach(() => {
  localStorage.clear()
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => vi.restoreAllMocks())

describe('reply target', () => {
  const isDraft = (m: Message) => !!m.isDraft
  it('skips an autosaved draft at the end of the conversation', () => {
    const t = [msg('a'), msg('b'), msg('d', { isDraft: true })]
    expect(replyTarget(t, isDraft)?.id).toBe('b')
  })
  it('answers the preferred message when it is in the thread and not a draft', () => {
    const t = [msg('a'), msg('b'), msg('c')]
    expect(replyTarget(t, isDraft, 'a')?.id).toBe('a')
    expect(replyTarget(t, isDraft, 'gone')?.id).toBe('c')
  })
  it('falls back to the last message when every one is a draft', () => {
    expect(replyTarget([msg('d', { isDraft: true })], isDraft)?.id).toBe('d')
  })
})

describe('subject prefixes', () => {
  it('adds the prefix once and keeps other ones', () => {
    expect(prefixSubject('Re', 'Plans')).toBe('Re: Plans')
    expect(prefixSubject('Re', 'RE: Plans')).toBe('RE: Plans')
    expect(prefixSubject('Re', 'AW: Plans')).toBe('AW: Plans')
    expect(prefixSubject('Re', 'Fwd: Plans')).toBe('Re: Fwd: Plans')
    expect(prefixSubject('Fwd', 'Re: Plans')).toBe('Fwd: Re: Plans')
    expect(prefixSubject('Fwd', 'Fw: Plans')).toBe('Fw: Plans')
    expect(prefixSubject('Re', 'Reply guide')).toBe('Re: Reply guide')
    expect(prefixSubject('Re', '(no subject)')).toBe('Re:')
  })
})

describe('send warnings', () => {
  const draft = (over: Record<string, unknown> = {}) => ({ subject: 'Hi', html: '<p>Hello</p>', attachments: [], ...over }) as Parameters<typeof sendWarnings>[0]
  it('the user’s own text, without signature and quote', () => {
    expect(ownText(`<p>Mine</p>${signatureBlock('<p>Sig attached</p>')}<p>On x wrote:</p><blockquote><p>Theirs</p></blockquote>`)).toBe('Mine On x wrote:')
  })
  it('asks when the text mentions an attachment and none is attached', () => {
    expect(sendWarnings(draft({ html: '<p>See the attached file</p>' }))).toEqual(['attachment'])
    expect(sendWarnings(draft({ html: '<p>Zie bijlage</p>' }))).toEqual(['attachment'])
    expect(sendWarnings(draft({ html: '<p>See the attached file</p>', attachments: [{ id: 'f' }] }))).toEqual([])
    expect(sendWarnings(draft({ html: '<p>Here it is</p>', forwarded: [{ filename: 'a.pdf', size: 1 }] }))).toEqual([])
  })
  it('never because of the quoted message or the subject', () => {
    expect(sendWarnings(draft({ subject: 'Re: Attached invoice', html: '<p>Thanks!</p><blockquote>Invoice attached</blockquote>' }))).toEqual([])
  })
  it('asks about an empty subject', () => {
    expect(sendWarnings(draft({ subject: '  ' }))).toEqual(['subject'])
  })
})

describe('triage', () => {
  function setup() {
    const accounts = useAccountsStore()
    accounts.folders = {
      a: [{ id: 'inbox1', accountId: 'a', kind: 'inbox', name: 'Inbox' }, { id: 'junk1', accountId: 'a', kind: 'spam', name: 'Spam' }],
      b: [{ id: 'inbox2', accountId: 'b', kind: 'inbox', name: 'Inbox' }, { id: 'junk2', accountId: 'b', kind: 'spam', name: 'Junk' }],
    } as never
    const mail = useMailStore()
    vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
    vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
    return mail
  }

  it('Not junk sends each message to its own account’s Inbox', async () => {
    const mail = setup()
    mail.setMailbox({ accountId: '', folderId: '', kind: 'spam' })
    mail.messages = [row('m1', { folderIds: ['junk1'] }), row('m2', { accountId: 'b', folderIds: ['junk2'] })]
    // NotJunk moves to the message's own account's Inbox (and keeps Jev from junking it again).
    const notJunk = vi.spyOn(api, 'notJunk').mockResolvedValue({ count: 1, rules: [], more: [] })
    await mail.notJunk(['m1', 'm2'])
    expect(notJunk.mock.calls.map((c) => c[0])).toEqual([['m1'], ['m2']])
    expect(mail.messages).toEqual([])
  })

  it('stars unless every target is starred', () => {
    const mail = setup()
    mail.messages = [row('m1', { starred: true }), row('m2')]
    expect(mail.shouldStar(['m1', 'm2'])).toBe(true)
    expect(mail.shouldStar(['m1'])).toBe(false)
  })

  it('keyboard actions apply to the selection, else the open message', async () => {
    const mail = setup()
    mail.messages = [row('m1'), row('m2'), row('m3')]
    mail.open('m3')
    const seen: string[][] = []
    mail.toggleSelect('m1')
    mail.toggleSelect('m2')
    await mail.actOnTargets((ids) => void seen.push(ids))
    expect(mail.selection.size).toBe(0)
    await mail.actOnTargets((ids) => void seen.push(ids))
    expect(seen).toEqual([['m1', 'm2'], ['m3']])
  })

  it('shortcuts act on the hovered row, unless j/k moved since the pointer last did', async () => {
    const mail = setup()
    mail.messages = [row('m1'), row('m2'), row('m3')]
    mail.open('m1')
    const seen: string[][] = []
    const act = () => mail.actOnTargets((ids) => void seen.push(ids), { hovered: true })
    mail.pointAt('m3', true)
    await act()
    // The palette keeps acting on the open message.
    await mail.actOnTargets((ids) => void seen.push(ids))
    mail.move(1)
    mail.pointAt('m3', false) // a row slid under the resting pointer
    await act()
    mail.pointAt('m3', true)
    mail.toggleSelect('m1')
    await act()
    expect(seen).toEqual([['m3'], ['m1'], ['m2'], ['m1']])
  })

  it('a move to another mailbox’s folder leaves that mail where it is, and says so', async () => {
    const mail = setup()
    mail.setMailbox({ accountId: '', folderId: '', kind: 'inbox' })
    mail.messages = [row('m1'), row('m2', { accountId: 'b', folderIds: ['inbox2'] })]
    const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
    await mail.moveToFolder(['m1', 'm2'], 'junk1')
    expect(move.mock.calls.map((c) => c[0])).toEqual([['m1']])
    expect(mail.messages.map((m) => m.id)).toEqual(['m2'])
  })
})

describe('recipient chips', () => {
  it('double-clicking a chip puts it back into the text box', async () => {
    let value = [{ name: 'Doe, Jane', email: 'jane@x.com' }]
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
    app.use(createPinia())
    app.mount(el)
    el.querySelector('span.rounded-full')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }))
    await nextTick()
    expect(value).toEqual([])
    expect(el.querySelector('input')!.value).toBe('"Doe, Jane" <jane@x.com>')
    app.unmount()
  })
})
