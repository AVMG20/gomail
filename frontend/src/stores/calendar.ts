import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod, readLocal, writeLocal } from '@/lib/api'
import {
  defaultCalendarId, editableCalendars, effectiveWeekStart, eventDate, eventToInput, isEventShown, type CalendarViewKind,
} from '@/lib/calendar'
import type { AttendeeResponse, Calendar, CalendarEvent, EventInput, RecurrenceScope } from '@/lib/types'
import { useAccountsStore } from './accounts'
import { useSettingsStore } from './settings'

/** Unified calendar across all mailboxes (independent of the active mailbox). */
/** Undo toasts stay long enough to reach the button. */
const UNDO_MS = 8000

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
  /** Your own copy of any event on a writable calendar, invitations too: its reminders, and removing it. */
  function canManageEvent(ev: CalendarEvent): boolean {
    return !!calendarMap.value.get(ev.calendarId)?.canEdit
  }

  const available = computed(() => hasMethod('ListCalendars'))
  const calendarMap = computed(() => new Map(calendars.value.map((c) => [c.id, c])))
  const weekStart = computed(() => effectiveWeekStart(settings.settings?.calendarWeekStart))
  const showDeclined = computed(() => !!settings.settings?.calendarShowDeclined)

  const disabledAccounts = computed(() => new Set(accounts.accounts.filter((a) => a.calendarEnabled === false).map((a) => a.id)))
  const visibleEvents = computed(() => events.value.filter((e) => isEventShown(e, calendarMap.value, showDeclined.value, disabledAccounts.value)))
  const writableCalendars = computed(() => editableCalendars(calendars.value, accounts.accounts))
  const defaultCalendar = computed(() => defaultCalendarId(calendars.value, accounts.accounts, lastCalendarId.value))

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

  async function setCalendarColor(id: string, color: string) {
    const before = calendars.value
    if (color) calendars.value = calendars.value.map((c) => (c.id === id ? { ...c, color } : c))
    try {
      await api.setCalendarColor(id, color)
      if (!color) await loadCalendars() // back to the provider's color
    } catch (e) {
      calendars.value = before
      toast.error('Couldn’t change the color', { description: errorMessage(e) })
    }
  }

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

  /** Mailbox menu: disable (no sync, no events, no reminders) or enable again, with Undo. */
  async function setAccountEnabled(accountId: string, enabled: boolean, undoable = true) {
    const a = accounts.accounts.find((x) => x.id === accountId)
    if (!a) return
    const was = a.calendarEnabled
    a.calendarEnabled = enabled
    try {
      await api.setAccountCalendarEnabled(accountId, enabled)
      if (enabled) void refresh()
      if (undoable) {
        toast(enabled ? `Calendar enabled for ${a.email}` : `Calendar disabled for ${a.email}`, {
          description: enabled ? undefined : 'Its events are hidden and it no longer syncs or sends reminders.',
          duration: UNDO_MS,
          action: { label: 'Undo', onClick: () => void setAccountEnabled(accountId, !enabled, false) },
        })
      }
    } catch (e) {
      a.calendarEnabled = was
      toast.error('Couldn’t update the mailbox', { description: errorMessage(e) })
    }
  }

  async function setAccountMuted(accountId: string, muted: boolean) {
    const a = accounts.accounts.find((x) => x.id === accountId)
    if (!a) return
    const was = a.calendarMuted
    a.calendarMuted = muted
    try {
      await api.setAccountCalendarMuted(accountId, muted)
      toast(muted ? `Reminders muted for ${a.email}` : `Reminders on for ${a.email}`)
    } catch (e) {
      a.calendarMuted = was
      toast.error('Couldn’t update the mailbox', { description: errorMessage(e) })
    }
  }

  /** "Show only this mailbox": check its calendars, uncheck everyone else's. */
  async function soloAccount(accountId: string) {
    const changes = calendars.value.filter((c) => c.visible !== (c.accountId === accountId))
    calendars.value = calendars.value.map((c) => ({ ...c, visible: c.accountId === accountId }))
    try {
      await Promise.all(changes.map((c) => api.setCalendarVisible(c.id, c.accountId === accountId)))
    } catch (e) {
      toast.error('Couldn’t update the calendars', { description: errorMessage(e) })
      void loadCalendars()
    }
  }

  async function syncAccount(accountId: string) {
    try {
      await api.syncCalendars(accountId)
      await refresh()
      toast('Calendar synced')
    } catch (e) {
      toast.error('Couldn’t sync the calendar', { description: errorMessage(e) })
    }
  }

  /** Alt/Option-click: show only this calendar (or everything again when it already is the only one). */
  async function soloCalendar(id: string) {
    const onlyThis = calendars.value.every((c) => c.visible === (c.id === id))
    const target = new Map(calendars.value.map((c) => [c.id, onlyThis ? true : c.id === id]))
    const changes = calendars.value.filter((c) => c.visible !== target.get(c.id))
    calendars.value = calendars.value.map((c) => ({ ...c, visible: target.get(c.id)! }))
    try {
      await Promise.all(changes.map((c) => api.setCalendarVisible(c.id, target.get(c.id)!)))
    } catch (e) {
      toast.error('Couldn’t update the calendars', { description: errorMessage(e) })
      void loadCalendars()
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
      const target = calendarMap.value.get(created.calendarId) ?? cal
      if (target && !target.visible) {
        toast(`Event created in ${target.name}, which is hidden. Show it?`, {
          description: created.title,
          action: { label: 'Show', onClick: () => void setCalendarVisible(target.id, true) },
        })
      } else {
        toast.success('Event created', {
          description: created.title,
          duration: UNDO_MS,
          action: { label: 'Undo', onClick: () => void remove(created, 'series', true) },
        })
      }
      return created
    } catch (e) {
      replaceLocal(temp.id, null)
      toast.error('Couldn’t create the event', { description: errorMessage(e) })
      return null
    }
  }

  /**
   * Save edits. Always addressed to the event the user is looking at: with scope "series"
   * the backend finds the series from this occurrence and moves every occurrence by the
   * change made to this one (the series keeps its own first date). Series edits refetch.
   */
  async function update(ev: CalendarEvent, input: EventInput, scope: RecurrenceScope, quiet = false): Promise<boolean> {
    rememberCalendar(input.calendarId)
    const series = scope === 'series' && ev.isRecurring
    if (!series) {
      replaceLocal(ev.id, { ...ev, ...input, accountId: calendarMap.value.get(input.calendarId)?.accountId ?? ev.accountId })
    }
    try {
      const saved = await api.updateEvent(ev.id, withAccount(input), scope)
      if (series) await fetchEvents()
      else replaceLocal(ev.id, saved)
      if (!quiet) toast.success('Event saved')
      return true
    } catch (e) {
      // Put back just this event (a range fetch may have replaced the list meanwhile).
      if (events.value.some((x) => x.id === ev.id)) replaceLocal(ev.id, ev)
      if (!conflict(e)) toast.error('Couldn’t save the event', { description: errorMessage(e) })
      return false
    }
  }

  /** Drag-move / resize with an Undo toast. */
  async function move(ev: CalendarEvent, times: Pick<EventInput, 'start' | 'end' | 'allDay'>, scope: RecurrenceScope, kind: 'drop' | 'resize' = 'drop'): Promise<boolean> {
    const ok = await update(ev, { ...eventToInput(ev), ...times }, scope, true)
    if (!ok) return false
    const series = scope === 'series' && ev.isRecurring
    toast(kind === 'resize' ? 'Event time changed' : 'Event moved', {
      description: ev.title,
      duration: UNDO_MS,
      action: { label: 'Undo', onClick: () => void undoMove(ev, times, series) },
    })
    return true
  }

  /** Put a moved event back. A moved series has new occurrence ids: find the occurrence where it landed. */
  async function undoMove(ev: CalendarEvent, times: Pick<EventInput, 'start' | 'end' | 'allDay'>, series: boolean) {
    let moved: CalendarEvent | undefined = { ...ev, ...times }
    if (series) {
      const at = eventDate(times.start, times.allDay).getTime()
      moved = events.value.find((e) => (e.seriesId || e.id) === (ev.seriesId || ev.id) && e.allDay === times.allDay && eventDate(e.start, e.allDay).getTime() === at)
      if (!moved) {
        toast('Couldn’t undo the move', { description: 'Open the event and change its time instead.' })
        return
      }
    }
    await update(moved, { ...eventToInput(moved), start: ev.start, end: ev.end, allDay: ev.allDay }, series ? 'series' : 'this', true)
  }

  async function remove(ev: CalendarEvent, scope: RecurrenceScope, fromUndo = false) {
    if (scope === 'series' && ev.isRecurring) events.value = events.value.filter((e) => (e.seriesId || e.id) !== (ev.seriesId || ev.id))
    else replaceLocal(ev.id, null)
    try {
      await api.deleteEvent(ev.id, scope)
      if (fromUndo) return
      // Your own single events can be restored by recreating them; occurrences
      // of a series and invitations (you'd become their organizer) can't.
      const undoable = !ev.isRecurring && canEditEvent(ev)
      toast(canEditEvent(ev) ? 'Event deleted' : 'Removed from your calendar', {
        description: ev.title,
        duration: UNDO_MS,
        action: undoable ? { label: 'Undo', onClick: () => void create(eventToInput(ev)) } : undefined,
      })
    } catch (e) {
      if (scope === 'series' && ev.isRecurring) void fetchEvents()
      else if (!events.value.some((x) => x.id === ev.id)) events.value = [...events.value, ev]
      if (!conflict(e)) toast.error('Couldn’t delete the event', { description: errorMessage(e) })
    }
  }

  /** Your own reminders (minutes before; [] = none), invitations too. */
  async function setReminders(ev: CalendarEvent, minutes: number[], scope: RecurrenceScope): Promise<boolean> {
    const series = scope === 'series' && ev.isRecurring
    const key = ev.seriesId || ev.id
    const hit = (e: CalendarEvent) => (series ? (e.seriesId || e.id) === key : e.id === ev.id)
    const before = events.value.filter(hit)
    events.value = events.value.map((e) => (hit(e) ? { ...e, reminders: [...minutes] } : e))
    try {
      const saved = await api.setEventReminders(ev.id, minutes, scope)
      if (series) await fetchEvents()
      else replaceLocal(ev.id, saved)
      return true
    } catch (e) {
      const old = new Map(before.map((x) => [x.id, x]))
      events.value = events.value.map((x) => old.get(x.id) ?? x)
      if (!conflict(e)) toast.error('Couldn’t change the reminder', { description: errorMessage(e) })
      return false
    }
  }

  /** Answer an invitation (the whole series for a recurring one). Resolves to "" on success, else the error. */
  async function respond(ev: CalendarEvent, response: AttendeeResponse): Promise<string> {
    const series = ev.seriesId || ev.id
    const setResponse = (r: AttendeeResponse) => (events.value = events.value.map((e) => ((e.seriesId || e.id) === series ? { ...e, myResponse: r } : e)))
    setResponse(response)
    try {
      await api.respondToEvent(ev.id, response)
      toast.success(response === 'accepted' ? 'Accepted' : response === 'declined' ? 'Declined' : 'Marked as maybe', { description: ev.title })
      return ''
    } catch (e) {
      setResponse(ev.myResponse)
      const msg = errorMessage(e) || 'Couldn’t send your response'
      toast.error('Couldn’t send your response', { description: msg })
      return msg
    }
  }

  return {
    calendars, events, range, loading, error, loaded, connecting, view, lastCalendarId, focusDate, openEventId, canEditEvent, canManageEvent, available, calendarMap, weekStart, showDeclined,
    visibleEvents, writableCalendars, defaultCalendar, calendarsOf, loadCalendars, loadRange, fetchEvents, refresh, setCalendarVisible, setCalendarColor,
    soloCalendar, setAccountEnabled, setAccountMuted, soloAccount, syncAccount, connect, create, update, move, remove, respond, setReminders,
  }
})
