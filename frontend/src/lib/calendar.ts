// Pure calendar helpers: week/locale rules, date ranges, time zones, recurrence
// rules and recurrence-scope decisions. No Vue or DOM here (unit tested).
import { ref } from 'vue'
import type { Account, Calendar, CalendarEvent, EventInput } from './types'

export type CalendarViewKind = 'day' | 'week' | 'month' | 'agenda'

const DAY_MS = 86_400_000

// ------------------------------------------------------------------ locale

/** First day of the week for a locale (0 = Sunday … 6 = Saturday). */
export function localeWeekStart(locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'): number {
  try {
    const L = new Intl.Locale(locale) as Intl.Locale & { getWeekInfo?: () => { firstDay: number }; weekInfo?: { firstDay: number } }
    const info = L.getWeekInfo?.() ?? L.weekInfo
    if (info?.firstDay) return info.firstDay % 7 // Intl uses 1 = Monday … 7 = Sunday
  } catch {
    /* older engines */
  }
  // Fallback: regions that start on Sunday (CLDR); everything else Monday.
  const region = (locale.split(/[-_]/)[1] ?? '').toUpperCase()
  const sunday = ['US', 'CA', 'MX', 'BR', 'JP', 'KR', 'TW', 'HK', 'IL', 'PH', 'IN', 'ZA', 'AU', 'SA', 'PE', 'CO', 'AR', 'VE', 'GT', 'PR']
  if (!region) return locale.startsWith('en') ? 0 : 1
  return sunday.includes(region) ? 0 : ['AE', 'AF', 'BH', 'DZ', 'EG', 'IQ', 'IR', 'JO', 'KW', 'LY', 'OM', 'QA', 'SD', 'SY'].includes(region) ? 6 : 1
}

const WEEK_START: Record<string, number> = { sunday: 0, monday: 1, saturday: 6 }

/** Setting "auto" (or anything unknown) follows the locale; numbers 0/1/6 are accepted too. */
export function effectiveWeekStart(setting: string | number | undefined, locale?: string): number {
  if (setting === 0 || setting === 1 || setting === 6) return setting
  if (typeof setting === 'string' && setting in WEEK_START) return WEEK_START[setting]
  return localeWeekStart(locale)
}

/** Whether the locale writes times with AM/PM. */
export function localeUses12h(locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'): boolean {
  try {
    const hc = new Intl.DateTimeFormat(locale, { hour: 'numeric' }).resolvedOptions().hourCycle
    return hc === 'h11' || hc === 'h12'
  } catch {
    return false
  }
}

// Settings → Appearance → Time format ("auto" = null: follow the locale). A ref, so computed dates re-render on change.
const clockPreference = ref<boolean | null>(null)
export function setClockPreference(format: string | undefined) {
  clockPreference.value = format === '12h' ? true : format === '24h' ? false : null
}
/** 12-hour clock for displayed times: the user's choice, else the locale's. */
export function uses12h(locale?: string): boolean {
  return clockPreference.value ?? localeUses12h(locale)
}

// ------------------------------------------------------------------ dates (local time)

export function pad(n: number): string {
  return String(n).padStart(2, '0')
}

export function toYmd(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export function toHm(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** "YYYY-MM-DD" (and optional "HH:MM") as a local Date. */
export function fromYmd(ymd: string, hm = '00:00'): Date {
  const [y, m, d] = ymd.split('-').map(Number)
  const [h, min] = hm.split(':').map(Number)
  return new Date(y, (m || 1) - 1, d || 1, h || 0, min || 0, 0, 0)
}

export function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

/** Calendar-day arithmetic (DST-safe: keeps the wall-clock time). */
export function addDays(d: Date, n: number): Date {
  const x = new Date(d)
  x.setDate(x.getDate() + n)
  return x
}

export function addMinutes(d: Date, n: number): Date {
  return new Date(d.getTime() + n * 60_000)
}

export function startOfWeek(d: Date, weekStart: number): Date {
  const day = startOfDay(d)
  const diff = (day.getDay() - weekStart + 7) % 7
  return addDays(day, -diff)
}

/** The visible date range [start, end) of a view around `date`. */
export function viewRange(view: CalendarViewKind, date: Date, weekStart: number): { start: Date; end: Date } {
  if (view === 'day') {
    const s = startOfDay(date)
    return { start: s, end: addDays(s, 1) }
  }
  if (view === 'week') {
    const s = startOfWeek(date, weekStart)
    return { start: s, end: addDays(s, 7) }
  }
  if (view === 'month') {
    // Six full weeks, like the month grid.
    const s = startOfWeek(new Date(date.getFullYear(), date.getMonth(), 1), weekStart)
    return { start: s, end: addDays(s, 42) }
  }
  // Agenda: the next 30 days from the start of the day.
  const s = startOfDay(date)
  return { start: s, end: addDays(s, 30) }
}

/** Step the anchor date one view forward (+1) or back (-1). */
export function stepDate(view: CalendarViewKind, date: Date, dir: 1 | -1): Date {
  if (view === 'day') return addDays(date, dir)
  if (view === 'week') return addDays(date, 7 * dir)
  if (view === 'agenda') return addDays(date, 30 * dir)
  const d = new Date(date.getFullYear(), date.getMonth() + dir, 1)
  return d
}

/** Local time with its UTC offset, e.g. "2026-10-20T09:30:00+02:00". */
export function toRfc3339(d: Date): string {
  const off = -d.getTimezoneOffset()
  const sign = off >= 0 ? '+' : '-'
  const a = Math.abs(off)
  return `${toYmd(d)}T${toHm(d)}:${pad(d.getSeconds())}${sign}${pad(Math.floor(a / 60))}:${pad(a % 60)}`
}

/** Event start/end as Dates (all-day "YYYY-MM-DD" = local midnight). */
export function eventDate(value: string, allDay: boolean): Date {
  return allDay || /^\d{4}-\d{2}-\d{2}$/.test(value) ? fromYmd(value.slice(0, 10)) : new Date(value)
}

/** Does the event overlap [start, end)? */
export function overlaps(ev: Pick<CalendarEvent, 'start' | 'end' | 'allDay'>, start: Date, end: Date): boolean {
  const s = eventDate(ev.start, ev.allDay)
  let e = eventDate(ev.end || ev.start, ev.allDay)
  if (e <= s) e = ev.allDay ? addDays(s, 1) : addMinutes(s, 1)
  return s < end && e > start
}

// ------------------------------------------------------------------ time zones

function zoneParts(date: Date, timeZone: string): Record<string, number> {
  const fmt = new Intl.DateTimeFormat('en-US', {
    timeZone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
  const out: Record<string, number> = {}
  for (const p of fmt.formatToParts(date)) if (p.type !== 'literal') out[p.type] = Number(p.value)
  if (out.hour === 24) out.hour = 0
  return out
}

/** Offset of `timeZone` from UTC at `date`, in minutes (Europe/Amsterdam in summer = 120). */
export function zoneOffsetMinutes(timeZone: string, date: Date): number {
  const p = zoneParts(date, timeZone)
  const asUtc = Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second)
  return Math.round((asUtc - Math.floor(date.getTime() / 1000) * 1000) / 60_000)
}

/** The instant when the wall clock in `timeZone` shows `ymd hm`. */
export function zonedToUtc(ymd: string, hm: string, timeZone: string): Date {
  if (!timeZone) return fromYmd(ymd, hm)
  const [y, m, d] = ymd.split('-').map(Number)
  const [h, min] = hm.split(':').map(Number)
  const guess = Date.UTC(y, m - 1, d, h, min)
  // Two passes settle the offset across DST transitions.
  let t = guess - zoneOffsetMinutes(timeZone, new Date(guess)) * 60_000
  t = guess - zoneOffsetMinutes(timeZone, new Date(t)) * 60_000
  return new Date(t)
}

/** Wall-clock date/time of an instant in `timeZone` ("" = local). */
export function utcToZoned(date: Date, timeZone: string): { ymd: string; hm: string } {
  if (!timeZone) return { ymd: toYmd(date), hm: toHm(date) }
  const p = zoneParts(date, timeZone)
  return { ymd: `${p.year}-${pad(p.month)}-${pad(p.day)}`, hm: `${pad(p.hour)}:${pad(p.minute)}` }
}

/** Whether Intl knows the zone (an unknown one would throw in every conversion). */
export function isValidTimeZone(tz: string): boolean {
  if (!tz) return false
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz })
    return true
  } catch {
    return false
  }
}

export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

export const COMMON_TIME_ZONES = [
  'UTC', 'Europe/London', 'Europe/Lisbon', 'Europe/Amsterdam', 'Europe/Berlin', 'Europe/Paris', 'Europe/Madrid', 'Europe/Athens',
  'Europe/Istanbul', 'Africa/Johannesburg', 'Asia/Dubai', 'Asia/Kolkata', 'Asia/Singapore', 'Asia/Shanghai', 'Asia/Tokyo',
  'Australia/Sydney', 'Pacific/Auckland', 'America/St_Johns', 'America/Halifax', 'America/New_York', 'America/Chicago',
  'America/Denver', 'America/Los_Angeles', 'America/Anchorage', 'Pacific/Honolulu', 'America/Sao_Paulo', 'America/Mexico_City',
]

export function allTimeZones(): string[] {
  try {
    const list = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone')
    if (list?.length) return list
  } catch {
    /* fall through */
  }
  return COMMON_TIME_ZONES
}

// ------------------------------------------------------------------ recurrence rules

export type RepeatFreq = 'none' | 'daily' | 'weekly' | 'monthly' | 'yearly'

export interface RepeatRule {
  freq: RepeatFreq
  /** Inclusive last day "YYYY-MM-DD", or "" for no end. */
  until: string
  interval?: number
  /** Weekdays for weekly rules (0 = Sunday). */
  byDay?: number[]
  count?: number
}

const FREQS: Record<string, RepeatFreq> = { DAILY: 'daily', WEEKLY: 'weekly', MONTHLY: 'monthly', YEARLY: 'yearly' }
const DAYS = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA']

/** Repeat rule → RRULE value ("" for none). */
export function buildRRule(r: RepeatRule): string {
  if (r.freq === 'none') return ''
  const parts = [`FREQ=${r.freq.toUpperCase()}`]
  if (r.interval && r.interval > 1) parts.push(`INTERVAL=${r.interval}`)
  if (r.byDay?.length && r.freq === 'weekly') parts.push(`BYDAY=${r.byDay.map((d) => DAYS[d]).join(',')}`)
  if (r.until) parts.push(`UNTIL=${r.until.replace(/-/g, '')}`)
  else if (r.count) parts.push(`COUNT=${r.count}`)
  return parts.join(';')
}

/**
 * The last day an RRULE UNTIL allows ("YYYY-MM-DD", "" if unreadable). A UTC time
 * ("20261201T075959Z") is read in the event's zone ("" = local): the day of the last
 * possible occurrence. A DATE or a floating time is taken as is.
 */
export function untilDay(value: string, timeZone = ''): string {
  const m = /^(\d{4})(\d{2})(\d{2})(?:T(\d{2})(\d{2})(\d{2})(Z)?)?/i.exec(value.trim())
  if (!m) return ''
  if (!m[7]) return `${m[1]}-${m[2]}-${m[3]}`
  const at = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]))
  return utcToZoned(at, isValidTimeZone(timeZone) ? timeZone : '').ymd
}

/** RRULE value (with or without the "RRULE:" prefix) → repeat rule. Unknown rules map to "none".
 *  A UTC UNTIL is read in `timeZone`, the event's zone ("" = local). */
export function parseRRule(value: string, timeZone = ''): RepeatRule {
  const raw = (value ?? '').replace(/^RRULE:/i, '').trim()
  const out: RepeatRule = { freq: 'none', until: '' }
  if (!raw) return out
  for (const part of raw.split(';')) {
    const [k, v = ''] = part.split('=')
    const key = k.toUpperCase()
    if (key === 'FREQ') out.freq = FREQS[v.toUpperCase()] ?? 'none'
    else if (key === 'UNTIL') out.until = untilDay(v, timeZone)
    else if (key === 'INTERVAL') out.interval = Math.max(1, Number(v) || 1)
    else if (key === 'COUNT') out.count = Math.max(1, Number(v) || 1)
    else if (key === 'BYDAY') out.byDay = v.split(',').map((d) => DAYS.indexOf(d.slice(-2).toUpperCase())).filter((d) => d >= 0)
  }
  return out
}

/**
 * Rules the editor's Repeat menu (and the backend's frequency/until/count input) can
 * express without losing anything: one frequency, an end date or a count, weekly on
 * at most one day. Anything else ("every 2 weeks", "Mon, Wed, Fri", "2nd Tuesday")
 * is shown as is and left untouched on save.
 */
export function isSimpleRRule(value: string): boolean {
  const raw = (value ?? '').replace(/^RRULE:/i, '').trim()
  if (!raw) return true
  let freq = ''
  for (const part of raw.split(';')) {
    const [k, v = ''] = part.split('=')
    const key = k.toUpperCase()
    if (key === 'FREQ') freq = v.toUpperCase()
    else if (key === 'UNTIL' || key === 'COUNT' || key === 'WKST') continue
    else if (key === 'INTERVAL' && Number(v) === 1) continue
    else if (key === 'BYDAY' && !v.includes(',') && /^(SU|MO|TU|WE|TH|FR|SA)$/i.test(v)) continue
    else return false
  }
  if (!(freq in FREQS)) return false
  return freq === 'WEEKLY' || !/BYDAY=/i.test(raw)
}

/**
 * The recurrence to send when saving an event whose rule was `original`.
 * A rule the editor can't express ("custom") is sent as "" so the backend keeps the
 * provider's own rule (it rebuilds rules only from frequency/until/count, which would
 * turn "every 2 weeks" into "weekly"). A simple rule keeps its count and weekday.
 */
export function recurrenceForSave(original: string, freq: RepeatFreq | 'custom', until: string): string {
  if (freq === 'custom') return ''
  const r0 = parseRRule(original)
  const same = freq === r0.freq
  return buildRRule({ freq, until, byDay: same ? r0.byDay : undefined, count: same && !until ? r0.count : undefined })
}

export function describeRepeat(r: RepeatRule, locale?: string): string {
  if (r.freq === 'none') return 'Does not repeat'
  let every = r.interval && r.interval > 1 ? `Every ${r.interval} ${{ daily: 'days', weekly: 'weeks', monthly: 'months', yearly: 'years' }[r.freq]}` : { daily: 'Daily', weekly: 'Weekly', monthly: 'Monthly', yearly: 'Yearly' }[r.freq]
  // Weekly on several days: "Every weekday", "Weekly on Mon, Wed, Fri".
  if (r.freq === 'weekly' && (r.byDay?.length ?? 0) > 1) {
    const days = [...new Set(r.byDay)].sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7))
    const names = days.map((d) => new Date(2026, 0, 4 + d).toLocaleDateString(locale, { weekday: 'short' })) // 4 Jan 2026 is a Sunday
    every = days.join() === '1,2,3,4,5' && !(r.interval && r.interval > 1) ? 'Every weekday' : `${every} on ${names.join(', ')}`
  }
  const until = r.until ? ` until ${fromYmd(r.until).toLocaleDateString(locale, { day: 'numeric', month: 'short', year: 'numeric' })}` : r.count ? `, ${r.count} times` : ''
  return `${every}${until}`
}

/**
 * Occurrence start dates of a rule within [rangeStart, rangeEnd) (local wall clock,
 * the first occurrence is `first`). Exceptions are "YYYY-MM-DD" days to skip.
 */
export function expandOccurrences(first: Date, rule: RepeatRule, rangeStart: Date, rangeEnd: Date, exdates: string[] = [], max = 1000): Date[] {
  if (rule.freq === 'none') return first >= rangeStart && first < rangeEnd ? [first] : []
  const out: Date[] = []
  const interval = rule.interval ?? 1
  const untilEnd = rule.until ? addDays(fromYmd(rule.until), 1) : null
  const skip = new Set(exdates)
  let n = 0
  const push = (d: Date) => {
    n++
    if (untilEnd && d >= untilEnd) return false
    if (rule.count && n > rule.count) return false
    if (d >= rangeStart && d < rangeEnd && !skip.has(toYmd(d))) out.push(d)
    return d < rangeEnd
  }
  if (rule.freq === 'weekly' && rule.byDay?.length) {
    const days = [...rule.byDay].sort()
    let week = startOfWeek(first, 0)
    for (let i = 0; i < max; i++) {
      for (const wd of days) {
        const d = addDays(week, wd)
        d.setHours(first.getHours(), first.getMinutes(), 0, 0)
        if (d < first) continue
        if (!push(d)) return out
      }
      week = addDays(week, 7 * interval)
    }
    return out
  }
  for (let i = 0; i < max; i++) {
    let d: Date
    if (rule.freq === 'daily') d = addDays(first, i * interval)
    else if (rule.freq === 'weekly') d = addDays(first, i * 7 * interval)
    else if (rule.freq === 'monthly') {
      d = new Date(first)
      d.setDate(1)
      d.setMonth(first.getMonth() + i * interval)
      const dim = new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate()
      if (first.getDate() > dim) continue // e.g. the 31st in a 30-day month
      d.setDate(first.getDate())
    } else {
      d = new Date(first)
      d.setFullYear(first.getFullYear() + i * interval)
      if (d.getMonth() !== first.getMonth()) continue // Feb 29
    }
    if (!push(d)) break
  }
  return out
}

// ------------------------------------------------------------------ recurrence scope

/** Edits, moves and deletes of a recurring event ask "This event / All events". */
export function needsScopeChoice(ev: Pick<CalendarEvent, 'isRecurring' | 'seriesId'>): boolean {
  return !!ev.isRecurring
}

export function eventToInput(ev: CalendarEvent): EventInput {
  return {
    accountId: ev.accountId, calendarId: ev.calendarId, title: ev.title, start: ev.start, end: ev.end, allDay: ev.allDay, timeZone: ev.timeZone,
    location: ev.location, description: ev.description, attendees: ev.attendees.map((a) => ({ ...a })),
    // meetingUrl is only for a link the user typed: the event's own link stays where it is
    // (resending it would make the backend copy it into the location/description).
    meetingUrl: '',
    // A custom rule goes out as "" (the backend then keeps the provider's rule as is).
    recurrence: isSimpleRRule(ev.recurrence) ? dateUntil(ev.recurrence, ev.timeZone) : '', reminders: [...ev.reminders],
  }
}

/**
 * A rule with its UNTIL as a date ("UNTIL=20261130"), read in the event's zone: what the
 * editor and the backend input use. Resaving a series ("all events") then keeps its end.
 */
export function dateUntil(rule: string, timeZone = ''): string {
  return rule.replace(/(^|[;:])UNTIL=([^;]*)/i, (all, sep: string, v: string) => {
    const day = untilDay(v, timeZone)
    return day ? `${sep}UNTIL=${day.replace(/-/g, '')}` : all
  })
}

/** Shift an event's times by a drag/resize delta. `startDelta` moves the start; `endDelta` the end (ms, or days for all-day). */
export function shiftTimes(ev: Pick<CalendarEvent, 'start' | 'end' | 'allDay'>, startDeltaMs: number, endDeltaMs: number, toAllDay = ev.allDay): { start: string; end: string; allDay: boolean } {
  const s = eventDate(ev.start, ev.allDay)
  const e = eventDate(ev.end || ev.start, ev.allDay)
  if (toAllDay) {
    const days = (ms: number) => Math.round(ms / DAY_MS)
    const ns = addDays(startOfDay(s), days(startDeltaMs))
    let ne = addDays(startOfDay(ev.allDay ? e : addDays(e, 1)), days(endDeltaMs))
    if (ne <= ns) ne = addDays(ns, 1)
    return { start: toYmd(ns), end: toYmd(ne), allDay: true }
  }
  const ns = new Date(s.getTime() + startDeltaMs)
  let ne = new Date(e.getTime() + endDeltaMs)
  if (ne <= ns) ne = addMinutes(ns, 15)
  return { start: toRfc3339(ns), end: toRfc3339(ne), allDay: false }
}

/** Local "YYYY-MM-DD" days the events cover (each day of a multi-day event, up to 62). */
export function eventDays(events: Pick<CalendarEvent, 'start' | 'end' | 'allDay'>[]): Set<string> {
  const out = new Set<string>()
  for (const ev of events) {
    const s = eventDate(ev.start, ev.allDay)
    let e = eventDate(ev.end || ev.start, ev.allDay)
    if (Number.isNaN(s.getTime())) continue
    if (Number.isNaN(e.getTime()) || e <= s) e = addMinutes(s, 1)
    let d = startOfDay(s)
    for (let i = 0; i < 62 && d < e; i++, d = addDays(d, 1)) out.add(toYmd(d))
  }
  return out
}

// ------------------------------------------------------------------ time-of-day fields

/** "13:05" → "1:05 PM" (12-hour clock) or "13:05" (24-hour), following Settings → Time format. */
export function formatHm(hm: string, h12 = uses12h()): string {
  const [h, m] = hm.split(':').map(Number)
  if (!Number.isInteger(h) || !Number.isInteger(m)) return hm
  if (!h12) return `${pad(h)}:${pad(m)}`
  return `${h % 12 || 12}:${pad(m)} ${h < 12 ? 'AM' : 'PM'}`
}

/**
 * A typed time → "HH:MM", or null. Takes "13:30", "1:30 pm", "1pm", "9.30", "13h30",
 * "930", "0930", "noon" and "midnight"; a bare hour without am/pm is 24-hour ("9" = 09:00).
 */
export function parseTimeInput(text: string): string | null {
  const t = text.trim().toLowerCase().replace(/([ap])\.?\s*m\.?$/, '$1m').replace(/\s+/g, '')
  if (!t) return null
  if (t === 'noon' || t === 'midday') return '12:00'
  if (t === 'midnight') return '00:00'
  const m = /^(\d{1,2})(?:[:.h]?(\d{2}))?(am|pm|a|p)?$/.exec(t)
  if (!m) return null
  let h = Number(m[1])
  const min = Number(m[2] ?? 0)
  if (min > 59) return null
  if (m[3]) {
    if (h < 1 || h > 12) return null
    h = (h % 12) + (m[3].startsWith('p') ? 12 : 0)
  } else if (h > 23) return null
  return `${pad(h)}:${pad(min)}`
}

/** Times of day every `step` minutes ("00:00", "00:15", …). */
export function timeSlots(step = 15): string[] {
  return Array.from({ length: Math.ceil(1440 / step) }, (_, i) => `${pad(Math.floor((i * step) / 60))}:${pad((i * step) % 60)}`)
}

/** "30 min", "1 hr", "1.5 hrs", "2 hrs 10 min". */
export function formatDuration(min: number): string {
  if (min < 60) return `${min} min`
  const h = Math.floor(min / 60)
  const r = min % 60
  if (!r) return `${h} hr${h === 1 ? '' : 's'}`
  if (r === 30) return `${h}.5 hrs`
  return `${h} hr${h === 1 ? '' : 's'} ${r} min`
}

// ------------------------------------------------------------------ calendars

/** Calendars an event can be created in: editable calendars of enabled mailboxes with calendar access (hidden ones included). */
export function editableCalendars(calendars: Calendar[], accounts: Pick<Account, 'id' | 'calendarConsent' | 'calendarEnabled'>[]): Calendar[] {
  const ok = new Set(accounts.filter((a) => a.calendarConsent !== false && a.calendarEnabled !== false).map((a) => a.id))
  return calendars.filter((c) => c.canEdit && ok.has(c.accountId))
}

/** Default calendar for new events: the last used one, else a visible primary, else any visible, else the first editable. */
export function defaultCalendarId(calendars: Calendar[], accounts: Pick<Account, 'id' | 'calendarConsent' | 'calendarEnabled'>[], lastUsed = ''): string {
  const list = editableCalendars(calendars, accounts)
  if (lastUsed && list.some((c) => c.id === lastUsed && c.visible)) return lastUsed
  return (list.find((c) => c.primary && c.visible) ?? list.find((c) => c.visible) ?? list.find((c) => c.primary) ?? list[0])?.id ?? ''
}

/** Event visibility: calendar checkboxes (view filter), disabled mailboxes and the "show declined" setting. */
export function isEventShown(ev: CalendarEvent, calendars: Map<string, Calendar>, showDeclined: boolean, disabledAccounts?: Set<string>): boolean {
  const cal = calendars.get(ev.calendarId)
  if (!cal || !cal.visible) return false
  if (disabledAccounts?.has(ev.accountId)) return false
  if (ev.status === 'cancelled') return false
  if (!showDeclined && ev.myResponse === 'declined') return false
  return true
}

// ------------------------------------------------------------------ display

const LOCALE = () => (typeof navigator !== 'undefined' ? navigator.language : 'en-US')

/** Intl options for the user's clock. `hourCycle: 'h23'` rather than `hour12: false`, which
 *  some engines render as "24:05" after midnight. */
export function clockOptions(locale?: string): Intl.DateTimeFormatOptions {
  return uses12h(locale) ? { hour: 'numeric', minute: '2-digit', hour12: true } : { hour: 'numeric', minute: '2-digit', hourCycle: 'h23' }
}

export function formatClock(d: Date, locale = LOCALE()): string {
  return d.toLocaleTimeString(locale, clockOptions(locale))
}

/** "Tue 20 Oct · 14:00 – 15:00", "Tue 20 Oct (all day)", "Wed 28 – Fri 30 Oct"; `long` spells out the day and month ("Tuesday 20 October"). */
export function formatEventWhen(ev: Pick<CalendarEvent, 'start' | 'end' | 'allDay'>, locale = LOCALE(), long = false): string {
  const s = eventDate(ev.start, ev.allDay)
  const e = eventDate(ev.end || ev.start, ev.allDay)
  const day = (d: Date) => d.toLocaleDateString(locale, long ? { weekday: 'long', day: 'numeric', month: 'long' } : { weekday: 'short', day: 'numeric', month: 'short' })
  if (ev.allDay) {
    const last = addDays(e, -1)
    if (last <= s) return `${day(s)} · All day`
    return `${day(s)} – ${day(last)}`
  }
  if (e.getTime() <= s.getTime()) return `${day(s)} · ${formatClock(s, locale)}`
  if (toYmd(s) === toYmd(e)) return `${day(s)} · ${formatClock(s, locale)} – ${formatClock(e, locale)}`
  return `${day(s)} ${formatClock(s, locale)} – ${day(e)} ${formatClock(e, locale)}`
}

/**
 * The same times on the event's own clock, when it was scheduled in another
 * time zone than ours ("09:00 – 10:00 New York time"); "" when the zones agree
 * at that moment, for all-day events, or when the zone is unknown.
 */
export function formatEventWhenInZone(ev: Pick<CalendarEvent, 'start' | 'end' | 'allDay' | 'timeZone'>, locale = LOCALE(), here = localTimeZone()): string {
  const tz = ev.timeZone
  if (ev.allDay || !tz || tz === here || tz === 'UTC' && !here) return ''
  const s = new Date(ev.start)
  const e = new Date(ev.end || ev.start)
  if (Number.isNaN(s.getTime())) return ''
  try {
    if (zoneOffsetMinutes(tz, s) === zoneOffsetMinutes(here || 'UTC', s)) return ''
    const opts = clockOptions(locale)
    const clock = (d: Date) => d.toLocaleTimeString(locale, { ...opts, timeZone: tz })
    const day = (d: Date) => d.toLocaleDateString(locale, { weekday: 'short', day: 'numeric', month: 'short', timeZone: tz })
    const city = (tz.split('/').pop() ?? tz).replace(/_/g, ' ')
    const sameDay = day(s) === day(e)
    const range = e.getTime() <= s.getTime() ? clock(s) : sameDay ? `${clock(s)} – ${clock(e)}` : `${clock(s)} – ${day(e)} ${clock(e)}`
    return `${day(s)} · ${range} ${city} time`
  } catch {
    return ''
  }
}

export function formatReminder(min: number): string {
  if (min < 0) return 'None'
  if (min === 0) return 'At start'
  if (min % 1440 === 0) return `${min / 1440} day${min === 1440 ? '' : 's'} before`
  if (min % 60 === 0) return `${min / 60} hour${min === 60 ? '' : 's'} before`
  return `${min} minutes before`
}

export const REMINDER_CHOICES = [0, 5, 10, 15, 30, 60, 120, 1440, 2880]
export const DURATION_CHOICES = [15, 30, 45, 60, 90, 120]

/** Whole calendar days from `today` to `day` (local dates, DST-safe). */
export function daysBetween(today: Date, day: Date): number {
  const a = Date.UTC(today.getFullYear(), today.getMonth(), today.getDate())
  const b = Date.UTC(day.getFullYear(), day.getMonth(), day.getDate())
  return Math.round((b - a) / 86_400_000)
}

/** Agenda hint next to a date: "Tomorrow", "in 5 days", "in 2 weeks and 3 days", "in about 3 months",
 *  "3 days ago"; "" for today (it has its own badge). */
export function relativeDay(day: Date, today = new Date()): string {
  const n = daysBetween(today, day)
  if (n === 0) return ''
  if (n === 1) return 'Tomorrow'
  if (n === -1) return 'Yesterday'
  const d = Math.abs(n)
  const unit = (k: number, word: string) => `${k} ${word}${k === 1 ? '' : 's'}`
  let text: string
  if (d < 7) text = unit(d, 'day')
  else if (d < 56) {
    const w = Math.floor(d / 7)
    const r = d % 7
    text = r ? `${unit(w, 'week')} and ${unit(r, 'day')}` : unit(w, 'week')
  } else text = `about ${unit(Math.round(d / 30.44), 'month')}`
  return n > 0 ? `in ${text}` : `${text} ago`
}
