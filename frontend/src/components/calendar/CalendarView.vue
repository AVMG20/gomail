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
import type { CalendarApi, CalendarOptions, DateSelectArg, DatesSetArg, EventClickArg, EventContentArg, EventDropArg, EventMountArg, DayHeaderContentArg, Duration } from '@fullcalendar/core'
import type { EventResizeDoneArg } from '@fullcalendar/interaction'
import { ChevronLeftIcon, ChevronRightIcon, CalendarOffIcon, LoaderIcon, PanelLeftIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import Tip from '@/components/common/Tip.vue'
import type { AnchorRect } from '@/components/common/FloatingCard.vue'
import { api, errorMessage } from '@/lib/api'
import {
  addDays, addMinutes, eventToInput, localeUses12h, needsScopeChoice, seriesMoveInput, startOfDay, toRfc3339, toYmd, type CalendarViewKind,
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

function onSelect(arg: DateSelectArg) {
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
  details.value = { anchor: { left: r.left, top: r.top, width: r.width, height: r.height }, event: arg.event.extendedProps.ev as CalendarEvent }
}

// ------------------------------------------------------------ drag / resize

function deltaMs(d: Duration): number {
  return (d.days ?? 0) * 86_400_000 + (d.milliseconds ?? 0) + (d.months ?? 0) * 30 * 86_400_000
}

async function onMoved(arg: EventDropArg | EventResizeDoneArg, kind: 'drop' | 'resize') {
  const ev = arg.event.extendedProps.ev as CalendarEvent
  const startDelta = kind === 'drop' ? deltaMs((arg as EventDropArg).delta) : deltaMs((arg as EventResizeDoneArg).startDelta)
  const endDelta = kind === 'drop' ? deltaMs((arg as EventDropArg).delta) : deltaMs((arg as EventResizeDoneArg).endDelta)
  const allDay = arg.event.allDay
  let scope: RecurrenceScope = 'this'
  if (needsScopeChoice(ev)) {
    const s = await askScope('move', ev.title)
    if (!s) return arg.revert()
    scope = s
  }
  const start = arg.event.start!
  const end = arg.event.end ?? (allDay ? addDays(start, 1) : addMinutes(start, settings.settings?.calendarDefaultDuration || 30))
  const times = { start: allDay ? toYmd(start) : toRfc3339(start), end: allDay ? toYmd(end) : toRfc3339(end), allDay }
  if (scope === 'series') {
    if (allDay !== ev.allDay) {
      arg.revert()
      toast('Can’t switch a whole series between all-day and timed by dragging', { description: 'Open the event and edit the series instead.' })
      return
    }
    try {
      const master = await api.getEvent(ev.seriesId || ev.id)
      await cal.move(ev, times, 'series', { next: seriesMoveInput(master, startDelta, endDelta), before: eventToInput(master) })
    } catch (e) {
      arg.revert()
      toast.error('Couldn’t move the series', { description: errorMessage(e) })
    }
    return
  }
  await cal.move(ev, times, 'this')
}

// ------------------------------------------------------------ rendering hooks

function onMount(arg: EventMountArg) {
  const ev = arg.event.extendedProps.ev as CalendarEvent | undefined
  if (!ev) return
  arg.el.style.setProperty('--ev', arg.event.extendedProps.color as string)
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
    return { html: `<span class="gm-list-day${arg.isToday ? ' gm-today' : ''}">${label}${arg.isToday ? ' <span class="gm-list-today">Today</span>' : ''}</span>` }
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

const hour12 = localeUses12h(locale)
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
  slotDuration: '00:30:00',
  snapDuration: '00:15:00',
  // Working hours by default; follow the clock during the day.
  scrollTime: `${String(new Date().getHours() >= 9 && new Date().getHours() <= 19 ? new Date().getHours() - 1 : 7).padStart(2, '0')}:30:00`,
  scrollTimeReset: false,
  slotLabelFormat: { hour: 'numeric', minute: '2-digit', omitZeroMinute: hour12, hour12 },
  eventTimeFormat: { hour: 'numeric', minute: '2-digit', omitZeroMinute: hour12, hour12 },
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

function onKey(e: KeyboardEvent) {
  if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return
  if (isEditableTarget(e.target) || ui.anyDialogOpen() || editorRequest.value) return
  if ((e.target as Element | null)?.closest?.('[role="dialog"], [role="menu"], [role="listbox"]')) return
  const k = e.key
  const view = VIEWS.find((v) => v.key === k)
  if (view) setView(view.id)
  else if (k === 't') go(0)
  else if (k === 'ArrowLeft') go(-1)
  else if (k === 'ArrowRight') go(1)
  else if (k === 'n') openNew()
  else return
  e.preventDefault()
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
})
watch(sidebarOpen, async () => {
  await nextTick()
  setTimeout(() => api$()?.updateSize(), 220)
})

onMounted(async () => {
  window.addEventListener('keydown', onKey)
  if (!cal.loaded) await cal.loadCalendars()
  if (cal.focusDate) {
    api$()?.gotoDate(cal.focusDate)
    cal.focusDate = null
  }
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

const noCalendars = computed(() => cal.loaded && !cal.calendars.length)
const consentMissing = computed(() => accounts.accounts.filter((a) => a.calendarConsent === false))
</script>

<template>
  <div :class="['gm-calendar flex min-w-0 flex-1', compact && 'gm-compact']">
    <div v-if="sidebarOpen" :class="ui.narrow ? 'absolute inset-y-0 left-14 z-30 w-64 shadow-2xl' : 'w-64 shrink-0'">
      <CalendarSidebar :selected="anchorDate" :range-start="visibleRange?.start" :range-end="visibleRange?.end" @pick="pickDate" @create="openNew" />
    </div>
    <div v-if="ui.narrow && sidebarOpen" class="absolute inset-y-0 right-0 left-14 z-20 bg-black/10" @click="sidebarOpen = false" />

    <section class="bg-background flex min-w-0 flex-1 flex-col" aria-label="Calendar">
      <header data-drag class="border-border flex h-[calc(52px+var(--titlebar))] shrink-0 items-center gap-1.5 border-b px-3 pt-[var(--titlebar)]">
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
              :class="['rounded-md px-2.5 py-1 text-xs font-medium transition-colors', cal.view === v.id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground']"
              @click="setView(v.id)"
            >{{ v.label }}</button>
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
    <EventDetails v-if="details" :key="details.event.id" :anchor="details.anchor" :event="details.event" @close="details = null" />
    <EventEditor v-if="editorRequest" :key="editorRequest.event?.id ?? 'new'" />
    <!-- While the editor (a modal) is open it hosts the prompts itself, nested, so they stay clickable. -->
    <ScopePrompt v-if="!editorRequest" />
  </div>
</template>
