// Command palette: Tab completes a search suggestion, like Enter.
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import CommandPalette from '@/components/dialogs/CommandPalette.vue'
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
})
