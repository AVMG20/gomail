import { afterAll, afterEach, describe, expect, it, vi } from 'vitest'

// Days around DST changes have 23 or 25 hours: these run in Los Angeles (DST from
// Sun 8 Mar 2026, standard time from Sun 1 Nov 2026).
const savedTz = process.env.TZ
process.env.TZ = 'America/Los_Angeles'
afterAll(() => {
  if (savedTz === undefined) delete process.env.TZ
  else process.env.TZ = savedTz
})
afterEach(() => {
  vi.useRealTimers()
})

const at = (s: string) => new Date(s).toISOString() // local time string -> ISO

describe('date groups around DST', () => {
  it('runs in Los Angeles', () => {
    expect(new Date(2026, 2, 7).getTimezoneOffset()).toBe(480)
    expect(new Date(2026, 2, 9).getTimezoneOffset()).toBe(420)
  })

  it('after the spring change (23-hour Sunday)', async () => {
    const { dateGroup } = await import('@/lib/dateGroups')
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-09T10:00:00')) // Monday
    expect(dateGroup(at('2026-03-09T00:30:00'))).toBe('Today')
    expect(dateGroup(at('2026-03-08T00:30:00'))).toBe('Yesterday')
    expect(dateGroup(at('2026-03-07T23:30:00'))).toBe('Last week') // Saturday: not yesterday
    expect(dateGroup(at('2026-03-02T00:30:00'))).toBe('Last week')
    expect(dateGroup(at('2026-03-01T23:30:00'))).toBe('March') // the Sunday before last week
  })

  it('after the autumn change (25-hour Sunday)', async () => {
    const { dateGroup } = await import('@/lib/dateGroups')
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-02T10:00:00')) // Monday
    expect(dateGroup(at('2026-11-01T00:30:00'))).toBe('Yesterday') // was "This week"
    expect(dateGroup(at('2026-10-31T23:30:00'))).toBe('Last week')
    expect(dateGroup(at('2026-10-26T00:30:00'))).toBe('Last week') // Monday of last week
    vi.setSystemTime(new Date('2026-11-04T10:00:00')) // Wednesday
    expect(dateGroup(at('2026-11-02T00:30:00'))).toBe('This week')
    expect(dateGroup(at('2026-10-26T00:30:00'))).toBe('Last week') // the week has 169 hours
    expect(dateGroup(at('2026-10-25T23:30:00'))).toBe('October')
  })

  it('counts a DST change day as one calendar day (month view click)', async () => {
    const { daysBetween } = await import('@/lib/calendar')
    // What FullCalendar selects for a click on Sun 8 Mar in month view.
    const start = new Date(2026, 2, 8)
    const end = new Date(2026, 2, 9)
    expect(end.getTime() - start.getTime()).toBe(23 * 3_600_000)
    expect(daysBetween(start, end)).toBe(1)
    expect(daysBetween(new Date(2026, 10, 1), new Date(2026, 10, 2))).toBe(1)
    expect(daysBetween(new Date(2026, 10, 1), new Date(2026, 10, 3))).toBe(2)
  })
})
