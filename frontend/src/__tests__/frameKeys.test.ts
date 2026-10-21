// Keys pressed inside an email frame: plain keys reach the app's shortcuts, ⌘/Ctrl/Alt combos
// stay in the frame (select all, copy, find) except the app-wide ones.
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBody from '@/components/mail/MessageBody.vue'
import { isMac } from '@/lib/platform'
import type { Message } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'

const msg = { id: 'm1', accountId: 'a', threadId: 't1', folderIds: [], from: { name: '', email: 'x@y.z' }, to: [], cc: [], bcc: [], replyTo: [], subject: 's', snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [], labelDetails: [], bodyText: '', bodyHtml: '<p>Hello</p>', attachments: [] } as unknown as Message

describe('keys from the email frame', () => {
  let unmount = () => {}
  afterEach(() => unmount())

  async function frameDoc() {
    const pinia = createPinia()
    setActivePinia(pinia)
    useSettingsStore().settings = { emailBackground: 'auto' } as never
    const el = document.createElement('div')
    document.body.appendChild(el)
    const host = createApp({ render: () => h(MessageBody, { message: msg, allowRemote: false }) })
    host.use(pinia)
    host.mount(el)
    unmount = () => host.unmount()
    await nextTick()
    const iframe = el.querySelector('iframe')!
    iframe.dispatchEvent(new Event('load'))
    return iframe.contentDocument!
  }

  /** The keys the app saw for each key pressed in the frame. */
  function forwarded(doc: Document, keys: KeyboardEventInit[]): string[] {
    const seen: string[] = []
    const on = (e: KeyboardEvent) => seen.push(`${e.metaKey || e.ctrlKey ? 'mod+' : ''}${e.shiftKey ? 'shift+' : ''}${e.altKey ? 'alt+' : ''}${e.key}`)
    window.addEventListener('keydown', on)
    for (const k of keys) doc.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, ...k }))
    window.removeEventListener('keydown', on)
    return seen
  }
  const mod = (key: string, more: KeyboardEventInit = {}): KeyboardEventInit => ({ key, ...(isMac ? { metaKey: true } : { ctrlKey: true }), ...more })

  it('forwards plain keys and the app-wide combos', async () => {
    const doc = await frameDoc()
    const keys = [{ key: 'e' }, { key: '#', shiftKey: true }, { key: 'j' }, mod('k'), mod(','), mod('2'), mod(')', { shiftKey: true, code: 'Digit0' }), mod('='), mod('-'), mod('0')]
    expect(forwarded(doc, keys)).toEqual(['e', 'shift+#', 'j', 'mod+k', 'mod+,', 'mod+2', 'mod+shift+)', 'mod+=', 'mod+-', 'mod+0'])
  })

  it('keeps select all, copy, find and Alt combos in the frame', async () => {
    const doc = await frameDoc()
    expect(forwarded(doc, [mod('a'), mod('c'), mod('f'), mod('b'), { key: 'å', altKey: true }, mod('k', { altKey: true })])).toEqual([])
  })
})
