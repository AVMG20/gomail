// Experimental "All mail dark": light colors turn dark, brand colors stay, only when chosen.
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBody from '@/components/mail/MessageBody.vue'
import { darkenDocument } from '@/lib/darkMail'
import { themeState } from '@/lib/theme'
import type { Message } from '@/lib/types'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

describe('darkenDocument', () => {
  function page(html: string) {
    document.body.innerHTML = html
    darkenDocument(document)
    return (id: string) => (document.getElementById(id) as HTMLElement).style
  }

  it('darkens light backgrounds and lightens dark text on them', () => {
    const el = page('<div id="card" style="background-color:#ffffff;color:#1f2328">Hi <span id="brand" style="color:#3b1f4f">Sale</span></div>')
    expect(el('card').getPropertyValue('background-color')).toMatch(/^hsla\(0, 0%, 7%/)
    expect(el('card').getPropertyValue('color')).toMatch(/[89]\d%, 1\)$/)
    expect(el('brand').getPropertyValue('color')).toMatch(/^hsla\(2\d\d, /) // still purple, now light
  })

  it('keeps dark and saturated colors, and the text on them', () => {
    const el = page('<div id="head" style="background-color:#3b1f4f;color:#ffffff">Logo</div><div id="btn" style="background-color:#ffcc00;color:#000000">Buy</div>')
    expect(el('head').getPropertyValue('background-color')).toMatch(/#3b1f4f|rgb\(59, 31, 79\)/)
    expect(el('head').getPropertyValue('color')).toMatch(/#ffffff|rgb\(255, 255, 255\)/)
    expect(el('btn').getPropertyValue('background-color')).toMatch(/#ffcc00|rgb\(255, 204, 0\)/)
    expect(el('btn').getPropertyValue('color')).toMatch(/#000000|rgb\(0, 0, 0\)/) // black on yellow stays readable
  })
})

const designed = '<table bgcolor="#f4f4f4"><tr><td style="color:#3b1f4f">Sale</td></tr></table>'
const msg = { id: 'm1', accountId: 'a', threadId: 't1', folderIds: [], from: { name: '', email: 'x@y.z' }, to: [], cc: [], bcc: [], replyTo: [], subject: 's', snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [], labelDetails: [], bodyText: '', bodyHtml: designed, attachments: [] } as unknown as Message

describe('dark designed mail', () => {
  function mount(emailBackground: 'auto' | 'light' | 'dark') {
    const pinia = createPinia()
    setActivePinia(pinia)
    useSettingsStore().settings = { emailBackground } as never
    themeState.value = { ...themeState.value, dark: true }
    const el = document.createElement('div')
    document.body.appendChild(el)
    const host = createApp({ render: () => h(MessageBody, { message: msg, allowRemote: false }) })
    host.use(pinia)
    host.mount(el)
    return { srcdoc: () => el.querySelector('iframe')!.getAttribute('srcdoc') ?? '', unmount: () => host.unmount() }
  }

  it('is off for "Plain mail dark" and "Always light", on for "All mail dark", off again for "show original colors"', async () => {
    for (const bg of ['auto', 'light'] as const) {
      const off = mount(bg)
      expect(off.srcdoc()).not.toContain('gm-dark')
      off.unmount()
    }
    const on = mount('dark')
    expect(on.srcdoc()).toContain('class="gm-dark"')
    // "Show original colors" for this conversation.
    useMailStore().originalColors = new Set(['t1'])
    await nextTick()
    expect(on.srcdoc()).not.toContain('gm-dark')
    on.unmount()
    themeState.value = { ...themeState.value, dark: false }
  })
})
