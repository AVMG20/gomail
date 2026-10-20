// The invite card in the reading pane: shows your current answer, no RSVP on replies.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia } from 'pinia'
import { api } from '@/lib/api'
import type { CalendarEvent, EventInvite } from '@/lib/types'
import InviteCard from '@/components/mail/InviteCard.vue'

const invite = (over: Partial<EventInvite> = {}): EventInvite => ({
  eventId: 'e1', uid: 'u1', title: 'Sync', start: '2026-10-21T10:00:00Z', end: '2026-10-21T11:00:00Z', allDay: false, location: '',
  organizer: { name: 'Sam', email: 'sam@x.y' }, method: 'REQUEST', ...over,
})
function mount(inv: EventInvite) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(InviteCard, { invite: inv })
  app.use(createPinia())
  app.mount(el)
  return { el, unmount: () => { app.unmount(); el.remove() } }
}
const flush = async () => { for (let i = 0; i < 4; i++) await nextTick() }
const pressed = (el: HTMLElement) => [...el.querySelectorAll('button[aria-pressed="true"]')].map((b) => b.textContent?.trim())

afterEach(() => vi.restoreAllMocks())

describe('InviteCard', () => {
  it('shows the answer you already gave (read from the synced event)', async () => {
    vi.spyOn(api, 'getEvent').mockResolvedValue({ id: 'e1', isOrganizer: false, myResponse: 'accepted' } as CalendarEvent)
    const c = mount(invite())
    await flush()
    expect(pressed(c.el)).toEqual(['Accept'])
    c.unmount()
  })

  it('offers no RSVP on a guest’s reply or on your own event', async () => {
    vi.spyOn(api, 'getEvent').mockResolvedValue({ id: 'e1', isOrganizer: true, myResponse: '' } as CalendarEvent)
    const reply = mount(invite({ method: 'REPLY' }))
    await flush()
    expect(reply.el.textContent).not.toMatch(/Accept/)
    reply.unmount()
    const own = mount(invite())
    await flush()
    expect(own.el.textContent).not.toMatch(/Accept/)
    own.unmount()
  })
})
