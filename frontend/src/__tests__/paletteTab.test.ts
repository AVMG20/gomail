// Command palette: Tab completes a search suggestion, like Enter.
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import CommandPalette from '@/components/dialogs/CommandPalette.vue'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'

const tick = async () => {
  for (let i = 0; i < 3; i++) await nextTick()
}

describe('palette Tab completion', () => {
  it('completes the highlighted suggestion (else the first)', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useUiStore().paletteOpen = true
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp({ render: () => h(CommandPalette) })
    app.use(pinia)
    app.mount(el)
    await tick()
    const input = document.querySelector<HTMLInputElement>('[role="dialog"] input')!
    input.value = 'fro'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await tick()
    expect(document.querySelector('[data-complete]')?.textContent).toContain('from:')
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
    input.dispatchEvent(tab)
    await tick()
    expect(tab.defaultPrevented).toBe(true)
    expect(input.value).toBe('from:')
    app.unmount()
  })

  it('Enter puts an operator search in the mail list, unless an item was picked with the arrows', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useUiStore().paletteOpen = true
    const mail = useMailStore()
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp({ render: () => h(CommandPalette) })
    app.use(pinia)
    app.mount(el)
    await tick()
    const input = document.querySelector<HTMLInputElement>('[role="dialog"] input')!
    input.value = 'from:asana invoice'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await tick()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await tick()
    expect(mail.search).toBe('from:asana invoice')
    expect(mail.mailbox.kind).toBe('all')
    app.unmount()
  })
})
