<script setup lang="ts">
// Event details card: time, location, Join, guests with responses, notes,
// reminders (yours to change, invitations too), calendar; RSVP buttons;
// Edit / Delete (invitations: remove from your calendar) / Email guests up top.
import { computed, ref } from 'vue'
import {
  AlignLeftIcon, BellIcon, CalendarIcon, CheckIcon, ChevronDownIcon, GlobeIcon, CircleHelpIcon, ClockIcon, MailIcon, MapPinIcon, PencilIcon, RepeatIcon, Trash2Icon, UsersIcon, VideoIcon, XIcon,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import FloatingCard, { type AnchorRect } from '@/components/common/FloatingCard.vue'
import { api, hasMethod } from '@/lib/api'
import { describeRepeat, formatEventWhen, formatEventWhenInZone, formatReminder, needsScopeChoice, parseRRule, REMINDER_CHOICES } from '@/lib/calendar'
import { isEditableTarget } from '@/lib/platform'
import { isSafeExternalUrl } from '@/lib/format'
import type { AttendeeResponse, CalendarEvent } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import { useComposeStore } from '@/stores/compose'
import { openEditor } from './editorState'
import { askConfirm, askScope } from './scopePrompt'

const props = defineProps<{ anchor: AnchorRect; event: CalendarEvent }>()
const emit = defineEmits<{ close: [] }>()
const cal = useCalendarStore()
const accounts = useAccountsStore()
const compose = useComposeStore()

// Live copy (RSVP / edits update the store while the card is open).
const ev = computed(() => cal.events.find((e) => e.id === props.event.id) ?? props.event)
const calendar = computed(() => cal.calendarMap.get(ev.value.calendarId))
const account = computed(() => accounts.accountMap.get(ev.value.accountId))
const canEdit = computed(() => cal.canEditEvent(ev.value))
/** Your own copy: delete it and set its reminders, also when someone else organizes it. */
const canManage = computed(() => cal.canManageEvent(ev.value))
const canSetReminders = computed(() => canManage.value && hasMethod('SetEventReminders'))
const joinUrl = computed(() => (ev.value.meetingUrl && isSafeExternalUrl(ev.value.meetingUrl) ? ev.value.meetingUrl : ''))
const repeat = computed(() => ev.value.recurrenceText || (ev.value.recurrence ? describeRepeat(parseRRule(ev.value.recurrence)) : ev.value.isRecurring ? 'Repeats' : ''))
const myEmail = computed(() => account.value?.email.toLowerCase() ?? '')
const guests = computed(() => {
  const list = [...ev.value.attendees]
  const org = ev.value.organizer
  if (org?.email && !list.some((a) => a.email.toLowerCase() === org.email.toLowerCase())) list.unshift({ ...org, response: 'accepted' })
  return list
})
const isMe = (email: string) => !!email && email.toLowerCase() === myEmail.value
/** Guests are only worth listing when someone besides you is invited. */
const hasOthers = computed(() => guests.value.some((g) => g.email && !isMe(g.email)))
/** The mailbox address, unless the calendar is already named after it (Google's primary calendar). */
const accountHint = computed(() => {
  const email = account.value?.email ?? ''
  return email.toLowerCase() === (calendar.value?.name ?? '').toLowerCase() ? '' : email
})
const reminderText = computed(() => [...ev.value.reminders].sort((a, b) => a - b).map(formatReminder).join(', '))
// One reminder picked from the list (Outlook keeps only one); "-1" = none.
const reminderValue = computed(() => (ev.value.reminders.length === 1 ? String(ev.value.reminders[0]) : ev.value.reminders.length ? '' : '-1'))
const reminderChoices = computed(() => [...new Set([...REMINDER_CHOICES, 240, 480, 10080, ...ev.value.reminders])].sort((a, b) => a - b))

async function setReminder(v: string) {
  const minutes = Number(v) < 0 ? [] : [Number(v)]
  let scope: 'this' | 'series' = 'this'
  if (needsScopeChoice(ev.value)) {
    const s = await askScope('edit', ev.value.title)
    if (!s) return
    scope = s
  }
  await cal.setReminders(ev.value, minutes, scope)
}
/** The location opens as a link: itself when it is one (a Teams room, a web page), else a Google Maps search. */
const locationUrl = computed(() => {
  const loc = ev.value.location.trim()
  if (!loc) return ''
  return isSafeExternalUrl(loc) ? loc : `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(loc)}`
})
const otherZone = computed(() => formatEventWhenInZone(ev.value))
const canRespond = computed(() => !ev.value.isOrganizer && !!ev.value.myResponse)

const RESPONSES: { value: AttendeeResponse; label: string }[] = [
  { value: 'accepted', label: 'Yes' }, { value: 'tentative', label: 'Maybe' }, { value: 'declined', label: 'No' },
]

function responseIcon(r: AttendeeResponse) {
  return r === 'accepted' ? CheckIcon : r === 'declined' ? XIcon : r === 'tentative' ? CircleHelpIcon : ClockIcon
}
function responseLabel(r: AttendeeResponse) {
  return r === 'accepted' ? 'Going' : r === 'declined' ? 'Not going' : r === 'tentative' ? 'Maybe' : 'Awaiting response'
}

const responding = ref(false)
const respondError = ref('')
/** RSVP: the card closes once the answer went through; on failure it stays open with the error. */
async function respond(r: AttendeeResponse) {
  if (responding.value) return
  responding.value = true
  respondError.value = ''
  const err = await cal.respond(ev.value, r)
  responding.value = false
  if (err) respondError.value = err
  else emit('close')
}

function edit() {
  openEditor({ event: ev.value })
  emit('close')
}

async function del() {
  let scope: 'this' | 'series' = 'this'
  if (needsScopeChoice(ev.value)) {
    const s = await askScope('delete', ev.value.title)
    if (!s) return
    scope = s
  } else if (!canEdit.value) {
    // An invitation can't be brought back with Undo.
    const who = ev.value.organizer?.name || ev.value.organizer?.email || 'the organizer'
    if (!(await askConfirm('Remove from your calendar?', `“${ev.value.title || 'Untitled event'}” is only removed from your calendar; ${who} keeps it.`, 'Remove'))) return
  }
  emit('close')
  await cal.remove(ev.value, scope)
}

// Card shortcuts: E edit, Delete/Backspace delete, Y/M/N respond (Esc closes, Tab cycles).
function onKey(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey || isEditableTarget(e.target)) return
  const k = e.key.toLowerCase()
  if (k === 'e' && canEdit.value) edit()
  else if ((k === 'delete' || k === 'backspace') && canManage.value) void del()
  else if (canRespond.value && (k === 'y' || k === 'm' || k === 'n')) void respond(k === 'y' ? 'accepted' : k === 'm' ? 'tentative' : 'declined')
  else return
  e.preventDefault()
  e.stopPropagation()
}

function emailGuests() {
  const to = guests.value.filter((g) => g.email && !isMe(g.email)).map((g) => ({ name: g.name, email: g.email }))
  compose.composeWith({ accountId: ev.value.accountId, to, subject: ev.value.title, body: `${formatEventWhen(ev.value)}${ev.value.location ? `\n${ev.value.location}` : ''}\n\n` })
  emit('close')
}
</script>

<template>
  <FloatingCard :anchor="anchor" :width="420" :label="ev.title || 'Event'" @close="emit('close')" @key="onKey">
    <div class="px-5 pt-2.5 pb-5">
      <div class="-mr-2.5 flex justify-end">
        <Button v-if="canEdit" variant="ghost" size="icon-sm" aria-label="Edit event" aria-keyshortcuts="E" @click="edit"><PencilIcon /></Button>
        <Button v-if="canManage" variant="ghost" size="icon-sm" :aria-label="canEdit ? 'Delete event' : 'Remove from your calendar'" :title="canEdit ? undefined : 'Remove from your calendar'" aria-keyshortcuts="Delete" class="hover:text-destructive" @click="del"><Trash2Icon /></Button>
        <Button v-if="hasOthers" variant="ghost" size="icon-sm" aria-label="Email guests" title="Email guests" @click="emailGuests"><MailIcon /></Button>
        <Button variant="ghost" size="icon-sm" aria-label="Close" @click="emit('close')"><XIcon /></Button>
      </div>

      <div class="flex items-start gap-4">
        <span class="flex h-7 w-5 shrink-0 items-center justify-center"><span class="size-3.5 rounded-[4px]" :style="{ background: calendar?.color ?? 'var(--muted-foreground)' }" /></span>
        <div class="min-w-0 flex-1">
          <h3 :class="['text-xl leading-7 font-normal break-words [overflow-wrap:anywhere]', ev.myResponse === 'declined' && 'text-muted-foreground line-through']">{{ ev.title || 'Untitled event' }}</h3>
          <p class="text-muted-foreground mt-1 text-sm">{{ formatEventWhen(ev, undefined, true) }}</p>
          <p v-if="otherZone" class="text-muted-foreground mt-0.5 flex items-center gap-1.5 text-xs" :title="ev.timeZone"><GlobeIcon class="size-3" /> {{ otherZone }}</p>
          <p v-if="repeat" class="text-muted-foreground mt-0.5 flex items-center gap-1.5 text-xs"><RepeatIcon class="size-3" /> {{ repeat }}</p>
        </div>
      </div>

      <div class="mt-4 space-y-3.5 text-sm">
        <div v-if="joinUrl" class="flex items-center gap-4">
          <span class="flex w-5 shrink-0 justify-center"><VideoIcon class="text-muted-foreground size-4" /></span>
          <Button size="sm" @click="api.openExternal(joinUrl)">Join meeting</Button>
        </div>
        <div v-if="ev.location" class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><MapPinIcon class="text-muted-foreground size-4" /></span>
          <a :href="locationUrl" class="min-w-0 break-words underline-offset-2 hover:underline" :title="isSafeExternalUrl(ev.location.trim()) ? 'Open link' : 'Open in Google Maps'" @click.prevent="api.openExternal(locationUrl)">{{ ev.location }}</a>
        </div>

        <div v-if="hasOthers" class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><UsersIcon class="text-muted-foreground size-4" /></span>
          <ul class="min-w-0 flex-1 space-y-1.5">
            <li>{{ guests.length }} guest{{ guests.length === 1 ? '' : 's' }}</li>
            <li v-for="g in guests.slice(0, 8)" :key="g.email" class="flex items-center gap-2">
              <component :is="responseIcon(g.response)" :class="['size-3.5 shrink-0', g.response === 'accepted' ? 'text-emerald-600 dark:text-emerald-400' : g.response === 'declined' ? 'text-destructive' : 'text-muted-foreground']" :aria-label="responseLabel(g.response)" />
              <span class="min-w-0 flex-1 truncate" :title="g.email">{{ g.name || g.email }}<span v-if="isMe(g.email)" class="text-muted-foreground"> (you)</span><span v-if="g.email === ev.organizer?.email" class="text-muted-foreground"> · organizer</span></span>
            </li>
            <li v-if="guests.length > 8" class="text-muted-foreground text-xs">+{{ guests.length - 8 }} more</li>
          </ul>
        </div>

        <div v-if="ev.description" class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><AlignLeftIcon class="text-muted-foreground size-4" /></span>
          <p class="selectable line-clamp-6 min-w-0 break-words whitespace-pre-line">{{ ev.description }}</p>
        </div>
        <div v-if="canSetReminders" class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><BellIcon class="text-muted-foreground size-4" /></span>
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <button type="button" class="hover:bg-accent -mx-1.5 -my-0.5 flex items-center gap-1 rounded-md px-1.5 py-0.5 text-left" aria-label="Change reminder">
                <span :class="!reminderText && 'text-muted-foreground'">{{ reminderText || 'Add a reminder' }}</span>
                <ChevronDownIcon class="text-muted-foreground size-3.5 shrink-0" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" class="max-h-72 w-48">
              <DropdownMenuRadioGroup :model-value="reminderValue" @update:model-value="(v) => setReminder(String(v))">
                <DropdownMenuRadioItem value="-1">No reminder</DropdownMenuRadioItem>
                <DropdownMenuRadioItem v-for="m in reminderChoices" :key="m" :value="String(m)">{{ formatReminder(m) }}</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        <div v-else-if="reminderText" class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><BellIcon class="text-muted-foreground size-4" /></span>
          <span>{{ reminderText }}</span>
        </div>
        <div class="flex items-start gap-4">
          <span class="flex h-5 w-5 shrink-0 items-center justify-center"><CalendarIcon class="text-muted-foreground size-4" /></span>
          <div class="min-w-0">
            <p class="truncate">{{ calendar?.name }}</p>
            <p v-if="accountHint" class="text-muted-foreground truncate text-xs">{{ accountHint }}</p>
            <p v-if="!canManage && !canRespond" class="text-muted-foreground text-xs">Read-only calendar</p>
          </div>
        </div>
      </div>

      <div v-if="canRespond" class="border-border mt-4 flex items-center gap-1.5 border-t pt-3">
        <span class="text-muted-foreground mr-auto text-sm">Going?</span>
        <Button
          v-for="r in RESPONSES"
          :key="r.value"
          size="xs"
          :variant="ev.myResponse === r.value ? 'default' : 'outline'"
          :aria-pressed="ev.myResponse === r.value"
          :disabled="responding"
          @click="respond(r.value)"
        >{{ r.label }}</Button>
      </div>
      <p v-if="respondError" role="alert" class="text-destructive mt-2 text-xs">{{ respondError }}</p>
    </div>
  </FloatingCard>
</template>
