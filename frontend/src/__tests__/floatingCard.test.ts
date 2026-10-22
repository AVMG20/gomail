// A floating card closes on a click outside it, but not while a menu opened from it is open.
import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import FloatingCard from '@/components/common/FloatingCard.vue'

async function mount() {
  const closed = vi.fn()
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp({ render: () => h(FloatingCard, { anchor: { left: 10, top: 10, width: 1, height: 1 }, label: 'Card', onClose: closed }, () => h('p', 'hi')) })
  app.mount(el)
  for (let i = 0; i < 3; i++) await nextTick()
  await new Promise((r) => setTimeout(r, 0)) // the outside-click listener is armed after the opening click
  return { closed, unmount: () => { app.unmount(); el.remove() } }
}
const down = (target: Element) => target.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))

describe('FloatingCard', () => {
  it('a click while its menu is open closes only the menu', async () => {
    const c = await mount()
    const menu = document.createElement('div')
    menu.setAttribute('data-reka-popper-content-wrapper', '')
    document.body.appendChild(menu)
    down(document.documentElement) // what a click in the card reports while the menu disables pointer events
    expect(c.closed).not.toHaveBeenCalled()
    menu.remove()
    down(document.body)
    expect(c.closed).toHaveBeenCalledTimes(1)
    c.unmount()
  })
})
