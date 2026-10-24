// The agenda's time column for all-day events.
import { describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('vue-sonner', () => ({ toast: Object.assign(() => {}, { error: () => {}, success: () => {}, warning: () => {}, dismiss: () => {} }) }))

import { api } from '@/lib/api'
import { addDays, toRfc3339, toYmd } from '@/lib/calendar'
import type { CalendarEvent } from '@/lib/types'
import { useCalendarStore } from '@/stores/calendar'
import CalendarView from '@/components/calendar/CalendarView.vue'

const base = {
  accountId: 'a', calendarId: 'c', location: '', description: '', attendees: [], organizer: { name: '', email: '' }, isOrganizer: true, myResponse: '',
  meetingUrl: '', recurrence: '', reminders: [], isRecurring: false, seriesId: '', status: 'confirmed', canEdit: true, timeZone: '',
}

describe('agenda', () => {
  it('shows "All day" in the time column of all-day events', async () => {
    const today = new Date()
    today.setHours(0, 0, 0, 0)
    const at10 = new Date(today)
    at10.setHours(10)
    const events = [
      { ...base, id: 'd', title: 'Holiday', start: toYmd(today), end: toYmd(addDays(today, 1)), allDay: true },
      { ...base, id: 't', title: 'Review', start: toRfc3339(at10), end: toRfc3339(new Date(at10.getTime() + 3_600_000)), allDay: false },
    ] as CalendarEvent[]
    vi.spyOn(api, 'listEvents').mockResolvedValue(events)
    const pinia = createPinia()
    setActivePinia(pinia)
    const cal = useCalendarStore()
    cal.calendars = [{ id: 'c', accountId: 'a', name: 'Work', color: '#3b82f6', primary: true, canEdit: true, visible: true }]
    cal.loaded = true
    cal.view = 'agenda'
    cal.events = events
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp(CalendarView)
    app.use(pinia)
    app.mount(el)
    for (let i = 0; i < 6; i++) await nextTick()
    const rows = [...el.querySelectorAll('.fc-list-event')]
    const time = (title: string) => rows.find((r) => r.textContent?.includes(title))?.querySelector('.fc-list-event-time')?.textContent?.trim()
    expect(time('Holiday')).toBe('All day')
    expect(time('Review')).toMatch(/10/)
    app.unmount()
    el.remove()
  })
})
