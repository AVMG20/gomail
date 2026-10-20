// Regression tests from the final pre-MVP frontend review.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('vue-sonner', () => {
  const rec = () => () => undefined
  return { toast: Object.assign(rec(), { error: rec(), success: rec(), warning: rec(), dismiss: () => {} }) }
})

import { api } from '@/lib/api'
import type { MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'
import { useShortcuts } from '@/composables/useShortcuts'
import RecipientField from '@/components/compose/RecipientField.vue'

const row = (id: string, over: Partial<MessageSummary> = {}) =>
  ({ id, accountId: 'a', threadId: id, folderIds: ['inbox1'], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [], labelDetails: [], ...over }) as MessageSummary

beforeEach(() => {
  localStorage.clear()
  document.body.innerHTML = ''
  setActivePinia(createPinia())
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('compose', () => {
  it('an address typed but not yet a chip survives minimising the window (Esc)', async () => {
    let value: { name: string; email: string }[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
    app.use(createPinia())
    app.mount(el)
    const input = el.querySelector('input')!
    input.value = 'bob@example.com'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    app.unmount() // ComposeWindow unmounts its fields when minimised
    expect(value.map((a) => a.email)).toEqual(['bob@example.com'])
  })

  it('a send finishing after the undo delay does not swallow the keys the user presses next', async () => {
    vi.useFakeTimers()
    const ui = useUiStore()
    const compose = useComposeStore()
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    await compose.send(d.id, 5)
    await vi.advanceTimersByTimeAsync(2000) // the user is back to triaging
    expect(ui.keyGuarded('#')).toBe(false)
    await vi.advanceTimersByTimeAsync(3100) // the queued send goes out in the background
    expect(compose.drafts).toHaveLength(0)
    expect(ui.keyGuarded('#')).toBe(false)
    expect(ui.keyGuarded('Backspace')).toBe(false)
  })
})

describe('shortcuts from a minimised compose tab', () => {
  function mountShortcuts() {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(defineComponent({ setup: () => (useShortcuts(), () => h('div')) }))
    app.mount(el)
    return app
  }
  function dockTab() {
    const dock = document.createElement('div')
    dock.setAttribute('data-compose-dock', '')
    const tab = document.createElement('button')
    dock.appendChild(tab)
    document.body.appendChild(dock)
    return tab
  }

  it('j still moves through the list, Backspace never trashes the open mail', async () => {
    const mail = useMailStore()
    mail.messages = [row('m0'), row('m1'), row('m2')]
    mail.openId = 'm0'
    const trash = vi.spyOn(api, 'trash').mockResolvedValue(undefined)
    const app = mountShortcuts()
    const tab = dockTab()
    tab.focus()
    tab.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
    expect(mail.openId).toBe('m1')
    tab.dispatchEvent(new KeyboardEvent('keydown', { key: 'Backspace', bubbles: true, cancelable: true }))
    await Promise.resolve()
    expect(trash).not.toHaveBeenCalled()
    expect(mail.messages).toHaveLength(3)
    app.unmount()
  })
})

describe('select all in a unified view', () => {
  it('Undo sends rows that were never loaded back to their own account’s folder', async () => {
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: 'a@x.io' }, { id: 'b', email: 'b@x.io' }] as never
    accounts.folders = {
      a: [{ id: 'inboxA', accountId: 'a', kind: 'inbox', name: 'Inbox' }],
      b: [{ id: 'inboxB', accountId: 'b', kind: 'inbox', name: 'Inbox' }],
    } as never
    const mail = useMailStore()
    vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
    vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
    vi.spyOn(accounts, 'refreshCounts').mockResolvedValue(undefined as never)
    mail.setMailbox({ accountId: '', folderId: '', kind: 'inbox' })
    mail.messages = [row('a1', { accountId: 'a', folderIds: ['inboxA'] })]
    vi.spyOn(api, 'resolveQueryIds').mockImplementation(async (q) => (q.accountId === 'a' ? ['a1', 'a2'] : q.accountId === 'b' ? ['b1'] : ['a1', 'a2', 'b1']))
    vi.spyOn(api, 'archive').mockResolvedValue(undefined)
    const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
    const sonner = await import('vue-sonner')
    let undo: (() => unknown) | undefined
    vi.spyOn(sonner, 'toast').mockImplementation(((_m: string, o?: { action?: { onClick: () => unknown } }) => void (undo ??= o?.action?.onClick)) as never)

    mail.selectAllQuery = true
    await mail.archive(await mail.targetIds())
    expect(undo).toBeTruthy()
    await undo!()
    const moved = new Map(move.mock.calls.map((c) => [c[1], [...c[0]].sort()]))
    expect(moved.get('inboxA')).toEqual(['a1', 'a2'])
    expect(moved.get('inboxB')).toEqual(['b1'])
  })
})

describe('untouched reply', () => {
  it('stays "untouched" after the editor normalises the prefilled HTML', async () => {
    const { draftHasContent } = await import('@/stores/compose')
    const compose = useComposeStore()
    const m = { ...row('m1'), cc: [], bcc: [], replyTo: [], bodyHtml: '<p>Hi sweetheart</p>', bodyText: 'Hi sweetheart', attachments: [] } as never
    useAccountsStore().accounts = [{ id: 'a', email: 'me@x.io', signature: '<p>Alex</p>' }] as never
    const d = (await compose.respond('reply', m))!
    // What Tiptap emits right after loading it: a class on the signature node and a trailing paragraph.
    d.html = d.html.replace('<div data-signature="">', '<div data-signature="" class="gm-signature">') + '<p></p>'
    expect(draftHasContent(d)).toBe(false)
    d.html = d.html.replace('<p></p>', '<p>Thanks!</p>')
    expect(draftHasContent(d)).toBe(true)
  })
})

describe('recipient field', () => {
  it('a comma typed inside a quoted name does not split the address', async () => {
    let value: { name: string; email: string }[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(RecipientField, { modelValue: value, label: 'To', 'onUpdate:modelValue': (v: typeof value) => (value = v) })
    app.use(createPinia())
    app.mount(el)
    const input = el.querySelector('input')!
    const type = async (t: string) => {
      input.value = t
      input.dispatchEvent(new Event('input'))
      await nextTick()
    }
    await type('"Doe')
    const comma = new KeyboardEvent('keydown', { key: ',', cancelable: true })
    input.dispatchEvent(comma)
    expect(comma.defaultPrevented).toBe(false)
    expect(value).toEqual([])
    await type('"Doe, John" <john@x.io>')
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', cancelable: true }))
    await nextTick()
    expect(value).toEqual([{ name: 'Doe, John', email: 'john@x.io' }])
    app.unmount()
  })
})

describe('emptied draft', () => {
  it('closing a message emptied after an autosave removes the server copy', async () => {
    const compose = useComposeStore()
    const del = vi.spyOn(api, 'deleteDraft').mockResolvedValue(undefined)
    const d = compose.compose()
    d.remoteId = 'draft:srv:1'
    d.remoteAccountId = d.accountId
    expect(await compose.saveAndClose(d.id)).toBe(true)
    expect(compose.drafts).toHaveLength(0)
    await Promise.resolve()
    expect(del).toHaveBeenCalledWith('draft:srv:1')
  })
})

describe('attendee responses on the wire', () => {
  it('new guests go out as "none", never as needsAction or another raw value', async () => {
    const { toWireInput } = await import('@/lib/api')
    const w = await toWireInput({
      calendarId: 'c', title: 't', start: '2026-10-20T10:00:00Z', end: '2026-10-20T11:00:00Z', allDay: false, timeZone: '', location: '', description: '',
      attendees: [
        { name: '', email: 'new@x.io', response: 'needsAction' },
        { name: '', email: 'odd@x.io', response: 'NEEDS-ACTION' },
        { name: '', email: 'yes@x.io', response: 'accepted' },
        { name: '', email: 'maybe@x.io', response: 'tentative' },
      ],
      meetingUrl: '', recurrence: '', reminders: [],
    } as never)
    expect((w.attendees as { response: string }[]).map((a) => a.response)).toEqual(['none', 'none', 'accepted', 'tentative'])
  })
})
