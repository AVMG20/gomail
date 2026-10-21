import { beforeEach, describe, expect, it } from 'vitest'
import { normMessage, normSettings, normSummary, writeLocal } from '@/lib/api'
import type { Message, MessageSummary, Settings } from '@/lib/types'

describe('api normalisation (Go nil slices / legacy fields)', () => {
  beforeEach(() => localStorage.clear())

  it('fills nil slices on summaries and maps legacy category', () => {
    const m = normSummary({ id: '1', folderIds: null, to: null, labels: null, labelDetails: null, from: null, category: 'work' } as unknown as MessageSummary)
    expect(m.folderIds).toEqual([])
    expect(m.to).toEqual([])
    expect(m.labels).toEqual(['work'])
    expect(m.labelDetails).toEqual([])
    expect(m.from).toEqual({ name: '', email: '' })
  })

  it('fills nil slices and bodies on full messages', () => {
    const m = normMessage({ id: '1', cc: null, bcc: null, replyTo: null, attachments: null, bodyHtml: null, bodyText: null } as unknown as Message)
    expect([m.cc, m.bcc, m.replyTo, m.attachments]).toEqual([[], [], [], []])
    expect(m.bodyHtml).toBe('')
    expect(m.bodyText).toBe('')
    expect(m.labels).toEqual([])
  })

  it('defaults settings and maps legacy categories to labels', () => {
    const s = normSettings({ theme: '', density: '', syncIntervalMinutes: 0, radius: undefined, categories: [{ id: 'a', name: 'A', description: '', color: '#f00' }] } as unknown as Settings)
    expect(s.theme).toBe('system')
    expect(s.density).toBe('comfortable')
    expect(s.syncIntervalMinutes).toBe(5)
    expect(s.labels.map((l) => l.id)).toEqual(['a'])
    expect(s.classifierModel).toBe('typesafe/jev-1.13')
    expect(s.classifyThreshold).toBe(0.5)
    expect(s.aiDailyBudgetUsd).toBe(1)
    expect(s.syncDepthDays).toBe(90)
    expect(s.emailBackground).toBe('auto')
    expect(s.checkUpdates).toBe(true)
    expect(normSettings({ emailBackground: 'light' } as unknown as Settings).emailBackground).toBe('light')
    expect(normSettings({ emailBackground: 'dark' } as unknown as Settings).emailBackground).toBe('dark')
    expect(normSettings({ emailBackground: 'bogus' } as unknown as Settings).emailBackground).toBe('auto')
    // The older "Dark designed mail" switch on top of Auto becomes "All mail dark", then is cleared.
    const migrated = normSettings({ emailBackground: 'auto', darkenDesignedMail: true } as unknown as Settings)
    expect([migrated.emailBackground, migrated.darkenDesignedMail]).toEqual(['dark', false])
    expect(normSettings({ emailBackground: 'light', darkenDesignedMail: true } as unknown as Settings).emailBackground).toBe('light')
  })

  it('keeps explicit zeros and falls back to locally stored prefs for older backends', () => {
    writeLocal('readingPane', 'off')
    const s = normSettings({ radius: 0, aiDailyBudgetUsd: 0, syncDepthDays: 0, labels: [] } as unknown as Settings)
    expect(s.radius).toBe(0)
    expect(s.aiDailyBudgetUsd).toBe(0)
    expect(s.syncDepthDays).toBe(0)
    expect(s.readingPane).toBe('off')
  })
})

describe('calendar wire format', async () => {
  const { normEvent, toWireInput } = await import('@/lib/api')
  const wire = (over: Record<string, unknown> = {}) => ({
    id: 'e1', calendarId: 'c1', accountId: 'a1', title: 'Offsite', start: '2026-10-28T00:00:00Z', end: '2026-10-31T00:00:00Z', allDay: true,
    timeZone: 'Europe/Amsterdam', location: '', description: '', attendees: null, organizer: { name: '', email: 'p@x.io' }, isOrganizer: false,
    myResponse: 'none', meetingUrl: '', isRecurring: false, seriesId: '', recurrence: 'Weekly on Monday', rrule: 'FREQ=WEEKLY;BYDAY=MO', status: 'confirmed',
    reminders: null, canEdit: false, updatedAt: '', ...over,
  }) as never

  it('keeps all-day dates as calendar dates (never shifted by the local zone)', () => {
    const e = normEvent(wire())
    expect(e.start).toBe('2026-10-28')
    expect(e.end).toBe('2026-10-31')
  })

  it('maps responses, recurrence and nil slices', () => {
    const e = normEvent(wire({ attendees: [{ name: 'P', email: 'p@x.io', response: 'organizer' }, { name: 'Me', email: 'me@x.io', response: 'none' }] }))
    expect(e.myResponse).toBe('needsAction')
    expect(e.attendees.map((a) => a.response)).toEqual(['accepted', 'needsAction'])
    expect(e.recurrence).toBe('FREQ=WEEKLY;BYDAY=MO')
    expect(e.recurrenceText).toBe('Weekly on Monday')
    expect(e.reminders).toEqual([])
    expect(normEvent(wire({ isOrganizer: true, myResponse: 'organizer' })).myResponse).toBe('')
    expect(normEvent(wire({ myResponse: 'declined' })).myResponse).toBe('declined')
  })

  it('builds the backend input', async () => {
    const w = await toWireInput({
      calendarId: 'c1', title: 'Trip', start: '2026-11-05', end: '2026-11-09', allDay: true, timeZone: '', location: 'Lisbon', description: 'Notes',
      attendees: [{ name: '', email: 'a@b.io', response: 'needsAction' }], meetingUrl: 'https://meet.example/x', recurrence: 'FREQ=YEARLY;UNTIL=20301231', reminders: [1440],
    })
    expect(w).toMatchObject({ start: '2026-11-05T00:00:00Z', end: '2026-11-09T00:00:00Z', allDay: true, reminderMinutes: [1440] })
    expect(w.attendees).toEqual([{ name: '', email: 'a@b.io', response: 'none', optional: false }])
    expect((w.recurrence as { frequency: string }).frequency).toBe('yearly')
    expect((w.recurrence as { until: string }).until).toMatch(/^2030-12-3[01]T/)
    expect(w).toMatchObject({ description: 'Notes', meetingUrl: 'https://meet.example/x', addVideoCall: false })
    const timed = await toWireInput({ calendarId: '', title: '', start: '2026-10-20T09:00:00+02:00', end: '2026-10-20T10:00:00+02:00', allDay: false, timeZone: 'Europe/Amsterdam', location: '', description: '', attendees: [], meetingUrl: '', recurrence: '', reminders: [] })
    expect(timed).toMatchObject({ start: '2026-10-20T07:00:00.000Z', end: '2026-10-20T08:00:00.000Z', recurrence: { frequency: 'none', until: null, count: 0 } })
  })
})
