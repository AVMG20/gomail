<script setup lang="ts">
// Full event editor: title, all-day, start/end, time zone (collapsed), repeat,
// location, meeting link, guests, reminders, description and calendar.
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { BellIcon, ChevronDownIcon, ClockIcon, GlobeIcon, LinkIcon, LoaderIcon, MailIcon, MapPinIcon, RepeatIcon, SparklesIcon, TextIcon, UsersIcon, VideoIcon, XIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import RecipientField from '@/components/compose/RecipientField.vue'
import {
  addDays, addMinutes, allTimeZones, describeRepeat, eventDate, formatReminder, fromYmd, isSimpleRRule, isValidTimeZone, localTimeZone, needsScopeChoice,
  parseRRule, recurrenceForSave, REMINDER_CHOICES, toHm, toRfc3339, toYmd, utcToZoned, zonedToUtc, type RepeatFreq,
} from '@/lib/calendar'
import type { Address, EventInput } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import { useSettingsStore } from '@/stores/settings'
import CalendarPicker from './CalendarPicker.vue'
import ScopePrompt from './ScopePrompt.vue'
import TimeField from './TimeField.vue'
import { closeEditor, editorRequest } from './editorState'
import { askConfirm, askScope } from './scopePrompt'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { shortcutLabel } from '@/lib/platform'

const cal = useCalendarStore()
const accounts = useAccountsStore()
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
// An unknown zone name would make every conversion throw: fall back to ours.
const tz0 = base.timeZone && isValidTimeZone(base.timeZone) ? base.timeZone : localTz
const timeZone = ref(tz0)
const showTz = ref(tz0 !== localTz)
const s0 = utcToZoned(startDate0, allDay.value ? '' : timeZone.value)
const e0 = utcToZoned(allDay.value ? addDays(endDate0, -1) : endDate0, allDay.value ? '' : timeZone.value)
const startDay = ref(s0.ymd)
const startTime = ref(s0.hm)
const endDay = ref(e0.ymd)
const endTime = ref(e0.hm)
const recurrence0 = base.recurrence ?? ''
const rule0 = parseRRule(recurrence0, base.timeZone)
/** A rule the menu can't express ("every 2 weeks", "every weekday") is kept as is unless replaced. */
const customRule = !!ev?.isRecurring && (!recurrence0 || !isSimpleRRule(recurrence0))
const customLabel = ev?.recurrenceText || (recurrence0 ? describeRepeat(rule0) : 'Repeats (custom)')
const freq0: RepeatFreq | 'custom' = customRule ? 'custom' : rule0.freq
const freq = ref<RepeatFreq | 'custom'>(freq0)
const until = ref(customRule ? '' : rule0.until)
const ruleChanged = () => freq.value !== freq0 || until.value !== (customRule ? '' : rule0.until)
const location = ref(base.location ?? '')
const meetingUrl = ref(base.meetingUrl ?? '')
const guests = ref<Address[]>((base.attendees ?? []).map((a) => ({ name: a.name, email: a.email })))
// Saving emails guests (Google and Outlook both send the invitations): people
// not on the saved event yet get a new invitation, so that's said out loud.
const invited0 = new Set((ev?.attendees ?? []).map((a) => a.email.toLowerCase()))
const newGuests = computed(() => guests.value.filter((g) => !invited0.has(g.email.toLowerCase())))
const guestHint = computed(() => {
  const n = newGuests.value.length
  if (n) return `${n === 1 ? newGuests.value[0].name || newGuests.value[0].email : `${n} ${isNew ? '' : 'new '}guests`} will get an email invitation when you ${isNew ? 'create the event' : 'save'}.`
  if (guests.value.length) return 'Guests are emailed about changes you save.'
  return ''
})
const saveLabel = computed(() => (newGuests.value.length ? (isNew ? 'Create & send invites' : 'Save & send invites') : isNew ? 'Create event' : 'Save'))
const defaultReminder = settings.settings?.calendarDefaultReminder ?? 10
const reminders = ref<number[]>(base.reminders ? [...base.reminders] : defaultReminder >= 0 ? [defaultReminder] : [])
const description = ref(base.description ?? '')
const calendarId = ref(base.calendarId && cal.writableCalendars.some((c) => c.id === base.calendarId) ? base.calendarId : cal.defaultCalendar)
const addVideo = ref(false)
const videoProvider = computed(() => cal.calendarMap.get(calendarId.value)?.videoCallProvider ?? '')
const videoLabel = computed(() => ({ teams: 'Teams meeting', meet: 'Google Meet', skype: 'Skype call' })[videoProvider.value] ?? 'video call')
/** The event already has a Meet/Teams/Skype link from the provider. */
const providerLink = computed(() => /^https:\/\/(meet\.google\.com|teams\.microsoft\.com|teams\.live\.com|join\.skype\.com)\//i.test(meetingUrl.value.trim()))
const saving = ref(false)
const error = ref('')

const readOnly = computed(() => !!ev && !cal.canEditEvent(ev))
const timeZones = [...new Set([tz0, localTz, ...allTimeZones()].filter(Boolean))]
const reminderAction = computed(() => (maxReminders.value === 1 && reminders.value.length ? 'Change reminder' : 'Add reminder'))
const reminderOptions = computed(() => REMINDER_CHOICES.filter((m) => !reminders.value.includes(m)))
/** Reminders the calendar keeps per event: Outlook one, Google five. */
const maxReminders = computed(() => accounts.accounts.find((a) => a.id === cal.calendarMap.get(calendarId.value)?.accountId)?.provider === 'outlook' ? 1 : 5)
// Moved to a calendar that keeps fewer: keep the earliest ones, like Outlook does.
watch(maxReminders, (max) => {
  if (reminders.value.length > max) reminders.value = [...reminders.value].sort((a, b) => b - a).slice(0, max)
}, { immediate: true })

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
  endTime.value = toHm(end)
  lastStart = `${startDay.value}T${startTime.value}`
}, { flush: 'sync' }) // sync: Enter in a time field submits right after the start changed

// All-day → timed: the day's 00:00 – 00:00 would be an empty (invalid) range; use 09:00 and the default length.
watch(allDay, (on) => {
  if (on || startTime.value !== '00:00' || endTime.value !== '00:00') return
  startTime.value = '09:00' // moves the end along (watcher above)
  endTime.value = toHm(addMinutes(fromYmd(endDay.value, '09:00'), duration))
})

// ---- describe it: natural language → fields, with the chat model from Settings → AI
const aiFill = computed(() => isNew && settings.aiReady && hasMethod('ParseEventText'))
const prompt = ref('')
const filling = ref(false)
const promptEl = ref<HTMLInputElement | null>(null)
const titleEl = ref<HTMLInputElement | null>(null)
/** Open on the describe field (or the title), not on the header's close button. */
function focusFirst(e: Event) {
  if (readOnly.value) return
  e.preventDefault()
  void nextTick(() => (promptEl.value ?? titleEl.value)?.focus())
}
async function fillFromText() {
  const text = prompt.value.trim()
  if (!text || filling.value) return
  filling.value = true
  try {
    const d = await api.parseEventText(text, localTz)
    if (d.title) title.value = d.title
    if (d.date) {
      allDay.value = d.allDay || !d.startTime
      startDay.value = d.date
      if (d.startTime) startTime.value = d.startTime
      await nextTick() // let the start watcher move the end first, then set the end we were given
      if (d.endDate || d.endTime) {
        endDay.value = d.endDate || d.date
        if (d.endTime) endTime.value = d.endTime
      } else if (allDay.value) endDay.value = d.date
    }
    if (d.location) location.value = d.location
    if (d.description) description.value = description.value ? `${description.value}\n${d.description}` : d.description
    if (d.repeat && d.repeat !== 'none') freq.value = d.repeat
    if (d.videoCall && videoProvider.value) addVideo.value = true
    // People: addresses as given, names looked up in the contacts gomail knows from your mail.
    const missing: string[] = []
    for (const who of d.attendees ?? []) {
      let addr: Address | null = /\S+@\S+\.\S+/.test(who) ? { name: '', email: who } : null
      if (!addr) {
        const hit = (await api.searchContacts(who, 1).catch(() => []))[0]
        addr = hit ? { name: hit.name, email: hit.email } : null
      }
      if (!addr) missing.push(who)
      else if (!guests.value.some((g) => g.email.toLowerCase() === addr!.email.toLowerCase())) guests.value = [...guests.value, addr]
    }
    prompt.value = ''
    if (missing.length) toast(`No address found for ${missing.join(', ')}`, { description: 'Add them under Guests.' })
  } catch (e) {
    toast.error('Couldn’t fill in the event', { description: errorMessage(e) })
  } finally {
    filling.value = false
  }
}
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
  if (freq.value !== 'none' && freq.value !== 'custom' && until.value && until.value < startDay.value) return fail('“Repeat until” is before the first event.')
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
    // Only a new/changed link: the event's existing one (Meet, Teams, or a Zoom link
    // already in the location) stays put; resending it would duplicate it.
    meetingUrl: meetingUrl.value.trim() === (ev?.meetingUrl ?? '').trim() ? '' : meetingUrl.value.trim(),
    // Cleared or replaced: the old link has to go (sending nothing would keep it).
    removeMeetingUrl: !!ev?.meetingUrl && meetingUrl.value.trim() !== ev.meetingUrl.trim(),
    addVideoCall: addVideo.value && !!videoProvider.value && !providerLink.value,
    recurrence: recurrenceForSave(recurrence0, freq.value, until.value),
    reminders: [...reminders.value].sort((a, b) => a - b),
  }
}

function fail(msg: string): null {
  error.value = msg
  return null
}

const guestField = ref<InstanceType<typeof RecipientField> | null>(null)

async function save() {
  error.value = ''
  // A guest typed but not yet turned into a chip (the field commits on blur, after this click).
  guestField.value?.commit()
  const input = buildInput()
  if (!input) return
  if (!input.title) input.title = 'Untitled event'
  saving.value = true
  try {
    if (newGuests.value.length) {
      const names = newGuests.value.map((g) => g.name || g.email)
      const who = names.length <= 3 ? names.join(', ') : `${names.slice(0, 2).join(', ')} and ${names.length - 2} others`
      const ok = await askConfirm(
        'Send email invitations?',
        `${who} will get an email invitation to “${input.title}” and can reply to it.`,
        'Send invites',
        'Back to editing',
      )
      if (!ok) return
    }
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
    // A new repeat rule only makes sense for the whole series (one occurrence can't have its own).
    if (needsScopeChoice(ev!) && ruleChanged()) scope = 'series'
    else if (needsScopeChoice(ev!)) {
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

// ---- don't lose typed changes on Esc / an outside click / Cancel
const snapshot = () => JSON.stringify([title.value, allDay.value, timeZone.value, startDay.value, startTime.value, endDay.value, endTime.value, freq.value,
  until.value, location.value, meetingUrl.value, guests.value, reminders.value, description.value, calendarId.value, addVideo.value])
let initial = ''
onMounted(() => (initial = snapshot()))
let confirming = false
async function requestClose() {
  guestField.value?.commit()
  if (!readOnly.value && initial && snapshot() !== initial) {
    if (confirming) return
    confirming = true
    const ok = await askConfirm('Discard your changes?', isNew ? 'This event hasn’t been created yet.' : 'Your changes to this event aren’t saved yet.', 'Discard')
    confirming = false
    if (!ok) return
  }
  close()
}

function addReminder(v: unknown) {
  const n = Number(v)
  if (Number.isNaN(n) || reminders.value.includes(n)) return
  // Outlook's one reminder is replaced; elsewhere the menu is hidden once full.
  reminders.value = maxReminders.value === 1 ? [n] : [...reminders.value, n]
}
</script>

<template>
  <Dialog :open="open" @update:open="(v: boolean) => !v && requestClose()">
    <DialogContent :show-close-button="false" @open-auto-focus="focusFirst" class="flex max-h-[min(780px,calc(100vh-2rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[620px]">
      <!-- Own close button: the dialog's default one sits off the header's center line. -->
      <div class="border-border flex items-center gap-2 border-b py-2 pr-3 pl-5">
        <DialogTitle class="flex-1 text-sm font-semibold">{{ isNew ? 'New event' : readOnly ? 'Event' : 'Edit event' }}</DialogTitle>
        <DialogDescription class="sr-only">Event details</DialogDescription>
        <DialogClose as-child>
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Close"><XIcon /></Button>
        </DialogClose>
      </div>

      <!-- Enter in a field saves (the hidden submit button); Cmd/Ctrl+Enter also from the description. -->
      <form
        class="thin-scroll min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-5"
        @submit.prevent="!readOnly && save()"
        @keydown.meta.enter.prevent="!readOnly && save()"
        @keydown.ctrl.enter.prevent="!readOnly && save()"
      >
        <!-- Describe it in your own words; the AI model fills in the fields below. -->
        <div v-if="aiFill" class="border-border focus-within:border-ring focus-within:ring-ring/40 flex items-center gap-2 rounded-lg border bg-violet-500/5 px-3 py-1.5 focus-within:ring-2">
          <SparklesIcon class="size-4 shrink-0 text-violet-500" />
          <input
            ref="promptEl"
            v-model="prompt"
            :disabled="filling"
            placeholder="Describe it: lunch with Sam friday 12:30 at Coffee Co for an hour"
            aria-label="Describe the event"
            class="placeholder:text-muted-foreground/70 min-w-0 flex-1 bg-transparent py-1 text-[13px] outline-none"
            @keydown.enter.prevent="fillFromText"
          />
          <Button type="button" size="xs" variant="ghost" class="shrink-0 gap-1 text-violet-600 dark:text-violet-400" :disabled="!prompt.trim() || filling" @click="fillFromText">
            <LoaderIcon v-if="filling" class="animate-spin" /> Fill in
          </Button>
        </div>
        <input
          ref="titleEl"
          v-model="title"
          :disabled="readOnly"
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
              <TimeField v-if="!allDay" v-model="startTime" label="Start time" :disabled="readOnly" />
              <span v-else />
              <span />
              <span class="text-muted-foreground text-xs">Ends</span>
              <Input v-model="endDay" type="date" class="h-8 w-auto text-sm" aria-label="End date" :disabled="readOnly" />
              <TimeField v-if="!allDay" v-model="endTime" label="End time" :duration-from="endDay === startDay ? startTime : undefined" :disabled="readOnly" />
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
                <SelectTrigger class="h-8 w-full max-w-72 text-xs" aria-label="Time zone"><SelectValue /></SelectTrigger>
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
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1.5">
          <Select v-model="freq" :disabled="readOnly">
            <SelectTrigger class="h-8 w-auto min-w-44 max-w-full text-sm" aria-label="Repeat"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-if="customRule" value="custom">{{ customLabel }}</SelectItem>
              <!-- An existing series ends with "until"; the backend can't turn it back into a single event. -->
              <SelectItem v-if="!ev?.isRecurring" value="none">Does not repeat</SelectItem>
              <SelectItem value="daily">Daily</SelectItem>
              <SelectItem value="weekly">Weekly</SelectItem>
              <SelectItem value="monthly">Monthly</SelectItem>
              <SelectItem value="yearly">Yearly</SelectItem>
            </SelectContent>
          </Select>
          <template v-if="freq !== 'none' && freq !== 'custom'">
            <span class="text-muted-foreground text-sm">until</span>
            <Input v-model="until" type="date" class="h-8 w-auto text-sm" aria-label="Repeat until (optional)" :disabled="readOnly" />
          </template>
          </div>
        </div>

        <!-- Where -->
        <div class="flex items-center gap-3">
          <MapPinIcon class="text-muted-foreground size-4 shrink-0" />
          <Input v-model="location" placeholder="Add a location" aria-label="Location" class="h-8 text-sm" :disabled="readOnly" />
        </div>
        <!-- Video call: the calendar's own provider (Meet/Teams) as a toggle; any other link as text. -->
        <div v-if="videoProvider && !providerLink" class="flex items-center gap-3">
          <VideoIcon class="text-muted-foreground size-4 shrink-0" />
          <label class="flex items-center gap-2 text-sm"><Switch v-model="addVideo" :disabled="readOnly" :aria-label="`Add ${videoLabel}`" /> Add {{ videoLabel }}</label>
        </div>
        <div class="flex items-center gap-3">
          <LinkIcon class="text-muted-foreground size-4 shrink-0" />
          <Input
            v-model="meetingUrl"
            type="url"
            :placeholder="videoProvider ? 'Other meeting link (Zoom, Webex…)' : 'Meeting link (Zoom, Teams, Meet…)'"
            aria-label="Meeting link"
            class="h-8 text-sm"
            :disabled="readOnly || (addVideo && !providerLink)"
          />
        </div>

        <!-- Guests -->
        <div class="flex gap-3">
          <UsersIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <div v-if="!readOnly" class="min-w-0 flex-1">
            <div class="border-input rounded-md border px-2">
              <RecipientField ref="guestField" v-model="guests" label="Guests" />
            </div>
            <p v-if="guestHint" class="text-muted-foreground mt-1.5 flex items-center gap-1.5 text-xs"><MailIcon class="size-3.5 shrink-0" />{{ guestHint }}</p>
          </div>
          <p v-else class="text-muted-foreground min-w-0 flex-1 pt-1.5 text-sm break-words">{{ guests.length ? guests.map((g) => g.name || g.email).join(', ') : 'No guests' }}</p>
        </div>

        <!-- Reminders -->
        <div class="flex items-start gap-3">
          <BellIcon class="text-muted-foreground mt-2 size-4 shrink-0" />
          <div class="flex flex-wrap items-center gap-1.5">
            <span v-for="m in reminders" :key="m" class="bg-muted inline-flex h-7 items-center gap-1 rounded-full pr-1 pl-2.5 text-xs">
              {{ formatReminder(m) }}
              <button type="button" class="hover:bg-background rounded-full p-0.5" :aria-label="`Remove reminder ${formatReminder(m)}`" :disabled="readOnly" @click="reminders = reminders.filter((x) => x !== m)"><XIcon class="size-3" /></button>
            </span>
            <Select v-if="!readOnly && (reminders.length < maxReminders || maxReminders === 1)" :model-value="''" @update:model-value="addReminder">
              <SelectTrigger class="h-7 w-auto gap-1 border-dashed text-xs" :aria-label="reminderAction"><span>{{ reminderAction }}</span></SelectTrigger>
              <!-- popper: item-aligned has no chosen item to line up with and opened off screen. -->
              <SelectContent position="popper" align="start">
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
        <Button variant="ghost" size="sm" @click="requestClose">Cancel</Button>
        <Button v-if="!readOnly" size="sm" :disabled="saving" :title="`${saveLabel} (${shortcutLabel('mod+enter')})`" @click="save">{{ saveLabel }}</Button>
      </div>
      <ScopePrompt />
    </DialogContent>
  </Dialog>
</template>
