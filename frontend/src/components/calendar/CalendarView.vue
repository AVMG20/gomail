<script setup lang="ts">
// The unified calendar (lazy chunk). FullCalendar renders the grids; everything
// around it (header, sidebar, popovers, editor) is ours and uses the app's tokens.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import FullCalendar from '@fullcalendar/vue3'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import listPlugin from '@fullcalendar/list'
import interactionPlugin from '@fullcalendar/interaction'
import allLocales from '@fullcalendar/core/locales-all'
import type { CalendarApi, CalendarOptions, DateSelectArg, DatesSetArg, EventClickArg, EventContentArg, EventDropArg, EventMountArg, DayHeaderContentArg } from '@fullcalendar/core'
import type { EventResizeDoneArg } from '@fullcalendar/interaction'
import { ChevronLeftIcon, ChevronRightIcon, CalendarOffIcon, LoaderIcon, PanelLeftIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import Tip from '@/components/common/Tip.vue'
import type { AnchorRect } from '@/components/common/FloatingCard.vue'
import { api, errorMessage } from '@/lib/api'
import {
  addDays, addMinutes, needsScopeChoice, relativeDay, startOfDay, uses12h, toRfc3339, toYmd, type CalendarViewKind,
} from '@/lib/calendar'
import { escapeHtml } from '@/lib/format'
import { isEditableTarget } from '@/lib/platform'
import type { CalendarEvent, RecurrenceScope } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import CalendarSidebar from './CalendarSidebar.vue'
import EventDetails from './EventDetails.vue'
import EventEditor from './EventEditor.vue'
import QuickCreate from './QuickCreate.vue'
import ScopePrompt from './ScopePrompt.vue'
import { editorRequest, openEditor } from './editorState'
import { askScope } from './scopePrompt'
import './calendar.css'

const cal = useCalendarStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()

const fc = ref<InstanceType<typeof FullCalendar> | null>(null)
const api$ = (): CalendarApi | undefined => (fc.value as unknown as { getApi?: () => CalendarApi } | null)?.getApi?.()
const title = ref('')
const anchorDate = ref(new Date())
const visibleRange = shallowRef<{ start: Date; end: Date } | null>(null)
const sidebarOpen = ref(!ui.narrow)
// When the window gets narrow the sidebar would cover the grid: tuck it away.
watch(() => ui.narrow, (n) => n && (sidebarOpen.value = false))
const locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'

const FC_VIEW: Record<CalendarViewKind, string> = { day: 'timeGridDay', week: 'timeGridWeek', month: 'dayGridMonth', agenda: 'listAgenda' }
const VIEWS: { id: CalendarViewKind; label: string; key: string }[] = [
  { id: 'day', label: 'Day', key: 'd' }, { id: 'week', label: 'Week', key: 'w' }, { id: 'month', label: 'Month', key: 'm' }, { id: 'agenda', label: 'Agenda', key: 'a' },
]

// ------------------------------------------------------------ events → FullCalendar

const fcEvents = computed(() =>
  cal.visibleEvents.map((e) => {
    const c = cal.calendarMap.get(e.calendarId)
    const editable = cal.canEditEvent(e) && !e.id.startsWith('tmp_')
    return {
      id: e.id, title: e.title || 'Untitled event', start: e.start, end: e.end || undefined, allDay: e.allDay,
      editable, startEditable: editable, durationEditable: editable,
      classNames: [
        e.myResponse === 'declined' ? 'gm-ev-declined' : '',
        e.myResponse === 'needsAction' ? 'gm-ev-pending' : '',
        e.myResponse === 'tentative' || e.status === 'tentative' ? 'gm-ev-tentative' : '',
        e.id.startsWith('tmp_') ? 'gm-ev-saving' : '',
      ].filter(Boolean),
      extendedProps: { ev: e, color: c?.color ?? '#64748b' },
    }
  }),
)

// ------------------------------------------------------------ popovers

const quick = shallowRef<{ anchor: AnchorRect; start: Date; end: Date; allDay: boolean } | null>(null)
const details = shallowRef<{ anchor: AnchorRect; event: CalendarEvent } | null>(null)

function pointRect(e?: MouseEvent | null): AnchorRect {
  if (e && 'clientX' in e) return { left: e.clientX, top: e.clientY - 12, width: 1, height: 1 }
  const host = document.querySelector('.gm-calendar') as HTMLElement | null
  const r = host?.getBoundingClientRect()
  return { left: (r?.left ?? 300) + (r?.width ?? 600) / 2 - 160, top: (r?.top ?? 100) + 80, width: 1, height: 1 }
}

// A click on the grid while a card is open only closes that card (the card
// closes on pointer-down; FullCalendar would then turn the same click into a
// new selection and open another card). Watched on window, ahead of the card.
let dismissing = false
function onPointerDown() {
  dismissing = !!(quick.value || details.value)
}
function onPointerUp() {
  if (dismissing) setTimeout(() => { dismissing = false }, 0)
}

let reselecting = false
function onSelect(arg: DateSelectArg) {
  if (reselecting) return
  if (dismissing) {
    api$()?.unselect()
    return
  }
  details.value = null
  if (!cal.writableCalendars.length) {
    toast('No editable calendar', { description: 'Turn on a mailbox with an editable calendar in the sidebar.' })
    api$()?.unselect()
    return
  }
  // In month view a click selects whole days; offer a timed event at the usual hour.
  let { start, end, allDay } = arg
  const oneDay = allDay && end.getTime() - start.getTime() === 86_400_000
  if (oneDay && arg.view.type === 'dayGridMonth') {
    allDay = false
    start = new Date(start)
    start.setHours(9, 0, 0, 0)
    end = addMinutes(start, settings.settings?.calendarDefaultDuration || 30)
  } else if (!allDay && end.getTime() - start.getTime() <= 30 * 60_000) {
    end = addMinutes(start, settings.settings?.calendarDefaultDuration || 30)
    // Show the block with the duration the event will get (a click selects one 15-minute slot).
    if (end.getTime() !== arg.end.getTime()) {
      reselecting = true
      api$()?.select(start, end)
      reselecting = false
    }
  }
  quick.value = { anchor: pointRect(arg.jsEvent as MouseEvent | null), start, end, allDay }
}

function closeQuick() {
  quick.value = null
  api$()?.unselect()
}

function onEventClick(arg: EventClickArg) {
  arg.jsEvent.preventDefault()
  quick.value = null
  const r = arg.el.getBoundingClientRect()
  // An agenda row spans the whole list: open the card next to the click, not beside the row.
  const x = arg.view.type.startsWith('list') ? arg.jsEvent.clientX : r.left
  const anchor = { left: x, top: r.top, width: x === r.left ? r.width : 1, height: r.height }
  details.value = { anchor, event: arg.event.extendedProps.ev as CalendarEvent }
}

// ------------------------------------------------------------ drag / resize

async function onMoved(arg: EventDropArg | EventResizeDoneArg, kind: 'drop' | 'resize') {
  const ev = arg.event.extendedProps.ev as CalendarEvent
  const allDay = arg.event.allDay
  let scope: RecurrenceScope = 'this'
  if (needsScopeChoice(ev)) {
    const s = await askScope(kind === 'resize' ? 'resize' : 'move', ev.title)
    if (!s) return arg.revert()
    scope = s
  }
  if (scope === 'series' && allDay !== ev.allDay) {
    arg.revert()
    toast('Can’t switch a whole series between all-day and timed by dragging', { description: 'Open the event and edit the series instead.' })
    return
  }
  const start = arg.event.start!
  const end = arg.event.end ?? (allDay ? addDays(start, 1) : addMinutes(start, settings.settings?.calendarDefaultDuration || 30))
  const times = { start: allDay ? toYmd(start) : toRfc3339(start), end: allDay ? toYmd(end) : toRfc3339(end), allDay }
  // "series": the backend moves every occurrence by this occurrence's change.
  if (!(await cal.move(ev, times, scope, kind))) arg.revert()
}

// ------------------------------------------------------------ rendering hooks

function onMount(arg: EventMountArg) {
  const ev = arg.event.extendedProps.ev as CalendarEvent | undefined
  if (!ev) return
  arg.el.style.setProperty('--ev', arg.event.extendedProps.color as string)
  arg.el.dataset.evId = ev.id
  arg.el.title = arg.event.title
  arg.el.dataset.evStart = String(arg.event.start?.getTime() ?? 0)
  // Keyboard access: Tab to an event, Enter/Space opens its details.
  if (!arg.el.hasAttribute('tabindex')) arg.el.setAttribute('tabindex', '0')
  arg.el.setAttribute('role', 'button')
  arg.el.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      e.stopPropagation()
      ;(arg.el as HTMLElement).click()
    }
  })
}

function eventContent(arg: EventContentArg) {
  // The selection mirror (drag-to-create) has no backing event.
  const ev = arg.event.extendedProps.ev as CalendarEvent | undefined
  if (!ev) return { html: `<span class="gm-ev-time">${escapeHtml(arg.timeText)}</span>` }
  const t = escapeHtml(arg.event.title)
  const loc = ev.location ? `<span class="gm-ev-loc">${escapeHtml(ev.location)}</span>` : ''
  if (arg.view.type.startsWith('list')) return { html: `<span class="gm-ev-title">${t}</span>${loc ? ` <span class="gm-ev-sep">·</span> ${loc}` : ''}` }
  if (arg.event.allDay || arg.view.type === 'dayGridMonth') {
    const time = !arg.event.allDay && arg.timeText ? `<span class="gm-ev-dot"></span><span class="gm-ev-time">${escapeHtml(arg.timeText)}</span> ` : ''
    return { html: `<span class="gm-ev-line">${time}<span class="gm-ev-title">${t}</span></span>` }
  }
  return { html: `<span class="gm-ev-title">${t}</span><span class="gm-ev-time">${escapeHtml(arg.timeText)}</span>${loc}` }
}

function dayHeaderContent(arg: DayHeaderContentArg) {
  if (arg.view.type.startsWith('list')) {
    const label = escapeHtml(arg.date.toLocaleDateString(locale, { weekday: 'long', day: 'numeric', month: 'long' }))
    const rel = arg.isToday ? '' : relativeDay(arg.date)
    const hint = arg.isToday ? ' <span class="gm-list-today">Today</span>' : rel ? ` <span class="gm-list-rel">${escapeHtml(rel)}</span>` : ''
    return { html: `<span class="gm-list-day${arg.isToday ? ' gm-today' : ''}">${label}${hint}</span>` }
  }
  const wd = escapeHtml(arg.date.toLocaleDateString(locale, { weekday: 'short' }))
  if (arg.view.type === 'dayGridMonth') return { html: `<span class="gm-dow">${wd}</span>` }
  const today = arg.isToday ? ' gm-today' : ''
  return { html: `<span class="gm-dow${today}">${wd}</span><span class="gm-dom${today}">${arg.date.getDate()}</span>` }
}

function onDatesSet(arg: DatesSetArg) {
  title.value = arg.view.title
  visibleRange.value = { start: arg.view.currentStart, end: arg.view.currentEnd }
  anchorDate.value = arg.view.currentStart
  void cal.loadRange(arg.start, arg.end)
}

// Settings → General → Time format (uses12h follows it, else the locale).
const hour12 = computed(() => uses12h(locale))
/** FullCalendar time format. hourCycle h23, not hour12: false (which can render "24:30"). */
function clockFormat(h12: boolean) {
  return h12
    ? ({ hour: 'numeric', minute: '2-digit', omitZeroMinute: true, hour12: true } as const)
    : ({ hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } as const)
}
watch(hour12, (h) => {
  api$()?.setOption('slotLabelFormat', clockFormat(h))
  api$()?.setOption('eventTimeFormat', clockFormat(h))
})
// Working hours by default; follow the clock during the day (computed once, so
// option updates don't scroll the grid back).
const nowHour = new Date().getHours()
const scrollTime = `${String(nowHour >= 9 && nowHour <= 19 ? nowHour - 1 : 7).padStart(2, '0')}:30:00`
const compact = computed(() => settings.settings?.density === 'compact')

const options = computed<CalendarOptions>(() => ({
  plugins: [dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin],
  locales: allLocales,
  locale,
  initialView: FC_VIEW[cal.view],
  views: { listAgenda: { type: 'list', duration: { days: 30 }, listDayFormat: { weekday: 'long', day: 'numeric', month: 'long' }, listDaySideFormat: false } },
  headerToolbar: false,
  firstDay: cal.weekStart,
  height: '100%',
  expandRows: true,
  nowIndicator: true,
  selectable: true,
  selectMirror: true,
  unselectAuto: false,
  editable: true,
  eventResizableFromStart: false,
  dayMaxEvents: true,
  // More than three overlapping events: "+N more" instead of unreadable slivers.
  eventMaxStack: 3,
  // Overlapping events side by side instead of stacked on top of each other.
  slotEventOverlap: false,
  eventMinHeight: 18,
  slotDuration: '00:30:00',
  snapDuration: '00:15:00',
  scrollTime,
  scrollTimeReset: false,
  slotLabelFormat: clockFormat(hour12.value),
  eventTimeFormat: clockFormat(hour12.value),
  // Day numbers and day headers open that day.
  navLinks: true,
  navLinkDayClick: (date: Date) => setView('day', date),
  dayHeaderContent,
  eventContent,
  eventDidMount: onMount,
  events: fcEvents.value,
  select: onSelect,
  eventClick: onEventClick,
  eventDrop: (a: EventDropArg) => void onMoved(a, 'drop'),
  eventResize: (a: EventResizeDoneArg) => void onMoved(a, 'resize'),
  datesSet: onDatesSet,
  // Scroll areas reachable by keyboard (agenda list, time grid).
  viewDidMount: () => nextTick(() => document.querySelectorAll('.gm-fc .fc-list .fc-scroller').forEach((el) => el.setAttribute('tabindex', '0'))),
  // FullCalendar labels day cells by their number only; the cell's own content is richer.
  dayCellDidMount: (arg: { el: HTMLElement }) => arg.el.removeAttribute('aria-labelledby'),
  noEventsContent: () => ({ html: '<div class="gm-cal-empty">Nothing scheduled in this period.</div>' }),
  allDayContent: () => ({ html: '' }),
}))

// ------------------------------------------------------------ navigation

function setView(v: CalendarViewKind, date?: Date) {
  cal.view = v
  api$()?.changeView(FC_VIEW[v], date)
}
function go(dir: 1 | -1 | 0) {
  const a = api$()
  if (!a) return
  if (dir === 0) a.today()
  else if (dir === 1) a.next()
  else a.prev()
}
function pickDate(d: Date) {
  api$()?.gotoDate(d)
  // Narrow window: the sidebar covers the grid; get it out of the way once a day is picked.
  if (ui.narrow) sidebarOpen.value = false
}
function createFromSidebar() {
  if (ui.narrow) sidebarOpen.value = false
  openNew()
}

function openNew() {
  quick.value = null
  details.value = null
  const now = new Date()
  const r = visibleRange.value
  let start: Date
  if (!r || (now >= r.start && now < r.end)) {
    start = new Date(now)
    start.setMinutes(Math.ceil((start.getMinutes() + 1) / 30) * 30, 0, 0)
  } else {
    start = startOfDay(r.start)
    start.setHours(9, 0, 0, 0)
  }
  const end = addMinutes(start, settings.settings?.calendarDefaultDuration || 30)
  openEditor({ draft: { start: toRfc3339(start), end: toRfc3339(end), allDay: false } })
}

// ------------------------------------------------------------ keyboard

/** Rendered event chips in chronological order (one per event; multi-day segments collapse). */
function eventEls(): HTMLElement[] {
  const seen = new Set<string>()
  return [...document.querySelectorAll<HTMLElement>('.gm-fc .fc-event[data-ev-id]')]
    .filter((el) => el.offsetParent !== null && !el.classList.contains('fc-event-mirror'))
    .map((el, i) => ({ el, i, t: Number(el.dataset.evStart) }))
    .sort((a, b) => a.t - b.t || a.i - b.i)
    .filter(({ el }) => !seen.has(el.dataset.evId!) && !!seen.add(el.dataset.evId!))
    .map(({ el }) => el)
}

/** ↑/↓ (or j/k) walk the events; from nowhere, start at the next upcoming one. */
function focusEvent(dir: 1 | -1) {
  const els = eventEls()
  if (!els.length) return
  const cur = (document.activeElement as HTMLElement | null)?.closest?.<HTMLElement>('[data-ev-id]')
  let i = cur ? els.findIndex((el) => el.dataset.evId === cur.dataset.evId) : -1
  if (i === -1) {
    const now = Date.now()
    i = els.findIndex((el) => Number(el.dataset.evStart) >= now)
    if (i === -1) i = dir > 0 ? 0 : els.length - 1
  } else i = Math.min(els.length - 1, Math.max(0, i + dir))
  els[i].focus()
  els[i].scrollIntoView({ block: 'nearest' })
}

function onKey(e: KeyboardEvent) {
  if (e.defaultPrevented || e.altKey) return
  if (isEditableTarget(e.target) || ui.anyDialogOpen() || editorRequest.value) return
  if ((e.target as Element | null)?.closest?.('[role="dialog"], [role="menu"], [role="listbox"]')) return
  const k = e.key
  if (e.metaKey || e.ctrlKey) {
    if (k.toLowerCase() === 'b') {
      sidebarOpen.value = !sidebarOpen.value
      e.preventDefault()
    }
    return
  }
  // The event chip that has keyboard focus (↑/↓ or Tab), if any.
  const chip = (document.activeElement as HTMLElement | null)?.closest?.<HTMLElement>('.gm-fc [data-ev-id]')
  const view = VIEWS.find((v) => v.key === k)
  if (k === 'Escape' && ui.narrow && sidebarOpen.value) sidebarOpen.value = false
  else if (k === 'e' && chip) {
    // Straight to the editor for the focused event (Enter shows its details card).
    const ev = cal.events.find((x) => x.id === chip.dataset.evId)
    if (!ev) return
    details.value = null
    openEditor({ event: ev })
  } else if (view) setView(view.id)
  else if (k === 't') go(0)
  else if (k === 'ArrowLeft') go(-1)
  else if (k === 'ArrowRight') go(1)
  else if (k === 'ArrowDown' || k === 'j') focusEvent(1)
  else if (k === 'ArrowUp' || k === 'k') focusEvent(-1)
  else if (k === 'n' || k === 'c') openNew()
  else return
  e.preventDefault()
}

function closeDetails() {
  const id = details.value?.event.id
  details.value = null
  // The chip that opened the card may have been re-rendered meanwhile; refocus it by id.
  setTimeout(() => {
    if (!id || details.value || quick.value || (document.activeElement && document.activeElement !== document.body)) return
    document.querySelector<HTMLElement>(`.gm-fc [data-ev-id="${CSS.escape(id)}"]`)?.focus({ preventScroll: true })
  }, 0)
}

watch(() => ui.newEventPending, (v) => {
  if (!v) return
  ui.newEventPending = false
  nextTick(openNew)
}, { immediate: true })
watch(() => cal.focusDate, (d) => {
  if (d) {
    nextTick(() => api$()?.gotoDate(d))
    cal.focusDate = null
  }
})
// Reminder notification click: jump to the event and show its details.
watch(() => cal.openEventId, async (id) => {
  if (!id) return
  cal.openEventId = null
  try {
    const ev = await api.getEvent(id)
    const date = new Date(ev.allDay ? `${ev.start}T00:00` : ev.start)
    if (api$()) api$()!.gotoDate(date)
    else cal.focusDate = date
    details.value = { anchor: pointRect(null), event: ev }
  } catch (e) {
    toast.error('Couldn’t open the event', { description: errorMessage(e) })
  }
}, { immediate: true })
watch(() => cal.weekStart, (fd) => api$()?.setOption('firstDay', fd))
watch(compact, async () => {
  await nextTick()
  api$()?.updateSize()
}, { immediate: true }) // also when set before the calendar opened (invite "View in calendar")
watch(sidebarOpen, async () => {
  await nextTick()
  setTimeout(() => api$()?.updateSize(), 220)
})

onMounted(async () => {
  window.addEventListener('keydown', onKey)
  window.addEventListener('pointerdown', onPointerDown, true)
  window.addEventListener('pointerup', onPointerUp, true)
  window.addEventListener('pointercancel', onPointerUp, true)
  if (!cal.loaded) await cal.loadCalendars()
  if (cal.focusDate) {
    api$()?.gotoDate(cal.focusDate)
    cal.focusDate = null
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('pointerdown', onPointerDown, true)
  window.removeEventListener('pointerup', onPointerUp, true)
  window.removeEventListener('pointercancel', onPointerUp, true)
})

const noCalendars = computed(() => cal.loaded && !cal.calendars.length)
const consentMissing = computed(() => accounts.accounts.filter((a) => a.calendarConsent === false))
</script>

<template>
  <div :class="['gm-calendar flex min-w-0 flex-1', compact && 'gm-compact']">
    <div v-if="sidebarOpen" :class="ui.narrow ? 'absolute inset-y-0 left-14 z-30 w-64 shadow-2xl' : 'w-64 shrink-0'">
      <CalendarSidebar :selected="anchorDate" :range-start="visibleRange?.start" :range-end="visibleRange?.end" @pick="pickDate" @create="createFromSidebar" />
    </div>
    <div v-if="ui.narrow && sidebarOpen" class="absolute inset-y-0 right-0 left-14 z-20 bg-black/10" @click="sidebarOpen = false" />

    <section class="bg-background flex min-w-0 flex-1 flex-col" aria-label="Calendar">
      <header data-drag class="border-border flex h-[52px] shrink-0 items-center gap-1.5 border-b px-3">
        <Tip label="Toggle sidebar" shortcut="mod+b">
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Toggle calendar sidebar" @click="sidebarOpen = !sidebarOpen"><PanelLeftIcon /></Button>
        </Tip>
        <Tip label="Today" shortcut="t"><Button variant="outline" size="sm" class="h-8" @click="go(0)">Today</Button></Tip>
        <div class="flex">
          <Tip label="Previous" shortcut="←"><Button variant="ghost" size="icon-sm" aria-label="Previous" @click="go(-1)"><ChevronLeftIcon /></Button></Tip>
          <Tip label="Next" shortcut="→"><Button variant="ghost" size="icon-sm" aria-label="Next" @click="go(1)"><ChevronRightIcon /></Button></Tip>
        </div>
        <h1 class="min-w-0 truncate pl-1 text-[15px] font-semibold tracking-tight" aria-live="polite">{{ title }}</h1>
        <LoaderIcon v-if="cal.loading" class="text-muted-foreground size-3.5 shrink-0 animate-spin" aria-label="Loading events" />
        <div class="flex-1" />
        <div class="bg-muted inline-flex rounded-lg p-0.5" role="group" aria-label="View">
          <Tip v-for="v in VIEWS" :key="v.id" :label="`${v.label} view`" :shortcut="v.key">
            <button
              type="button"
              :aria-pressed="cal.view === v.id"
              :aria-label="`${v.label} view`"
              :class="['rounded-md px-2.5 py-1 text-xs font-medium transition-colors max-[720px]:px-2', cal.view === v.id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground']"
              @click="setView(v.id)"
            ><span class="max-[720px]:hidden">{{ v.label }}</span><span class="hidden max-[720px]:inline" aria-hidden="true">{{ v.label[0] }}</span></button>
          </Tip>
        </div>
      </header>

      <div v-if="cal.error && !cal.events.length" role="alert" class="bg-destructive/10 text-destructive mx-3 mt-3 rounded-md px-3 py-2 text-xs">{{ cal.error }}</div>
      <div v-if="noCalendars" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
        <div class="bg-muted/70 text-muted-foreground flex size-14 items-center justify-center rounded-2xl"><CalendarOffIcon class="size-6" /></div>
        <div>
          <p class="text-sm font-medium">No calendars yet</p>
          <p class="text-muted-foreground mt-1 max-w-sm text-xs">
            <template v-if="consentMissing.length">Connect a mailbox’s calendar in the sidebar to see its events here.</template>
            <template v-else>Your mailboxes don’t have calendars, or they haven’t synced yet.</template>
          </p>
        </div>
      </div>
      <div v-show="!noCalendars" class="gm-fc min-h-0 flex-1">
        <FullCalendar ref="fc" :options="options" />
      </div>
    </section>

    <QuickCreate v-if="quick" :key="quick.start.getTime()" :anchor="quick.anchor" :start="quick.start" :end="quick.end" :all-day="quick.allDay" @close="closeQuick" />
    <EventDetails v-if="details" :key="details.event.id" :anchor="details.anchor" :event="details.event" @close="closeDetails" />
    <EventEditor v-if="editorRequest" :key="editorRequest.event?.id ?? 'new'" />
    <!-- While the editor (a modal) is open it hosts the prompts itself, nested, so they stay clickable. -->
    <ScopePrompt v-if="!editorRequest" />
  </div>
</template>
