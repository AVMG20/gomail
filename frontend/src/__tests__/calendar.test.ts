import { describe, expect, it } from 'vitest'
import {
  addDays, buildRRule, defaultCalendarId, effectiveWeekStart, expandOccurrences, fromYmd, isEventShown, localeUses12h, localeWeekStart,
  needsScopeChoice, overlaps, parseRRule, scopeTargetId, seriesMoveInput, shiftTimes, stepDate, toRfc3339, toYmd, utcToZoned, viewRange,
  zonedToUtc, zoneOffsetMinutes,
} from '@/lib/calendar'
import type { Calendar, CalendarEvent } from '@/lib/types'

const ev = (over: Partial<CalendarEvent> = {}): CalendarEvent => ({
  id: 'e1', calendarId: 'c1', accountId: 'a1', title: 'Standup', start: '2026-10-20T09:00:00+02:00', end: '2026-10-20T09:15:00+02:00',
  allDay: false, timeZone: 'Europe/Amsterdam', location: '', description: '', attendees: [], organizer: { name: '', email: 'me@x.io' },
  isOrganizer: true, myResponse: '', meetingUrl: '', isRecurring: false, seriesId: '', recurrence: '', status: 'confirmed', reminders: [10], ...over,
})

describe('locale rules', () => {
  it('knows the first day of the week', () => {
    expect(localeWeekStart('en-US')).toBe(0)
    expect(localeWeekStart('nl-NL')).toBe(1)
    expect(localeWeekStart('en-GB')).toBe(1)
    expect(effectiveWeekStart(6, 'en-US')).toBe(6)
    expect(effectiveWeekStart('saturday', 'en-US')).toBe(6)
    expect(effectiveWeekStart('monday', 'en-US')).toBe(1)
    expect(effectiveWeekStart('auto', 'de-DE')).toBe(1)
    expect(effectiveWeekStart(-1, 'de-DE')).toBe(1)
    expect(effectiveWeekStart(undefined, 'en-US')).toBe(0)
  })
  it('knows 12/24h clocks', () => {
    expect(localeUses12h('en-US')).toBe(true)
    expect(localeUses12h('nl-NL')).toBe(false)
  })
})

describe('date ranges', () => {
  const wed = fromYmd('2026-10-21', '15:30') // a Wednesday

  it('computes day, week, month and agenda ranges', () => {
    expect(viewRange('day', wed, 1)).toEqual({ start: fromYmd('2026-10-21'), end: fromYmd('2026-10-22') })
    expect(toYmd(viewRange('week', wed, 1).start)).toBe('2026-10-19')
    expect(toYmd(viewRange('week', wed, 0).start)).toBe('2026-10-18')
    expect(toYmd(viewRange('week', wed, 6).start)).toBe('2026-10-17')
    const m = viewRange('month', wed, 1)
    expect(toYmd(m.start)).toBe('2026-09-28')
    expect(toYmd(m.end)).toBe('2026-11-09')
    expect(toYmd(viewRange('agenda', wed, 1).end)).toBe('2026-11-20')
  })

  it('steps views forward and back', () => {
    expect(toYmd(stepDate('week', wed, 1))).toBe('2026-10-28')
    expect(toYmd(stepDate('day', wed, -1))).toBe('2026-10-20')
    expect(toYmd(stepDate('month', fromYmd('2026-01-31'), 1))).toBe('2026-02-01')
  })

  it('checks overlap with all-day exclusive ends', () => {
    const allDay = ev({ allDay: true, start: '2026-10-20', end: '2026-10-21' })
    expect(overlaps(allDay, fromYmd('2026-10-20'), fromYmd('2026-10-21'))).toBe(true)
    expect(overlaps(allDay, fromYmd('2026-10-21'), fromYmd('2026-10-22'))).toBe(false)
    expect(overlaps(ev({ allDay: true, start: '2026-10-20', end: '2026-10-20' }), fromYmd('2026-10-20'), fromYmd('2026-10-21'))).toBe(true)
  })

  it('formats local RFC 3339 with offset', () => {
    expect(toRfc3339(fromYmd('2026-10-20', '09:05'))).toMatch(/^2026-10-20T09:05:00[+-]\d{2}:\d{2}$/)
  })
})

describe('time zones', () => {
  it('computes offsets across DST', () => {
    expect(zoneOffsetMinutes('Europe/Amsterdam', new Date('2026-07-01T12:00:00Z'))).toBe(120)
    expect(zoneOffsetMinutes('Europe/Amsterdam', new Date('2026-12-01T12:00:00Z'))).toBe(60)
    expect(zoneOffsetMinutes('America/New_York', new Date('2026-12-01T12:00:00Z'))).toBe(-300)
    expect(zoneOffsetMinutes('UTC', new Date())).toBe(0)
  })
  it('converts wall-clock times in a zone to instants and back', () => {
    expect(zonedToUtc('2026-10-20', '09:00', 'Europe/Amsterdam').toISOString()).toBe('2026-10-20T07:00:00.000Z')
    expect(zonedToUtc('2026-11-02', '09:00', 'America/New_York').toISOString()).toBe('2026-11-02T14:00:00.000Z')
    // The day after the US DST change.
    expect(zonedToUtc('2026-03-09', '09:00', 'America/New_York').toISOString()).toBe('2026-03-09T13:00:00.000Z')
    expect(utcToZoned(new Date('2026-10-20T07:00:00Z'), 'Asia/Tokyo')).toEqual({ ymd: '2026-10-20', hm: '16:00' })
  })
})

describe('recurrence rules', () => {
  it('builds and parses RRULEs', () => {
    expect(buildRRule({ freq: 'none', until: '' })).toBe('')
    expect(buildRRule({ freq: 'weekly', until: '2026-12-31' })).toBe('FREQ=WEEKLY;UNTIL=20261231')
    expect(buildRRule({ freq: 'weekly', until: '', byDay: [1, 2, 3, 4, 5] })).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR')
    expect(parseRRule('RRULE:FREQ=DAILY;INTERVAL=2;UNTIL=20261231T235959Z')).toEqual({ freq: 'daily', until: '2026-12-31', interval: 2 })
    expect(parseRRule('FREQ=WEEKLY;BYDAY=MO,WE')).toEqual({ freq: 'weekly', until: '', byDay: [1, 3] })
    expect(parseRRule('FREQ=SECONDLY').freq).toBe('none')
    expect(parseRRule('').freq).toBe('none')
  })

  it('expands weekday standups with exceptions and an end date', () => {
    const first = fromYmd('2026-10-19', '09:30') // Monday
    const rule = parseRRule('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;UNTIL=20261030')
    const days = expandOccurrences(first, rule, fromYmd('2026-10-01'), fromYmd('2026-12-01'), ['2026-10-21']).map(toYmd)
    expect(days).toEqual(['2026-10-19', '2026-10-20', '2026-10-22', '2026-10-23', '2026-10-26', '2026-10-27', '2026-10-28', '2026-10-29', '2026-10-30'])
  })

  it('expands monthly rules skipping short months, and COUNT', () => {
    const days = expandOccurrences(fromYmd('2026-01-31', '10:00'), { freq: 'monthly', until: '' }, fromYmd('2026-01-01'), fromYmd('2026-06-01')).map(toYmd)
    expect(days).toEqual(['2026-01-31', '2026-03-31', '2026-05-31'])
    const counted = expandOccurrences(fromYmd('2026-10-20'), { freq: 'daily', until: '', count: 3 }, fromYmd('2026-10-01'), fromYmd('2026-11-01'))
    expect(counted.map(toYmd)).toEqual(['2026-10-20', '2026-10-21', '2026-10-22'])
  })

  it('returns only occurrences inside the range', () => {
    const days = expandOccurrences(fromYmd('2026-01-01', '08:00'), { freq: 'weekly', until: '' }, fromYmd('2026-10-19'), fromYmd('2026-10-26'))
    expect(days.map(toYmd)).toEqual(['2026-10-22'])
  })
})

describe('recurrence scope', () => {
  const occurrence = ev({ id: 's1_20261020', seriesId: 's1', isRecurring: true, recurrence: 'FREQ=DAILY' })

  it('asks for a scope only on recurring events', () => {
    expect(needsScopeChoice(occurrence)).toBe(true)
    expect(needsScopeChoice(ev())).toBe(false)
  })

  it('targets the occurrence or the series master', () => {
    expect(scopeTargetId(occurrence, 'this')).toBe('s1_20261020')
    expect(scopeTargetId(occurrence, 'series')).toBe('s1')
    expect(scopeTargetId(ev(), 'series')).toBe('e1')
  })

  it('moves the series by the same delta from its own first date', () => {
    const master = ev({ id: 's1', isRecurring: true, recurrence: 'FREQ=DAILY', start: '2026-09-01T09:00:00+02:00', end: '2026-09-01T09:15:00+02:00' })
    const input = seriesMoveInput(master, 30 * 60_000, 30 * 60_000)
    expect(new Date(input.start).toISOString()).toBe('2026-09-01T07:30:00.000Z')
    expect(new Date(input.end).toISOString()).toBe('2026-09-01T07:45:00.000Z')
    expect(input.recurrence).toBe('FREQ=DAILY')
  })

  it('shifts all-day events by whole days and keeps a minimum length', () => {
    expect(shiftTimes({ start: '2026-10-20', end: '2026-10-22', allDay: true }, 86_400_000, 86_400_000)).toEqual({ start: '2026-10-21', end: '2026-10-23', allDay: true })
    const t = shiftTimes({ start: '2026-10-20T09:00:00Z', end: '2026-10-20T10:00:00Z', allDay: false }, 0, -2 * 3_600_000)
    expect(new Date(t.end).getTime() - new Date(t.start).getTime()).toBe(15 * 60_000)
  })
})

describe('calendar choice and visibility', () => {
  const cals: Calendar[] = [
    { id: 'g-ro', accountId: 'g', name: 'Holidays', color: '#0a0', primary: false, canEdit: false, visible: true },
    { id: 'g-main', accountId: 'g', name: 'Alex', color: '#f00', primary: true, canEdit: true, visible: true },
    { id: 'o-main', accountId: 'o', name: 'Calendar', color: '#00f', primary: true, canEdit: true, visible: true },
  ]
  it('defaults to the last used, else an enabled primary calendar', () => {
    expect(defaultCalendarId(cals, [{ id: 'g' }, { id: 'o' }], 'o-main')).toBe('o-main')
    expect(defaultCalendarId(cals, [{ id: 'g', calendarEnabled: false }, { id: 'o' }], 'g-main')).toBe('o-main')
    expect(defaultCalendarId(cals, [{ id: 'g' }, { id: 'o' }], 'g-ro')).toBe('g-main')
    expect(defaultCalendarId(cals, [{ id: 'g', calendarEnabled: false }, { id: 'o', calendarEnabled: false }])).toBe('')
  })
  it('hides events of hidden calendars, disabled mailboxes, cancelled and (optionally) declined', () => {
    const calMap = new Map(cals.map((c) => [c.id, c]))
    const accs = new Map([['g', { calendarEnabled: true }], ['o', { calendarEnabled: false }]])
    expect(isEventShown(ev({ calendarId: 'g-main', accountId: 'g' }), calMap, accs, true)).toBe(true)
    expect(isEventShown(ev({ calendarId: 'o-main', accountId: 'o' }), calMap, accs, true)).toBe(false)
    expect(isEventShown(ev({ calendarId: 'g-main', accountId: 'g', myResponse: 'declined' }), calMap, accs, false)).toBe(false)
    expect(isEventShown(ev({ calendarId: 'g-main', accountId: 'g', myResponse: 'declined' }), calMap, accs, true)).toBe(true)
    expect(isEventShown(ev({ calendarId: 'g-main', accountId: 'g', status: 'cancelled' }), calMap, accs, true)).toBe(false)
    expect(isEventShown(ev({ calendarId: 'nope' }), calMap, accs, true)).toBe(false)
    const hidden = new Map(calMap)
    hidden.set('g-main', { ...cals[1], visible: false })
    expect(isEventShown(ev({ calendarId: 'g-main', accountId: 'g' }), hidden, accs, true)).toBe(false)
  })
  it('addDays keeps the wall clock', () => {
    expect(addDays(fromYmd('2026-10-24', '09:00'), 2).getHours()).toBe(9)
  })
})

describe('mock calendar recurrence scopes', async () => {
  const { MockCalendar } = await import('@/lib/mock/calendar')
  const { startOfWeek: sow } = await import('@/lib/calendar')
  const monday = sow(new Date(), 1)
  const range = (days = 7) => [monday.toISOString(), addDays(monday, days).toISOString()] as const
  const standups = (c: InstanceType<typeof MockCalendar>) => c.list(...range(), new Set(['acc_work'])).filter((e) => e.seriesId === 'ev_standup')

  it('expands the weekday standup into five occurrences', () => {
    const c = new MockCalendar(() => {})
    expect(standups(c)).toHaveLength(5)
  })

  it('"this" edits one occurrence, "series" edits all and drops one-offs', () => {
    const c = new MockCalendar(() => {})
    const [mon, tue] = standups(c)
    c.update(tue.id, { ...tue, title: 'Standup (moved)' } as never, 'this')
    expect(standups(c).map((e) => e.title)).toEqual(['Atlas standup', 'Standup (moved)', 'Atlas standup', 'Atlas standup', 'Atlas standup'])
    const master = c.get('ev_standup')
    c.update(mon.id, { ...master, title: 'Daily sync' } as never, 'series')
    expect(new Set(standups(c).map((e) => e.title))).toEqual(new Set(['Daily sync']))
  })

  it('"this" delete skips one day, "series" delete removes all', () => {
    const c = new MockCalendar(() => {})
    const [, , wed] = standups(c)
    c.remove(wed.id, 'this')
    expect(standups(c)).toHaveLength(4)
    c.remove(standups(c)[0].id, 'series')
    expect(standups(c)).toHaveLength(0)
  })

  it('refuses edits on read-only calendars and responses on own events', () => {
    const c = new MockCalendar(() => {})
    expect(() => c.create({ calendarId: 'cal_holidays', title: 'x', start: '2026-10-20', end: '2026-10-21', allDay: true } as never)).toThrow(/read-only/)
    const own = c.create({ calendarId: 'cal_work', title: 'Mine', start: '2026-10-20T10:00:00Z', end: '2026-10-20T11:00:00Z', allDay: false, attendees: [], reminders: [], recurrence: '' } as never)
    expect(() => c.respond(own.id, 'accepted')).toThrow()
  })
})

describe('calendar store: series edits from an occurrence', async () => {
  const { createPinia, setActivePinia } = await import('pinia')
  const { api } = await import('@/lib/api')
  const { useCalendarStore } = await import('@/stores/calendar')
  const { vi } = await import('vitest')

  it('applies the occurrence time change to the series start, not its date', async () => {
    setActivePinia(createPinia())
    const cal = useCalendarStore()
    const master = ev({ id: 's1', isRecurring: true, recurrence: 'FREQ=WEEKLY', start: '2026-09-01T09:00:00Z', end: '2026-09-01T09:15:00Z' })
    const occ = ev({ id: 's1_20261020', seriesId: 's1', isRecurring: true, recurrence: 'FREQ=WEEKLY', start: '2026-10-20T09:00:00Z', end: '2026-10-20T09:15:00Z' })
    vi.spyOn(api, 'getEvent').mockResolvedValue(master)
    const upd = vi.spyOn(api, 'updateEvent').mockResolvedValue(master)
    vi.spyOn(api, 'listEvents').mockResolvedValue([])
    await cal.update(occ, { ...occ, title: 'Renamed', start: '2026-10-20T10:00:00Z', end: '2026-10-20T10:30:00Z' }, 'series', true)
    const [id, input, scope] = upd.mock.calls[0]
    expect(id).toBe('s1')
    expect(scope).toBe('series')
    expect(input.title).toBe('Renamed')
    expect(new Date(input.start).toISOString()).toBe('2026-09-01T10:00:00.000Z')
    expect(new Date(input.end).toISOString()).toBe('2026-09-01T10:30:00.000Z')
  })
})
