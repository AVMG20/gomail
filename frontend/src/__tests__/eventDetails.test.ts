// RSVP from the calendar's event details card.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('vue-sonner', () => ({ toast: Object.assign(() => {}, { error: () => {}, success: () => {}, warning: () => {}, dismiss: () => {} }) }))

import { api } from '@/lib/api'
import type { CalendarEvent } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import EventDetails from '@/components/calendar/EventDetails.vue'

const invite = {
  id: 'e1', accountId: 'a', calendarId: 'c', title: 'Sync', start: '2026-10-21T10:00:00Z', end: '2026-10-21T11:00:00Z', allDay: false, timeZone: '',
  location: '', description: '', attendees: [{ name: 'Sam', email: 'sam@x.y', response: 'accepted' }], organizer: { name: 'Sam', email: 'sam@x.y' },
  isOrganizer: false, myResponse: 'needsAction', meetingUrl: '', recurrence: '', reminders: [], isRecurring: false, seriesId: '', status: 'confirmed', canEdit: false,
} as CalendarEvent

function mount(event: CalendarEvent = invite, setup?: () => void) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const cal = useCalendarStore()
  cal.events = [event]
  setup?.()
  const closed = vi.fn()
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(EventDetails, { anchor: { left: 10, top: 10, width: 1, height: 1 }, event, onClose: closed })
  app.use(pinia)
  app.mount(el)
  const button = (label: string) => [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === label)!
  return { cal, closed, button, unmount: () => { app.unmount(); el.remove() } }
}
const flush = async () => { for (let i = 0; i < 4; i++) await nextTick() }

afterEach(() => vi.restoreAllMocks())

describe('EventDetails RSVP', () => {
  it('closes the card once the answer is sent', async () => {
    const rsvp = vi.spyOn(api, 'respondToEvent').mockResolvedValue()
    const c = mount()
    c.button('Yes').click()
    await flush()
    expect(rsvp).toHaveBeenCalledWith('e1', 'accepted')
    expect(c.closed).toHaveBeenCalled()
    // Reopened later, the card shows the new answer (it reads the live event).
    expect(c.cal.events[0].myResponse).toBe('accepted')
    c.unmount()
  })

  it('stays open with the error when the answer fails', async () => {
    vi.spyOn(api, 'respondToEvent').mockRejectedValue(new Error('offline'))
    const c = mount()
    c.button('Maybe').click()
    await flush()
    expect(c.closed).not.toHaveBeenCalled()
    expect(document.body.querySelector('[role="alert"]')?.textContent).toMatch(/offline/i)
    expect(c.cal.events[0].myResponse).toBe('needsAction')
    c.unmount()
  })
})

describe('EventDetails layout', () => {
  const mine = {
    ...invite, id: 'e2', attendees: [{ name: '', email: 'me@gmail.com', response: 'accepted' }], organizer: { name: '', email: 'me@gmail.com' },
    isOrganizer: true, myResponse: 'accepted', reminders: [60], canEdit: true,
  } as CalendarEvent
  const setup = () => {
    useAccountsStore().accounts = [{ id: 'a', email: 'me@gmail.com' }] as never
    useCalendarStore().calendars = [{ id: 'c', accountId: 'a', name: 'me@gmail.com', color: '#4285f4' }] as never
  }

  it('shows no guest list or Email guests when you are the only one invited, and your address once', () => {
    const c = mount(mine, setup)
    const text = document.body.textContent ?? ''
    expect(text).not.toMatch(/guest/i)
    expect(document.body.querySelector('[aria-label="Email guests"]')).toBeNull()
    expect(text.split('me@gmail.com').length - 1).toBe(1)
    expect(text).toContain('1 hour before')
    c.unmount()
  })

  it('lists guests (marking you) once someone else is invited', () => {
    const c = mount({ ...mine, attendees: [...mine.attendees, { name: 'Sam', email: 'sam@x.y', response: 'needsAction' }] } as CalendarEvent, setup)
    const text = document.body.textContent ?? ''
    expect(text).toContain('2 guests')
    expect(text).toContain('(you)')
    expect(document.body.querySelector('[aria-label="Email guests"]')).not.toBeNull()
    c.unmount()
  })
})
