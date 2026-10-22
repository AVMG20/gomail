// A floating card closes on a click outside it or Escape, not on a click inside it.
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
  await new Promise((r) => setTimeout(r, 20)) // the outside-click listener is armed after the opening click
  const card = document.querySelector('[role="dialog"][aria-label="Card"]') as HTMLElement
  return { closed, card, unmount: () => { app.unmount(); el.remove() } }
}
async function down(target: Element) {
  target.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
  await new Promise((r) => setTimeout(r, 10))
}

describe('FloatingCard', () => {
  it('stays open on a click inside, closes on one outside', async () => {
    const c = await mount()
    expect(c.card?.textContent).toBe('hi')
    await down(c.card.querySelector('p')!)
    expect(c.closed).not.toHaveBeenCalled()
    await down(document.body)
    expect(c.closed).toHaveBeenCalledTimes(1)
    c.unmount()
  })

  it('closes on Escape', async () => {
    const c = await mount()
    c.card.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(c.closed).toHaveBeenCalledTimes(1)
    c.unmount()
  })
})
