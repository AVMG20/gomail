// "Load images" must show the images without reopening the mail.
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick, reactive } from 'vue'
import { createPinia } from 'pinia'
import MessageBody from '@/components/mail/MessageBody.vue'
import type { Message } from '@/lib/types'

const stripped = '<p>Hi</p><img data-blocked="1" alt="">'
const full = '<p>Hi</p><img src="https://example.com/a.png" alt="">'
const msg = (over: Partial<Message>) =>
  ({ id: 'm1', accountId: 'a', threadId: 't1', folderIds: [], from: { name: '', email: 'x@y.z' }, to: [], cc: [], bcc: [], replyTo: [], subject: 's', snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [], labelDetails: [], bodyText: 'Hi', attachments: [], ...over }) as unknown as Message

function mount() {
  const props = reactive({ message: msg({ bodyHtml: stripped, remoteContentBlocked: true, hasRemoteContent: true }), allowRemote: false })
  const el = document.createElement('div')
  document.body.appendChild(el)
  const host = createApp({ render: () => h(MessageBody, { message: props.message, allowRemote: props.allowRemote }) })
  host.use(createPinia())
  host.mount(el)
  return { props, el, frame: () => el.querySelector('iframe')!, unmount: () => host.unmount() }
}

describe('load images', () => {
  it('replaces the frame once the full body arrives, and not before', async () => {
    const m = mount()
    const first = m.frame()
    expect(first.getAttribute('srcdoc')).toContain("img-src data: cid:;")

    // Clicked "Load images": allowed, but the body is still the stripped one.
    m.props.allowRemote = true
    await nextTick()
    expect(m.frame()).toBe(first)
    expect(m.el.textContent).toContain('Loading images')

    // The unstripped body arrives: a fresh frame with remote images allowed.
    m.props.message = { ...m.props.message, bodyHtml: full, remoteContentBlocked: false }
    await nextTick()
    const second = m.frame()
    expect(second).not.toBe(first)
    expect(second.getAttribute('srcdoc')).toContain('https://example.com/a.png')
    expect(second.getAttribute('srcdoc')).toContain('img-src data: cid: https: http:;')
    m.unmount()
  })
})
