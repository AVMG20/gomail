// RSVP from the calendar's event details card.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

const toastFn = vi.hoisted(() => vi.fn())
vi.mock('vue-sonner', () => ({ toast: Object.assign(toastFn, { error: () => {}, success: () => {}, warning: () => {}, dismiss: () => {} }) }))

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

describe('EventDetails on an invitation', () => {
  const setup = (canEdit: boolean) => () => {
    useAccountsStore().accounts = [{ id: 'a', email: 'me@x.y' }] as never
    useCalendarStore().calendars = [{ id: 'c', accountId: 'a', name: 'Work', color: '#4285f4', canEdit }] as never
  }
  const inv = { ...invite, attendees: [...invite.attendees, { name: '', email: 'me@x.y', response: 'needsAction' }], reminders: [10] } as CalendarEvent
  const q = (label: string) => document.body.querySelector<HTMLElement>(`[aria-label="${label}"]`)

  it('lets you remove it and change your own reminder on a writable calendar, but not edit it', async () => {
    const set = vi.spyOn(api, 'setEventReminders').mockImplementation(async (_id, minutes) => ({ ...inv, reminders: minutes }))
    const c = mount(inv, setup(true))
    expect(q('Edit event')).toBeNull()
    expect(q('Remove from your calendar')).not.toBeNull()
    expect(q('Change reminder')?.textContent).toContain('10 minutes before')
    await c.cal.setReminders(c.cal.events[0], [60], 'this')
    expect(set).toHaveBeenCalledWith('e1', [60], 'this')
    expect(c.cal.events[0].reminders).toEqual([60])
    c.unmount()
  })

  it('removing it offers no Undo (recreating it would make you the organizer)', async () => {
    vi.spyOn(api, 'deleteEvent').mockResolvedValue()
    const c = mount(inv, setup(true))
    await c.cal.remove(c.cal.events[0], 'this')
    expect(c.cal.events).toHaveLength(0)
    expect(toastFn).toHaveBeenCalledWith('Removed from your calendar', expect.objectContaining({ action: undefined }))
    c.unmount()
  })

  it('offers neither on a read-only calendar', () => {
    const c = mount(inv, setup(false))
    expect(q('Remove from your calendar')).toBeNull()
    expect(q('Change reminder')).toBeNull()
    expect(document.body.textContent).toContain('10 minutes before')
    c.unmount()
  })
})
