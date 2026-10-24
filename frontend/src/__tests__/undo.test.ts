// Undo from the keyboard (z / mod+Z) and from the toast: the latest action, run once.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

interface Opts { duration?: number; description?: string; action?: { onClick: () => unknown }; onDismiss?: () => void; onAutoClose?: () => void }
const { toasts, toast } = vi.hoisted(() => {
  const toasts: { msg: string; opts?: Opts; id: number }[] = []
  let seq = 0
  const toast = Object.assign((msg: string, opts?: Opts) => (toasts.push({ msg, opts, id: ++seq }), seq), {
    error: (msg: string, opts?: Opts) => void toasts.push({ msg: `error: ${msg}`, opts, id: ++seq }),
    success: () => {}, warning: () => {}, info: () => {},
    dismiss: (id: number) => toasts.find((t) => t.id === id)?.opts?.onDismiss?.(),
  })
  return { toasts, toast }
})
vi.mock('vue-sonner', () => ({ toast }))

import { api } from '@/lib/api'
import type { MessageSummary } from '@/lib/types'
import { useShortcuts } from '@/composables/useShortcuts'
import { useAccountsStore } from '@/stores/accounts'
import { UNDO_MS, useMailStore } from '@/stores/mail'

const row = (id: string) => ({ id, accountId: 'a', threadId: id, folderIds: ['a:inbox'], from: { name: '', email: 'x@y.z' }, to: [], subject: id, snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [], labelDetails: [] }) as unknown as MessageSummary
const last = (msg: string) => [...toasts].reverse().find((t) => t.msg === msg)

async function setup() {
  const accounts = useAccountsStore()
  accounts.accounts = [{ id: 'a', email: 'alex@example.com' }] as never
  accounts.folders = { a: [{ id: 'a:inbox', accountId: 'a', kind: 'inbox', name: 'Inbox' }, { id: 'a:archive', accountId: 'a', kind: 'archive', name: 'Archive' }] } as never
  vi.spyOn(accounts, 'refreshCounts').mockResolvedValue(undefined as never)
  const mail = useMailStore()
  vi.spyOn(api, 'listMessages').mockResolvedValue({ messages: [], nextCursor: '', total: 0, hasMoreRemote: false })
  vi.spyOn(mail, 'softRefresh').mockResolvedValue(undefined as never)
  mail.setMailbox({ accountId: 'a', folderId: 'a:inbox', kind: 'inbox' })
  await new Promise((r) => setTimeout(r)) // the new mailbox's (empty) list has loaded
  mail.messages = [row('m1'), row('m2'), row('m3')]
  mail.status = 'ready'
  vi.spyOn(api, 'archive').mockResolvedValue(undefined)
  const move = vi.spyOn(api, 'moveToFolder').mockResolvedValue(undefined)
  return { mail, move }
}

beforeEach(() => {
  localStorage.clear()
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => vi.restoreAllMocks())

describe('undo', () => {
  it('the toast stays long enough, and the key undoes the action once', async () => {
    const { mail, move } = await setup()
    await mail.archive(['m1'])
    const t = last('Conversation archived')!
    expect(t.opts?.duration).toBe(UNDO_MS)
    expect(mail.lastUndo?.what).toBe('Conversation archived')
    await mail.undoLast()
    expect(move).toHaveBeenCalledTimes(1)
    expect(move.mock.calls[0][1]).toBe('a:inbox')
    expect(last('Undone')?.opts?.description).toBe('Conversation archived')
    expect(mail.lastUndo).toBeNull()
    // The toast's button afterwards (it was dismissed, but a click may already be on its way).
    await t.opts!.action!.onClick()
    expect(move).toHaveBeenCalledTimes(1)
  })

  it('after the toast’s button, the key has nothing left to undo', async () => {
    const { mail, move } = await setup()
    await mail.trash(['m1'])
    await last('Conversation moved to Trash')!.opts!.action!.onClick()
    await mail.undoLast()
    expect(move).toHaveBeenCalledTimes(1)
    expect(last('Nothing to undo')).toBeTruthy()
  })

  it('a newer action replaces the older one; a closed toast ends it', async () => {
    const { mail, move } = await setup()
    await mail.archive(['m1'])
    await mail.archive(['m2'])
    await mail.undoLast()
    expect(move.mock.calls).toEqual([[['m2'], 'a:inbox']])
    await mail.archive(['m3'])
    last('Conversation archived')!.opts!.onAutoClose!()
    expect(mail.lastUndo).toBeNull()
    await mail.undoLast()
    expect(move).toHaveBeenCalledTimes(1)
  })

  it('a failed undo says so instead of “Undone”', async () => {
    const { mail, move } = await setup()
    move.mockRejectedValue(new Error('offline'))
    await mail.archive(['m1'])
    await mail.undoLast()
    expect(last('error: Undo failed')).toBeTruthy()
    expect(last('Undone')).toBeUndefined()
  })

  it('z and mod+Z undo, but not while typing', async () => {
    const { mail } = await setup()
    const undo = vi.spyOn(mail, 'undoLast').mockResolvedValue()
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(defineComponent({ setup: () => (useShortcuts(), () => h('div')) }))
    app.mount(el)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'z' }))
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', ctrlKey: true, metaKey: true }))
    expect(undo).toHaveBeenCalledTimes(2)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Z', ctrlKey: true, metaKey: true, shiftKey: true }))
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', bubbles: true }))
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', ctrlKey: true, metaKey: true, bubbles: true }))
    expect(undo).toHaveBeenCalledTimes(2)
    app.unmount()
  })
})
