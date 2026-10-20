import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod, readLocal, writeLocal } from '@/lib/api'
import {
  defaultCalendarId, editableCalendars, effectiveWeekStart, eventDate, eventToInput, isEventShown, scopeTargetId, shiftTimes, type CalendarViewKind,
} from '@/lib/calendar'
import type { AttendeeResponse, Calendar, CalendarEvent, EventInput, RecurrenceScope } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useSettingsStore } from './settings'

/** Unified calendar across all mailboxes (independent of the active mailbox). */
export const useCalendarStore = defineStore('calendar', () => {
  const accounts = useAccountsStore()
  const settings = useSettingsStore()

  const calendars = shallowRef<Calendar[]>([])
  const events = shallowRef<CalendarEvent[]>([])
  const range = ref<{ start: Date; end: Date } | null>(null)
  const loading = ref(false)
  const error = ref('')
  const loaded = ref(false)
  const connecting = ref<string | null>(null)
  const view = ref<CalendarViewKind>((settings.settings?.calendarDefaultView as CalendarViewKind) || 'week')
  const lastCalendarId = ref<string>(readLocal('lastCalendarId', ''))
  /** Ask the calendar view to jump to a date ("View in calendar" on an invite). */
  const focusDate = ref<Date | null>(null)
  /** Ask the calendar view to show an event's details (reminder notification click). */
  const openEventId = ref<string | null>(null)

  /** The backend refuses stale writes (HTTP 412): reload and say so. */
  function conflict(e: unknown): boolean {
    if (!/changed elsewhere|412|precondition/i.test(errorMessage(e))) return false
    void fetchEvents()
    toast('This event changed elsewhere', { description: 'We loaded the latest version. Please make your change again.' })
    return true
  }

  /** Who may edit an event: the backend says so; older backends: organizer on an editable calendar. */
  function canEditEvent(ev: CalendarEvent): boolean {
    if (ev.canEdit !== undefined) return ev.canEdit
    return !!calendarMap.value.get(ev.calendarId)?.canEdit && ev.isOrganizer
  }

  const available = computed(() => hasMethod('ListCalendars'))
  const calendarMap = computed(() => new Map(calendars.value.map((c) => [c.id, c])))
  const weekStart = computed(() => effectiveWeekStart(settings.settings?.calendarWeekStart))
  const showDeclined = computed(() => !!settings.settings?.calendarShowDeclined)
  const accountMap = computed(() => new Map(accounts.accounts.map((a) => [a.id, a])))

  const visibleEvents = computed(() =>
    events.value.filter((e) => isEventShown(e, calendarMap.value, accountMap.value, showDeclined.value)),
  )
  const writableCalendars = computed(() => editableCalendars(calendars.value, accounts.accounts.filter((a) => a.calendarConsent !== false)))
  const defaultCalendar = computed(() => defaultCalendarId(calendars.value, accounts.accounts.filter((a) => a.calendarConsent !== false), lastCalendarId.value))

  // The backend leaves declined invitations out unless asked: refetch when the setting flips.
  watch(showDeclined, () => void fetchEvents())

  function calendarsOf(accountId: string): Calendar[] {
    return calendars.value.filter((c) => c.accountId === accountId)
  }

  // ------------------------------------------------------------ loading
  let token = 0

  async function loadCalendars() {
    if (!available.value) return
    try {
      calendars.value = await api.listCalendars()
      loaded.value = true
    } catch (e) {
      error.value = errorMessage(e)
    }
  }

  async function loadRange(start: Date, end: Date) {
    range.value = { start, end }
    await fetchEvents()
  }

  async function fetchEvents() {
    if (!range.value || !available.value) return
    const t = ++token
    loading.value = true
    try {
      const list = await api.listEvents(range.value.start.toISOString(), range.value.end.toISOString())
      if (t !== token) return // the user moved on to another range
      events.value = list
      error.value = ''
    } catch (e) {
      if (t === token) error.value = errorMessage(e)
    } finally {
      if (t === token) loading.value = false
    }
  }

  async function refresh() {
    await Promise.all([loadCalendars(), fetchEvents()])
  }

  // ------------------------------------------------------------ toggles (optimistic)

  async function setCalendarVisible(id: string, visible: boolean) {
    const before = calendars.value
    calendars.value = calendars.value.map((c) => (c.id === id ? { ...c, visible } : c))
    try {
      await api.setCalendarVisible(id, visible)
    } catch (e) {
      calendars.value = before
      toast.error('Couldn’t update the calendar', { description: errorMessage(e) })
    }
  }

  async function setAccountEnabled(accountId: string, enabled: boolean) {
    const a = accounts.accounts.find((x) => x.id === accountId)
    if (!a) return
    const was = a.calendarEnabled
    a.calendarEnabled = enabled
    try {
      await api.setAccountCalendarEnabled(accountId, enabled)
      if (enabled) void fetchEvents()
    } catch (e) {
      a.calendarEnabled = was
      toast.error('Couldn’t update the mailbox', { description: errorMessage(e) })
    }
  }

  async function connect(accountId: string) {
    connecting.value = accountId
    try {
      await api.enableCalendar(accountId)
      await accounts.reloadAccounts()
      await refresh()
      toast.success('Calendar connected')
    } catch (e) {
      toast.error('Couldn’t connect the calendar', { description: errorMessage(e) })
    } finally {
      connecting.value = null
    }
  }

  // ------------------------------------------------------------ CRUD

  /** The backend addresses calendars per mailbox: always send the calendar's account. */
  function withAccount(input: EventInput): EventInput {
    return { ...input, accountId: calendarMap.value.get(input.calendarId)?.accountId ?? input.accountId ?? '' }
  }

  function rememberCalendar(id: string) {
    lastCalendarId.value = id
    writeLocal('lastCalendarId', id)
  }

  function replaceLocal(id: string, next: CalendarEvent | null) {
    events.value = next ? events.value.map((e) => (e.id === id ? next : e)) : events.value.filter((e) => e.id !== id)
  }

  async function create(input: EventInput): Promise<CalendarEvent | null> {
    rememberCalendar(input.calendarId)
    const cal = calendarMap.value.get(input.calendarId)
    const temp: CalendarEvent = {
      ...input, id: `tmp_${Date.now()}`, accountId: cal?.accountId ?? '', organizer: { name: '', email: '' }, isOrganizer: true, myResponse: '',
      isRecurring: !!input.recurrence, seriesId: '', status: 'confirmed',
    }
    events.value = [...events.value, temp]
    try {
      const created = await api.createEvent(withAccount(input))
      if (created.isRecurring) {
        replaceLocal(temp.id, null)
        void fetchEvents() // occurrences come from the backend
      } else replaceLocal(temp.id, created)
      toast.success('Event created', {
        description: created.title,
        action: { label: 'Undo', onClick: () => void remove(created, 'series', true) },
      })
      return created
    } catch (e) {
      replaceLocal(temp.id, null)
      toast.error('Couldn’t create the event', { description: errorMessage(e) })
      return null
    }
  }

  /** Save edits. Recurring + "series" refetches (every occurrence changes). */
  async function update(ev: CalendarEvent, input: EventInput, scope: RecurrenceScope, quiet = false, seriesTimesResolved = false): Promise<boolean> {
    rememberCalendar(input.calendarId)
    const before = events.value
    if (scope === 'series' && ev.isRecurring && !seriesTimesResolved) {
      // The editor shows one occurrence; for "all events" apply its time change to the
      // series' own first date instead of moving the whole series to this occurrence.
      try {
        const master = await api.getEvent(ev.seriesId || ev.id)
        const ds = eventDate(input.start, input.allDay).getTime() - eventDate(ev.start, ev.allDay).getTime()
        const de = eventDate(input.end, input.allDay).getTime() - eventDate(ev.end, ev.allDay).getTime()
        input = { ...input, ...shiftTimes(master, ds, de, input.allDay) }
      } catch (e) {
        toast.error('Couldn’t load the series', { description: errorMessage(e) })
        return false
      }
    }
    if (scope === 'this' || !ev.isRecurring) {
      replaceLocal(ev.id, { ...ev, ...input, accountId: calendarMap.value.get(input.calendarId)?.accountId ?? ev.accountId })
    }
    try {
      const saved = await api.updateEvent(scopeTargetId(ev, scope), withAccount(input), scope)
      if (scope === 'series' && ev.isRecurring) await fetchEvents()
      else replaceLocal(ev.id, saved)
      if (!quiet) toast.success('Event saved')
      return true
    } catch (e) {
      events.value = before
      if (!conflict(e)) toast.error('Couldn’t save the event', { description: errorMessage(e) })
      return false
    }
  }

  /**
   * Drag-move / resize with an Undo toast. For a whole series, pass the master's
   * new input and its input before the move (Undo writes that back).
   */
  async function move(ev: CalendarEvent, times: Pick<EventInput, 'start' | 'end' | 'allDay'>, scope: RecurrenceScope, series?: { next: EventInput; before: EventInput }) {
    const input = series ? series.next : { ...eventToInput(ev), ...times }
    const ok = await update(ev, input, scope, true, !!series)
    if (!ok) return
    toast('Event moved', {
      description: ev.title,
      action: {
        label: 'Undo',
        onClick: () => void (series ? update(ev, series.before, 'series', true, true) : update({ ...ev, ...times }, eventToInput(ev), scope, true)),
      },
    })
  }

  async function remove(ev: CalendarEvent, scope: RecurrenceScope, fromUndo = false) {
    const before = events.value
    if (scope === 'series' && ev.isRecurring) events.value = events.value.filter((e) => (e.seriesId || e.id) !== (ev.seriesId || ev.id))
    else replaceLocal(ev.id, null)
    try {
      await api.deleteEvent(scopeTargetId(ev, scope), scope)
      if (fromUndo) return
      // Single events can be restored by recreating them; occurrences of a series can't.
      const undoable = !ev.isRecurring
      toast('Event deleted', {
        description: ev.title,
        action: undoable ? { label: 'Undo', onClick: () => void create(eventToInput(ev)) } : undefined,
      })
    } catch (e) {
      events.value = before
      if (!conflict(e)) toast.error('Couldn’t delete the event', { description: errorMessage(e) })
    }
  }

  async function respond(ev: CalendarEvent, response: AttendeeResponse) {
    const before = events.value
    const series = ev.seriesId || ev.id
    events.value = events.value.map((e) => ((e.seriesId || e.id) === series ? { ...e, myResponse: response } : e))
    try {
      await api.respondToEvent(ev.id, response)
      toast.success(response === 'accepted' ? 'Accepted' : response === 'declined' ? 'Declined' : 'Marked as maybe', { description: ev.title })
    } catch (e) {
      events.value = before
      toast.error('Couldn’t send your response', { description: errorMessage(e) })
    }
  }

  return {
    calendars, events, range, loading, error, loaded, connecting, view, lastCalendarId, focusDate, openEventId, canEditEvent, available, calendarMap, weekStart, showDeclined,
    visibleEvents, writableCalendars, defaultCalendar, calendarsOf, loadCalendars, loadRange, fetchEvents, refresh, setCalendarVisible,
    setAccountEnabled, connect, create, update, move, remove, respond,
  }
})
