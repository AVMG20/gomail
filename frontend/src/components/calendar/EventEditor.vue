<script setup lang="ts">
// Full event editor: title, all-day, start/end, time zone (collapsed), repeat,
// location, meeting link, guests, reminders, description and calendar.
import { computed, ref, watch } from 'vue'
import { BellIcon, ChevronDownIcon, ClockIcon, GlobeIcon, LinkIcon, MapPinIcon, RepeatIcon, TextIcon, UsersIcon, XIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import RecipientField from '@/components/compose/RecipientField.vue'
import {
  addDays, addMinutes, allTimeZones, buildRRule, eventDate, formatReminder, fromYmd, localTimeZone, needsScopeChoice, parseRRule,
  REMINDER_CHOICES, toRfc3339, toYmd, utcToZoned, zonedToUtc, type RepeatFreq,
} from '@/lib/calendar'
import type { Address, EventInput } from '@/lib/types'
import { useCalendarStore } from '@/stores/calendar'
import { useSettingsStore } from '@/stores/settings'
import CalendarPicker from './CalendarPicker.vue'
import ScopePrompt from './ScopePrompt.vue'
import { closeEditor, editorRequest } from './editorState'
import { askConfirm, askScope } from './scopePrompt'
import { api, hasMethod } from '@/lib/api'

const cal = useCalendarStore()
const settings = useSettingsStore()
const open = ref(true)

const req = editorRequest.value!
const ev = req.event
const isNew = !ev
const localTz = localTimeZone()

// ---- initial values
const base: Partial<EventInput> = ev ?? req.draft ?? {}
const duration = settings.settings?.calendarDefaultDuration || 30
const startDate0 = base.start ? eventDate(base.start, !!base.allDay) : roundUp(new Date())
const endDate0 = base.end ? eventDate(base.end, !!base.allDay) : addMinutes(startDate0, duration)

const title = ref(base.title ?? '')
const allDay = ref(!!base.allDay)
const timeZone = ref(base.timeZone || localTz)
const showTz = ref(!!base.timeZone && base.timeZone !== localTz)
const s0 = utcToZoned(startDate0, allDay.value ? '' : timeZone.value)
const e0 = utcToZoned(allDay.value ? addDays(endDate0, -1) : endDate0, allDay.value ? '' : timeZone.value)
const startDay = ref(s0.ymd)
const startTime = ref(s0.hm)
const endDay = ref(e0.ymd)
const endTime = ref(e0.hm)
const rule0 = parseRRule(base.recurrence ?? '')
const freq = ref<RepeatFreq>(rule0.freq)
const until = ref(rule0.until)
const location = ref(base.location ?? '')
const meetingUrl = ref(base.meetingUrl ?? '')
const guests = ref<Address[]>((base.attendees ?? []).map((a) => ({ name: a.name, email: a.email })))
const defaultReminder = settings.settings?.calendarDefaultReminder ?? 10
const reminders = ref<number[]>(base.reminders ? [...base.reminders] : defaultReminder >= 0 ? [defaultReminder] : [])
const description = ref(base.description ?? '')
const calendarId = ref(base.calendarId && cal.writableCalendars.some((c) => c.id === base.calendarId) ? base.calendarId : cal.defaultCalendar)
const saving = ref(false)
const error = ref('')

const readOnly = computed(() => !!ev && !cal.canEditEvent(ev))
const timeZones = allTimeZones()
const reminderOptions = computed(() => REMINDER_CHOICES.filter((m) => !reminders.value.includes(m)))

function roundUp(d: Date): Date {
  const x = new Date(d)
  x.setMinutes(Math.ceil((x.getMinutes() + 1) / 30) * 30, 0, 0)
  return x
}

// Keep the duration when the start moves; keep the end after the start.
let lastStart = `${startDay.value}T${startTime.value}`
watch([startDay, startTime], () => {
  const prev = fromYmd(lastStart.slice(0, 10), lastStart.slice(11))
  const next = fromYmd(startDay.value, startTime.value)
  const delta = next.getTime() - prev.getTime()
  const end = new Date(fromYmd(endDay.value, endTime.value).getTime() + delta)
  endDay.value = toYmd(end)
  endTime.value = `${String(end.getHours()).padStart(2, '0')}:${String(end.getMinutes()).padStart(2, '0')}`
  lastStart = `${startDay.value}T${startTime.value}`
})

function buildInput(): EventInput | null {
  let start: string
  let end: string
  if (allDay.value) {
    if (endDay.value < startDay.value) return fail('The event ends before it starts.')
    start = startDay.value
    end = toYmd(addDays(fromYmd(endDay.value), 1))
  } else {
    const tz = showTz.value ? timeZone.value : localTz
    const s = zonedToUtc(startDay.value, startTime.value, tz === localTz ? '' : tz)
    const e = zonedToUtc(endDay.value, endTime.value, tz === localTz ? '' : tz)
    if (e <= s) return fail('The event ends before it starts.')
    start = toRfc3339(s)
    end = toRfc3339(e)
  }
  if (freq.value !== 'none' && until.value && until.value < startDay.value) return fail('“Repeat until” is before the first event.')
  if (!calendarId.value) return fail('Choose a calendar.')
  const keep = new Map((ev?.attendees ?? []).map((a) => [a.email.toLowerCase(), a.response]))
  return {
    calendarId: calendarId.value,
    title: title.value.trim(),
    start, end, allDay: allDay.value,
    timeZone: allDay.value ? '' : showTz.value ? timeZone.value : localTz,
    location: location.value.trim(),
    description: description.value,
    attendees: guests.value.map((g) => ({ name: g.name, email: g.email, response: keep.get(g.email.toLowerCase()) ?? 'needsAction' })),
    // Unchanged links of existing events stay where the provider keeps them (conference data).
    meetingUrl: !isNew && meetingUrl.value.trim() === (ev?.meetingUrl ?? '') ? '' : meetingUrl.value.trim(),
    recurrence: buildRRule({ freq: freq.value, until: until.value, byDay: rule0.freq === freq.value ? rule0.byDay : undefined }),
    reminders: [...reminders.value].sort((a, b) => a - b),
  }
}

function fail(msg: string): null {
  error.value = msg
  return null
}

async function save() {
  error.value = ''
  const input = buildInput()
  if (!input) return
  if (!input.title) input.title = 'Untitled event'
  saving.value = true
  try {
    if (isNew) {
      if (await cal.create(input)) close()
      return
    }
    // Moving to a calendar in another mailbox recreates the event there.
    if (input.calendarId !== ev!.calendarId && hasMethod('MoveEventCheck')) {
      const check = await api.moveEventCheck(ev!.id, input.calendarId).catch(() => null)
      if (check && !check.allowed) {
        error.value = check.reason || 'This event can’t be moved to that calendar.'
        return
      }
      if (check?.crossAccount && check.hasAttendees) {
        const ok = await askConfirm(
          'Move to another account?',
          'The event is recreated in the other mailbox’s calendar. Attendees will receive a new invitation (and a cancellation for the old one).',
          'Move event',
        )
        if (!ok) return
      }
    }
    let scope: 'this' | 'series' = 'this'
    if (needsScopeChoice(ev!)) {
      const s = await askScope('edit', ev!.title)
      if (!s) return
      scope = s
    }
    if (await cal.update(ev!, input, scope)) close()
  } finally {
    saving.value = false
  }
}

function close() {
  open.value = false
  setTimeout(closeEditor, 150)
}

function addReminder(v: unknown) {
  const n = Number(v)
  if (!Number.isNaN(n) && !reminders.value.includes(n)) reminders.value = [...reminders.value, n]
}
</script>

<template>
  <Dialog :open="open" @update:open="(v: boolean) => !v && close()">
    <DialogContent class="flex max-h-[min(780px,calc(100vh-2rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[620px]">
      <div class="border-border flex items-center gap-2 border-b px-5 py-3">
        <DialogTitle class="text-sm font-semibold">{{ isNew ? 'New event' : readOnly ? 'Event' : 'Edit event' }}</DialogTitle>
        <DialogDescription class="sr-only">Event details</DialogDescription>
      </div>

      <form class="thin-scroll min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4" @submit.prevent="save">
        <input
          v-model="title"
          :disabled="readOnly"
          autofocus
          placeholder="Add a title"
          aria-label="Title"
          class="placeholder:text-muted-foreground/70 w-full bg-transparent text-xl font-semibold tracking-tight outline-none"
        />

        <!-- When -->
        <div class="flex gap-3">
          <ClockIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <div class="min-w-0 flex-1 space-y-2">
            <div class="grid grid-cols-[3rem_auto_auto_1fr] items-center gap-x-2 gap-y-1.5">
              <span class="text-muted-foreground text-xs">Starts</span>
              <Input v-model="startDay" type="date" class="h-8 w-auto text-sm" aria-label="Start date" :disabled="readOnly" />
              <Input v-if="!allDay" v-model="startTime" type="time" step="900" class="h-8 w-auto text-sm" aria-label="Start time" :disabled="readOnly" />
              <span v-else />
              <span />
              <span class="text-muted-foreground text-xs">Ends</span>
              <Input v-model="endDay" type="date" class="h-8 w-auto text-sm" aria-label="End date" :disabled="readOnly" />
              <Input v-if="!allDay" v-model="endTime" type="time" step="900" class="h-8 w-auto text-sm" aria-label="End time" :disabled="readOnly" />
              <span v-else />
              <span />
            </div>
            <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
              <label class="flex items-center gap-2"><Switch v-model="allDay" :disabled="readOnly" aria-label="All day" /> All day</label>
              <button v-if="!allDay && !showTz" type="button" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs" @click="showTz = true">
                <GlobeIcon class="size-3.5" /> Time zone <ChevronDownIcon class="size-3" />
              </button>
            </div>
            <div v-if="!allDay && showTz" class="flex items-center gap-2">
              <GlobeIcon class="text-muted-foreground size-3.5" />
              <Select v-model="timeZone" :disabled="readOnly">
                <SelectTrigger class="h-8 w-72 text-xs" aria-label="Time zone"><SelectValue /></SelectTrigger>
                <SelectContent class="max-h-72">
                  <SelectItem v-for="z in timeZones" :key="z" :value="z">{{ z.replace(/_/g, ' ') }}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>

        <!-- Repeat -->
        <div class="flex items-center gap-3">
          <RepeatIcon class="text-muted-foreground size-4 shrink-0" />
          <Select v-model="freq" :disabled="readOnly">
            <SelectTrigger class="h-8 w-44 text-sm" aria-label="Repeat"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="none">Does not repeat</SelectItem>
              <SelectItem value="daily">Daily</SelectItem>
              <SelectItem value="weekly">Weekly</SelectItem>
              <SelectItem value="monthly">Monthly</SelectItem>
              <SelectItem value="yearly">Yearly</SelectItem>
            </SelectContent>
          </Select>
          <template v-if="freq !== 'none'">
            <span class="text-muted-foreground text-sm">until</span>
            <Input v-model="until" type="date" class="h-8 w-auto text-sm" aria-label="Repeat until (optional)" :disabled="readOnly" />
          </template>
        </div>

        <!-- Where -->
        <div class="flex items-center gap-3">
          <MapPinIcon class="text-muted-foreground size-4 shrink-0" />
          <Input v-model="location" placeholder="Add a location" aria-label="Location" class="h-8 text-sm" :disabled="readOnly" />
        </div>
        <div class="flex items-center gap-3">
          <LinkIcon class="text-muted-foreground size-4 shrink-0" />
          <Input v-model="meetingUrl" type="url" placeholder="Meeting link (Teams, Meet, Zoom…)" aria-label="Meeting link" class="h-8 text-sm" :disabled="readOnly" />
        </div>

        <!-- Guests -->
        <div class="flex gap-3">
          <UsersIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <div class="border-input min-w-0 flex-1 rounded-md border px-2">
            <RecipientField v-model="guests" label="Guests" />
          </div>
        </div>

        <!-- Reminders -->
        <div class="flex items-start gap-3">
          <BellIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <div class="flex flex-wrap items-center gap-1.5">
            <span v-for="m in reminders" :key="m" class="bg-muted inline-flex h-7 items-center gap-1 rounded-full pr-1 pl-2.5 text-xs">
              {{ formatReminder(m) }}
              <button type="button" class="hover:bg-background rounded-full p-0.5" :aria-label="`Remove reminder ${formatReminder(m)}`" :disabled="readOnly" @click="reminders = reminders.filter((x) => x !== m)"><XIcon class="size-3" /></button>
            </span>
            <Select v-if="!readOnly" :model-value="''" @update:model-value="addReminder">
              <SelectTrigger class="h-7 w-auto gap-1 border-dashed text-xs" aria-label="Add reminder"><span>Add reminder</span></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="m in reminderOptions" :key="m" :value="String(m)">{{ formatReminder(m) }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <!-- Notes -->
        <div class="flex gap-3">
          <TextIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <Textarea v-model="description" rows="3" placeholder="Add a description" aria-label="Description" class="min-h-20 text-sm" :disabled="readOnly" />
        </div>

        <!-- Calendar -->
        <div class="flex items-center gap-3">
          <span class="size-4 shrink-0" />
          <div class="min-w-0 flex-1">
            <CalendarPicker v-if="!readOnly" v-model="calendarId" />
            <p v-else class="text-muted-foreground text-xs">This calendar is read-only.</p>
          </div>
        </div>

        <p v-if="error" role="alert" class="text-destructive text-sm">{{ error }}</p>
        <button type="submit" class="hidden" />
      </form>

      <div class="border-border flex items-center justify-end gap-2 border-t px-5 py-3">
        <Button variant="ghost" size="sm" @click="close">Cancel</Button>
        <Button v-if="!readOnly" size="sm" :disabled="saving" @click="save">{{ isNew ? 'Create event' : 'Save' }}</Button>
      </div>
      <ScopePrompt />
    </DialogContent>
  </Dialog>
</template>
