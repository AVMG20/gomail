import { afterAll, describe, expect, it } from 'vitest'
import { dateUntil, eventToInput, parseRRule, untilDay } from '@/lib/calendar'
import { toWireInput } from '@/lib/api'
import type { CalendarEvent } from '@/lib/types'

// "Repeat until" is a calendar date; these run in Los Angeles, where a local end of day
// used to become the next day in UTC.
const savedTz = process.env.TZ
process.env.TZ = 'America/Los_Angeles'
afterAll(() => {
  if (savedTz === undefined) delete process.env.TZ
  else process.env.TZ = savedTz
})

const series = (over: Partial<CalendarEvent> = {}): CalendarEvent => ({
  id: 'e1', accountId: 'a1', calendarId: 'c1', title: 'Standup', start: '2026-11-23T17:00:00Z', end: '2026-11-23T17:30:00Z', allDay: false,
  timeZone: 'America/Los_Angeles', location: '', description: '', attendees: [], organizer: { name: '', email: '' }, myResponse: '',
  meetingUrl: '', isRecurring: true, recurrence: 'FREQ=DAILY;UNTIL=20261201T075959Z', seriesId: '', status: 'confirmed', reminders: [],
  canEdit: true, isOrganizer: true, ...over,
} as CalendarEvent)

describe('repeat until', () => {
  it('runs in Los Angeles', () => {
    expect(new Date(2026, 10, 30).getTimezoneOffset()).toBe(480)
  })

  it('sends the until date as a date', async () => {
    const w = await toWireInput({ ...eventToInput(series()), recurrence: 'FREQ=DAILY;UNTIL=20261130' })
    expect(w.recurrence).toEqual({ frequency: 'daily', until: '2026-11-30T00:00:00Z', count: 0 })
    const sydney = await toWireInput({ ...eventToInput(series({ timeZone: 'Australia/Sydney' })), recurrence: 'FREQ=WEEKLY;UNTIL=20261130' })
    expect((sydney.recurrence as { until: string }).until).toBe('2026-11-30T00:00:00Z')
  })

  it('reads a UTC UNTIL as the last day in the event zone', () => {
    // Google web ("until Nov 30" in LA) and gomail's backend both end the day in the zone.
    expect(parseRRule('FREQ=DAILY;UNTIL=20261201T075959Z', 'America/Los_Angeles').until).toBe('2026-11-30')
    expect(parseRRule('FREQ=DAILY;UNTIL=20261201T075959Z').until).toBe('2026-11-30') // local
    expect(parseRRule('FREQ=DAILY;UNTIL=20261130T125959Z', 'Australia/Sydney').until).toBe('2026-11-30')
    expect(parseRRule('FREQ=DAILY;UNTIL=20261130T235959Z', 'Australia/Sydney').until).toBe('2026-12-01')
    expect(parseRRule('FREQ=DAILY;UNTIL=20261130', 'Australia/Sydney').until).toBe('2026-11-30')
    expect(untilDay('20261130T090000', 'Australia/Sydney')).toBe('2026-11-30') // floating
    expect(untilDay('20261201T075959Z', 'Not/A_Zone')).toBe('2026-11-30') // falls back to local
    expect(untilDay('soon')).toBe('')
    expect(dateUntil('FREQ=WEEKLY;UNTIL=20261201T075959Z;BYDAY=MO', 'America/Los_Angeles')).toBe('FREQ=WEEKLY;UNTIL=20261130;BYDAY=MO')
    expect(dateUntil('FREQ=WEEKLY;COUNT=3')).toBe('FREQ=WEEKLY;COUNT=3')
  })

  it('keeps the end date when a series is saved again (title edit, drag, undo)', async () => {
    for (const ev of [series(), series({ recurrence: 'FREQ=DAILY;UNTIL=20261130T125959Z', timeZone: 'Australia/Sydney' })]) {
      const input = eventToInput(ev)
      expect(input.recurrence).toBe('FREQ=DAILY;UNTIL=20261130')
      const w = await toWireInput({ ...input, title: 'Renamed' })
      expect((w.recurrence as { until: string }).until).toBe('2026-11-30T00:00:00Z')
      // Saving what comes back changes nothing.
      expect(eventToInput({ ...ev, recurrence: input.recurrence }).recurrence).toBe(input.recurrence)
    }
  })
})
