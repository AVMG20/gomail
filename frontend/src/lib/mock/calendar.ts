// In-memory calendars and events for the browser mock: three mailboxes, events
// around today (all-day, multi-day, a recurring standup, a declined meeting) and
// full CRUD including recurrence scopes ("this" occurrence vs the whole series).
import {
  addDays, buildRRule, eventDate, expandOccurrences, fromYmd, overlaps, parseRRule, startOfDay, startOfWeek, toRfc3339, toYmd, type RepeatFreq,
} from '../calendar'
import type { Account, AttendeeResponse, Calendar, CalendarEvent, EventInput, EventInvite, Message, RecurrenceScope } from '../types'

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
const ME: Record<string, { name: string; email: string }> = {
  acc_personal: { name: 'Alex Morgan', email: 'alex.morgan@gmail.com' },
  acc_work: { name: 'Alex Morgan', email: 'alex.morgan@northwind.io' },
  acc_side: { name: 'Alex Morgan', email: 'hello@pixelforge.studio' },
}
const PEOPLE = {
  priya: { name: 'Priya Raman', email: 'priya.raman@northwind.io' },
  jonas: { name: 'Jonas Weber', email: 'jonas.weber@northwind.io' },
  mara: { name: 'Mara Lindqvist', email: 'mara@northwind.io' },
  hr: { name: 'Northwind People Team', email: 'people@northwind.io' },
  sam: { name: 'Sam Okafor', email: 'sam.okafor@gmail.com' },
  mom: { name: 'Mom', email: 'linda.morgan@icloud.com' },
  rachel: { name: 'Rachel Kim', email: 'rachel@lumen.design' },
}

interface Master extends CalendarEvent {
  /** Skipped occurrence days ("YYYY-MM-DD") of a series. */
  exdates: string[]
}

export class MockCalendar {
  calendars: Calendar[] = [
    { id: 'cal_personal', accountId: 'acc_personal', name: 'Alex Morgan', color: '#7c3aed', primary: true, canEdit: true, visible: true, videoCallProvider: 'meet' },
    { id: 'cal_family', accountId: 'acc_personal', name: 'Family', color: '#ec4899', primary: false, canEdit: true, visible: true, videoCallProvider: 'meet' },
    { id: 'cal_birthdays', accountId: 'acc_personal', name: 'Birthdays', color: '#eab308', primary: false, canEdit: false, visible: true, videoCallProvider: '' },
    { id: 'cal_holidays', accountId: 'acc_personal', name: 'Holidays in the Netherlands', color: '#16a34a', primary: false, canEdit: false, visible: true, videoCallProvider: '' },
    { id: 'cal_work', accountId: 'acc_work', name: 'Calendar', color: '#2563eb', primary: true, canEdit: true, visible: true, videoCallProvider: 'teams' },
    { id: 'cal_atlas', accountId: 'acc_work', name: 'Atlas project', color: '#0891b2', primary: false, canEdit: true, visible: true, videoCallProvider: 'teams' },
  ]
  private masters = new Map<string, Master>()
  /** Edited single occurrences of a series, keyed by occurrence id. */
  private overrides = new Map<string, CalendarEvent>()
  private seq = 0

  constructor(private onChange: (accountId: string) => void) {
    this.seed()
  }

  /** Pixelforge has no calendar consent yet ("Connect"). */
  decorateAccount(a: Account): Account {
    return { ...a, calendarConsent: a.calendarConsent ?? a.id !== 'acc_side', calendarEnabled: a.calendarEnabled ?? true, calendarMuted: a.calendarMuted ?? false }
  }

  /** Test flag (?calconsent=none): every mailbox starts without calendar access. */
  noConsentFlag(): boolean {
    return typeof location !== 'undefined' && new URLSearchParams(location.search).get('calconsent') === 'none'
  }

  // ---------------------------------------------------------------- seed

  private add(e: Partial<CalendarEvent> & Pick<CalendarEvent, 'calendarId' | 'title' | 'start' | 'end'>, exdates: string[] = []) {
    const cal = this.calendars.find((c) => c.id === e.calendarId)!
    const id = e.id ?? `ev_${++this.seq}`
    const me = ME[cal.accountId]
    const ev: Master = {
      id, calendarId: e.calendarId, accountId: cal.accountId, title: e.title, start: e.start, end: e.end, allDay: e.allDay ?? false,
      timeZone: e.timeZone ?? 'Europe/Amsterdam', location: e.location ?? '', description: e.description ?? '', attendees: e.attendees ?? [],
      organizer: e.organizer ?? me, isOrganizer: e.isOrganizer ?? true, myResponse: e.myResponse ?? '', meetingUrl: e.meetingUrl ?? '',
      isRecurring: !!e.recurrence, seriesId: '', recurrence: e.recurrence ?? '', status: e.status ?? 'confirmed', reminders: e.reminders ?? [10], exdates,
    }
    this.masters.set(id, ev)
    return ev
  }

  private seed() {
    const today = startOfDay(new Date())
    const at = (dayOffset: number, hm: string) => toRfc3339(fromYmd(toYmd(addDays(today, dayOffset)), hm))
    const ymd = (dayOffset: number) => toYmd(addDays(today, dayOffset))
    const monday = startOfWeek(today, 1)
    const wd = (weekday: number, hm: string, weekOffset = 0) => toRfc3339(fromYmd(toYmd(addDays(monday, weekday - 1 + 7 * weekOffset)), hm))
    const me = ME.acc_work

    // Work (Outlook)
    this.add({
      id: 'ev_standup', calendarId: 'cal_work', title: 'Atlas standup', start: toRfc3339(fromYmd(toYmd(addDays(monday, -14)), '09:30')),
      end: toRfc3339(fromYmd(toYmd(addDays(monday, -14)), '09:45')), recurrence: 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', meetingUrl: 'https://teams.microsoft.com/l/meetup-join/atlas-standup',
      attendees: [{ ...PEOPLE.priya, response: 'accepted' }, { ...PEOPLE.jonas, response: 'accepted' }, { ...PEOPLE.mara, response: 'tentative' }],
      description: 'Yesterday, today, blockers. Keep it under 15 minutes.', reminders: [5],
    })
    this.add({
      calendarId: 'cal_atlas', title: 'Q4 roadmap review', start: at(0, '14:00'), end: at(0, '15:00'), location: 'Room Amstel (3rd floor)',
      organizer: PEOPLE.priya, isOrganizer: false, myResponse: 'accepted', meetingUrl: 'https://teams.microsoft.com/l/meetup-join/q4-roadmap',
      attendees: [{ ...PEOPLE.priya, response: 'accepted' }, { ...me, response: 'accepted' }, { ...PEOPLE.jonas, response: 'accepted' }, { ...PEOPLE.mara, response: 'needsAction' }],
      description: 'Slides 6–9: offline sync in Q4 or January? Please add comments beforehand.',
    })
    this.add({
      calendarId: 'cal_work', title: '1:1 Mara / Alex', start: wd(2, '11:00'), end: wd(2, '11:30'), recurrence: 'FREQ=WEEKLY',
      attendees: [{ ...PEOPLE.mara, response: 'accepted' }], location: 'Coffee corner',
    })
    this.add({
      calendarId: 'cal_work', title: 'Design crit', start: wd(3, '15:00'), end: wd(3, '16:00'), organizer: PEOPLE.mara, isOrganizer: false,
      myResponse: 'declined', attendees: [{ ...PEOPLE.mara, response: 'accepted' }, { ...me, response: 'declined' }],
    })
    this.add({
      calendarId: 'cal_work', title: 'Interview: Senior Frontend Engineer', start: wd(4, '13:00'), end: wd(4, '14:00'), organizer: PEOPLE.hr,
      isOrganizer: false, myResponse: 'needsAction', meetingUrl: 'https://teams.microsoft.com/l/meetup-join/interview-fe',
      attendees: [{ ...PEOPLE.hr, response: 'accepted' }, { ...me, response: 'needsAction' }, { ...PEOPLE.jonas, response: 'accepted' }],
    })
    this.add({
      id: 'ev_leadership', calendarId: 'cal_work', title: 'Atlas leadership sync', start: wd(4, '10:00'), end: wd(4, '11:00'), organizer: PEOPLE.priya,
      isOrganizer: false, myResponse: 'needsAction', attendees: [{ ...PEOPLE.priya, response: 'accepted' }, { ...me, response: 'needsAction' }],
      meetingUrl: 'https://teams.microsoft.com/l/meetup-join/leadership',
    })
    // Overlap + very long title + organised in another time zone.
    this.add({
      calendarId: 'cal_work', title: 'Northwind kickoff with the New York office: scope, timelines, staffing and the long list of open questions',
      start: wd(3, '15:00'), end: wd(3, '16:30'), timeZone: 'America/New_York', meetingUrl: 'https://zoom.us/j/5550199',
      attendees: [{ ...PEOPLE.priya, response: 'accepted' }],
    })
    this.add({ calendarId: 'cal_work', title: 'Release freeze starts', start: wd(2, '17:00'), end: wd(2, '17:00'), reminders: [] })
    this.add({ calendarId: 'cal_work', title: 'Focus time', start: wd(5, '09:00'), end: wd(5, '12:00'), status: 'tentative', reminders: [] })
    this.add({ calendarId: 'cal_atlas', title: 'Team offsite (Zandvoort)', start: toYmd(addDays(monday, 9)), end: toYmd(addDays(monday, 12)), allDay: true, location: 'Zandvoort aan Zee', reminders: [] })
    this.add({ calendarId: 'cal_work', title: 'Sprint planning', start: wd(1, '13:30', 1), end: wd(1, '15:00', 1), attendees: [{ ...PEOPLE.priya, response: 'accepted' }, { ...PEOPLE.jonas, response: 'accepted' }] })

    // Personal (Gmail)
    this.add({ calendarId: 'cal_personal', title: 'Climbing with Sam', start: wd(6, '10:00'), end: wd(6, '12:00'), location: 'Klimhal Amsterdam', attendees: [{ ...PEOPLE.sam, response: 'accepted' }] })
    this.add({ calendarId: 'cal_family', title: 'Sunday dinner at Mom’s', start: wd(7, '18:00'), end: wd(7, '21:00'), location: 'Haarlem', attendees: [{ ...PEOPLE.mom, response: 'accepted' }] })
    this.add({ calendarId: 'cal_personal', title: 'Dentist', start: at(8, '09:30'), end: at(8, '10:15'), location: 'Tandartspraktijk De Pijp', reminders: [60] })
    this.add({ calendarId: 'cal_personal', title: 'Lisbon trip', start: ymd(17), end: ymd(21), allDay: true, location: 'Lisbon', reminders: [] })
    this.add({ calendarId: 'cal_personal', title: 'Flight KL1689 AMS → LIS', start: at(17, '07:10'), end: at(17, '09:40'), location: 'Schiphol', timeZone: 'Europe/Amsterdam' })
    this.add({ calendarId: 'cal_personal', title: 'Night train to Vienna', start: at(11, '20:30'), end: at(12, '09:15'), location: 'Amsterdam Centraal', reminders: [120] })
    this.add({ calendarId: 'cal_personal', title: 'Gym', start: wd(1, '07:00'), end: wd(1, '08:00'), recurrence: 'FREQ=WEEKLY;BYDAY=MO,TH', reminders: [] })
    this.add({ calendarId: 'cal_family', title: 'Mom’s birthday', start: ymd(5), end: ymd(6), allDay: true, recurrence: 'FREQ=YEARLY', reminders: [1440] })
    this.add({ calendarId: 'cal_birthdays', title: 'Sam’s birthday', start: ymd(2), end: ymd(3), allDay: true, recurrence: 'FREQ=YEARLY', organizer: { name: 'Birthdays', email: '' }, isOrganizer: false, reminders: [] })
    this.add({ calendarId: 'cal_holidays', title: 'Daylight saving time ends', start: toYmd(lastSunday(today.getFullYear(), 9)), end: toYmd(addDays(lastSunday(today.getFullYear(), 9), 1)), allDay: true, organizer: { name: 'Holidays', email: '' }, isOrganizer: false, reminders: [] })
    this.add({ calendarId: 'cal_holidays', title: 'Sinterklaas', start: `${today.getFullYear()}-12-05`, end: `${today.getFullYear()}-12-06`, allDay: true, organizer: { name: 'Holidays', email: '' }, isOrganizer: false, reminders: [] })
  }

  // ---------------------------------------------------------------- reads

  private occurrenceId(master: Master, day: Date) {
    return `${master.id}_${toYmd(day).replace(/-/g, '')}`
  }

  /** All instances (single events and expanded occurrences) overlapping [start, end). */
  private instances(start: Date, end: Date): CalendarEvent[] {
    const out: CalendarEvent[] = []
    for (const m of this.masters.values()) {
      const { exdates, ...base } = m
      if (!m.recurrence) {
        if (overlaps(m, start, end)) out.push(clone(base))
        continue
      }
      const first = eventDate(m.start, m.allDay)
      const dur = eventDate(m.end, m.allDay).getTime() - first.getTime()
      // Look back by the duration so multi-day occurrences that started earlier still show.
      const days = expandOccurrences(first, parseRRule(m.recurrence), new Date(start.getTime() - dur), end, exdates)
      for (const d of days) {
        const id = this.occurrenceId(m, d)
        const override = this.overrides.get(id)
        if (override) {
          if (overlaps(override, start, end)) out.push(clone(override))
          continue
        }
        const s = d
        const e = new Date(d.getTime() + dur)
        const inst: CalendarEvent = {
          ...clone(base), id, seriesId: m.id, isRecurring: true,
          start: m.allDay ? toYmd(s) : toRfc3339(s), end: m.allDay ? toYmd(addDays(s, Math.max(1, Math.round(dur / 86_400_000)))) : toRfc3339(e),
        }
        if (overlaps(inst, start, end)) out.push(inst)
      }
    }
    return out.sort((a, b) => eventDate(a.start, a.allDay).getTime() - eventDate(b.start, b.allDay).getTime())
  }

  list(start: string, end: string, enabled: Set<string>): CalendarEvent[] {
    return this.instances(new Date(start), new Date(end)).filter((e) => enabled.has(e.accountId))
  }

  get(id: string): CalendarEvent {
    const m = this.masters.get(id)
    if (m) {
      const { exdates: _x, ...rest } = m
      return clone(rest)
    }
    const o = this.overrides.get(id)
    if (o) return clone(o)
    const [masterId, day] = splitOccurrence(id)
    const master = this.masters.get(masterId)
    if (master && day) {
      const d = fromYmd(day)
      const found = this.instances(addDays(d, -1), addDays(d, 2)).find((e) => e.id === id)
      if (found) return found
    }
    throw new Error('event not found')
  }

  // ---------------------------------------------------------------- writes

  private canEdit(calendarId: string) {
    const cal = this.calendars.find((c) => c.id === calendarId)
    if (!cal) throw new Error('calendar not found')
    if (!cal.canEdit) throw new Error('This calendar is read-only')
    return cal
  }

  /** Like the providers: "add a video call" attaches a Meet/Teams link. */
  private withVideo(input: EventInput, cal: Calendar, existing = ''): EventInput {
    if (!input.addVideoCall || input.meetingUrl || existing) return { ...input, meetingUrl: input.meetingUrl || existing }
    const code = Math.random().toString(36).slice(2, 12)
    const url = cal.videoCallProvider === 'teams' ? `https://teams.microsoft.com/l/meetup-join/${code}` : cal.videoCallProvider === 'meet' ? `https://meet.google.com/${code.slice(0, 3)}-${code.slice(3, 7)}-${code.slice(7, 10)}` : ''
    return { ...input, meetingUrl: url }
  }

  create(input: EventInput): CalendarEvent {
    const cal = this.canEdit(input.calendarId)
    input = this.withVideo(input, cal)
    const ev = this.add({ ...clone(input), isOrganizer: true, organizer: ME[cal.accountId], myResponse: '' })
    this.onChange(cal.accountId)
    return this.get(ev.id)
  }

  update(id: string, input: EventInput, scope: RecurrenceScope): CalendarEvent {
    const cal = this.canEdit(input.calendarId)
    input = this.withVideo(input, cal, this.get(id).meetingUrl)
    const master = this.masters.get(id)
    const [masterId] = splitOccurrence(id)
    if (master || scope === 'series') {
      const target = master ?? this.masters.get(masterId)
      if (!target) throw new Error('event not found')
      Object.assign(target, clone(input), { accountId: cal.accountId, isRecurring: !!input.recurrence })
      // Editing the series drops one-off changes, like most calendar apps.
      for (const k of [...this.overrides.keys()]) if (k.startsWith(`${target.id}_`)) this.overrides.delete(k)
      this.onChange(cal.accountId)
      return this.get(target.id)
    }
    const current = this.get(id)
    const next: CalendarEvent = { ...current, ...clone(input), accountId: cal.accountId, recurrence: current.recurrence, isRecurring: true }
    this.overrides.set(id, next)
    this.onChange(cal.accountId)
    return clone(next)
  }

  remove(id: string, scope: RecurrenceScope) {
    const master = this.masters.get(id)
    const [masterId, day] = splitOccurrence(id)
    const target = master ?? this.masters.get(masterId)
    if (!target) throw new Error('event not found')
    this.canEdit(target.calendarId)
    if (master || scope === 'series') {
      this.masters.delete(target.id)
      for (const k of [...this.overrides.keys()]) if (k.startsWith(`${target.id}_`)) this.overrides.delete(k)
    } else if (day) {
      target.exdates.push(day)
      this.overrides.delete(id)
    }
    this.onChange(target.accountId)
  }

  respond(id: string, response: AttendeeResponse) {
    const ev = this.get(id)
    if (ev.isOrganizer) throw new Error('You organize this event')
    const apply = (e: CalendarEvent) => {
      e.myResponse = response
      const me = ME[e.accountId]
      e.attendees = e.attendees.map((a) => (a.email === me?.email ? { ...a, response } : a))
    }
    const master = this.masters.get(ev.seriesId || ev.id)
    if (master) apply(master)
    for (const [k, o] of this.overrides) if (k.startsWith(`${ev.seriesId || ev.id}_`)) apply(o)
    this.onChange(ev.accountId)
  }

  setVisible(id: string, visible: boolean) {
    const cal = this.calendars.find((c) => c.id === id)
    if (!cal) throw new Error('calendar not found')
    cal.visible = visible
    this.onChange(cal.accountId)
  }

  /** "Connect calendar": grants consent and adds the mailbox's calendar. */
  connect(accountId: string) {
    if (!this.calendars.some((c) => c.accountId === accountId)) {
      this.calendars.push({ id: `cal_${accountId}`, accountId, name: 'Pixelforge', color: '#14b8a6', primary: true, canEdit: true, visible: true, videoCallProvider: 'meet' })
      const today = startOfDay(new Date())
      this.add({ calendarId: `cal_${accountId}`, title: 'Client call: Lumen rebrand', start: toRfc3339(fromYmd(toYmd(addDays(today, 1)), '16:00')), end: toRfc3339(fromYmd(toYmd(addDays(today, 1)), '16:45')), attendees: [{ ...PEOPLE.rachel, response: 'accepted' }], meetingUrl: 'https://meet.google.com/abc-defg-hij' })
    }
    this.onChange(accountId)
  }

  // ---------------------------------------------------------------- mail tie-in

  /** Invitation details for mails that carry one (the leadership sync invite). */
  inviteFor(m: Message): EventInvite | null {
    if (!/^Invitation:/i.test(m.subject)) return null
    const ev = this.masters.get('ev_leadership')
    if (!ev) return null
    return { eventId: ev.id, uid: `${ev.id}@northwind.io`, title: ev.title, start: ev.start, end: ev.end, allDay: false, location: ev.location || 'Microsoft Teams', organizer: ev.organizer, method: 'REQUEST', myResponse: ev.myResponse }
  }
}

/** Backend-shaped EventInput (what api.ts sends) → the mock's internal input. */
export function fromWireInput(w: Record<string, unknown> & Partial<EventInput>): EventInput {
  const allDay = !!w.allDay
  const rec = (w.recurrence ?? {}) as { frequency?: string; until?: string | null; count?: number } | string
  const recurrence = typeof rec === 'string'
    ? rec
    : buildRRule({ freq: (rec.frequency as RepeatFreq) || 'none', until: rec.until ? toYmd(new Date(rec.until)) : '', count: rec.count || undefined })
  const description = String(w.description ?? '')
  const meetingUrl = String(w.meetingUrl ?? '')
  const attendees = ((w.attendees as { name: string; email: string; response: string }[] | null) ?? []).map((a) => ({ name: a.name, email: a.email, response: a.response === 'none' ? 'needsAction' : a.response }))
  return {
    calendarId: String(w.calendarId ?? ''),
    title: String(w.title ?? ''),
    start: allDay ? String(w.start).slice(0, 10) : String(w.start),
    end: allDay ? String(w.end).slice(0, 10) : String(w.end),
    allDay,
    timeZone: String(w.timeZone ?? ''),
    location: String(w.location ?? ''),
    description,
    attendees,
    meetingUrl,
    addVideoCall: !!w.addVideoCall,
    recurrence,
    reminders: ((w.reminderMinutes ?? w.reminders) as number[] | null) ?? [],
  }
}

function splitOccurrence(id: string): [string, string] {
  const m = /^(.*)_(\d{4})(\d{2})(\d{2})$/.exec(id)
  return m ? [m[1], `${m[2]}-${m[3]}-${m[4]}`] : [id, '']
}

function lastSunday(year: number, month: number): Date {
  const d = new Date(year, month + 1, 0)
  return addDays(d, -d.getDay())
}

