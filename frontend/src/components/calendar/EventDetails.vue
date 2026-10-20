<script setup lang="ts">
// Event details card: time, calendar, location, Join, guests with responses,
// RSVP buttons, Edit / Delete / Email guests.
import { computed, ref } from 'vue'
import {
  CalendarIcon, CheckIcon, GlobeIcon, CircleHelpIcon, ClockIcon, MailIcon, MapPinIcon, PencilIcon, RepeatIcon, Trash2Icon, UsersIcon, VideoIcon, XIcon,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FloatingCard, { type AnchorRect } from '@/components/common/FloatingCard.vue'
import { api } from '@/lib/api'
import { describeRepeat, formatEventWhen, formatEventWhenInZone, needsScopeChoice, parseRRule } from '@/lib/calendar'
import { isEditableTarget } from '@/lib/platform'
import { isSafeExternalUrl } from '@/lib/format'
import type { AttendeeResponse, CalendarEvent } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import { useComposeStore } from '@/stores/compose'
import { openEditor } from './editorState'
import { askScope } from './scopePrompt'

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
const joinUrl = computed(() => (ev.value.meetingUrl && isSafeExternalUrl(ev.value.meetingUrl) ? ev.value.meetingUrl : ''))
const repeat = computed(() => ev.value.recurrenceText || (ev.value.recurrence ? describeRepeat(parseRRule(ev.value.recurrence)) : ev.value.isRecurring ? 'Repeats' : ''))
const myEmail = computed(() => account.value?.email.toLowerCase() ?? '')
const guests = computed(() => {
  const list = [...ev.value.attendees]
  const org = ev.value.organizer
  if (org?.email && !list.some((a) => a.email.toLowerCase() === org.email.toLowerCase())) list.unshift({ ...org, response: 'accepted' })
  return list
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
  }
  emit('close')
  await cal.remove(ev.value, scope)
}

// Card shortcuts: E edit, Delete/Backspace delete, Y/M/N respond (Esc closes, Tab cycles).
function onKey(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey || isEditableTarget(e.target)) return
  const k = e.key.toLowerCase()
  if (k === 'e' && canEdit.value) edit()
  else if ((k === 'delete' || k === 'backspace') && canEdit.value) void del()
  else if (canRespond.value && (k === 'y' || k === 'm' || k === 'n')) void respond(k === 'y' ? 'accepted' : k === 'm' ? 'tentative' : 'declined')
  else return
  e.preventDefault()
  e.stopPropagation()
}

function emailGuests() {
  const to = guests.value.filter((g) => g.email && g.email.toLowerCase() !== myEmail.value).map((g) => ({ name: g.name, email: g.email }))
  compose.composeWith({ accountId: ev.value.accountId, to, subject: ev.value.title, body: `${formatEventWhen(ev.value)}${ev.value.location ? `\n${ev.value.location}` : ''}\n\n` })
  emit('close')
}
</script>

<template>
  <FloatingCard :anchor="anchor" :width="360" :label="ev.title || 'Event'" @close="emit('close')" @key="onKey">
    <div class="p-4">
      <div class="flex items-start gap-3">
        <span class="mt-1.5 size-3 shrink-0 rounded-[4px]" :style="{ background: calendar?.color ?? 'var(--muted-foreground)' }" />
        <div class="min-w-0 flex-1">
          <h3 :class="['text-base leading-snug font-semibold break-words [overflow-wrap:anywhere]', ev.myResponse === 'declined' && 'text-muted-foreground line-through']">{{ ev.title || 'Untitled event' }}</h3>
          <p class="text-muted-foreground mt-0.5 text-[13px]">{{ formatEventWhen(ev) }}</p>
          <p v-if="otherZone" class="text-muted-foreground mt-0.5 flex items-center gap-1.5 text-xs" :title="ev.timeZone"><GlobeIcon class="size-3" /> {{ otherZone }}</p>
          <p v-if="repeat" class="text-muted-foreground mt-0.5 flex items-center gap-1.5 text-xs"><RepeatIcon class="size-3" /> {{ repeat }}</p>
        </div>
        <div class="-mt-1 -mr-1 flex shrink-0">
          <Button v-if="canEdit" variant="ghost" size="icon-sm" aria-label="Edit event" aria-keyshortcuts="E" @click="edit"><PencilIcon /></Button>
          <Button v-if="canEdit" variant="ghost" size="icon-sm" aria-label="Delete event" aria-keyshortcuts="Delete" class="hover:text-destructive" @click="del"><Trash2Icon /></Button>
          <Button variant="ghost" size="icon-sm" aria-label="Close" @click="emit('close')"><XIcon /></Button>
        </div>
      </div>

      <div class="mt-3 space-y-2.5 text-[13px]">
        <Button v-if="joinUrl" size="sm" class="w-full" @click="api.openExternal(joinUrl)"><VideoIcon /> Join meeting</Button>
        <p v-if="ev.location" class="flex items-start gap-2.5"><MapPinIcon class="text-muted-foreground mt-0.5 size-3.5 shrink-0" /> <span class="selectable">{{ ev.location }}</span></p>
        <p class="flex items-center gap-2.5"><CalendarIcon class="text-muted-foreground size-3.5 shrink-0" /> <span class="truncate">{{ calendar?.name }}</span><span class="text-muted-foreground truncate text-xs">{{ account?.email }}</span></p>

        <div v-if="guests.length" class="flex items-start gap-2.5">
          <UsersIcon class="text-muted-foreground mt-0.5 size-3.5 shrink-0" />
          <ul class="min-w-0 flex-1 space-y-1">
            <li class="text-muted-foreground text-xs">{{ guests.length }} guest{{ guests.length === 1 ? '' : 's' }}</li>
            <li v-for="g in guests.slice(0, 8)" :key="g.email" class="flex items-center gap-2">
              <component :is="responseIcon(g.response)" :class="['size-3.5 shrink-0', g.response === 'accepted' ? 'text-emerald-600 dark:text-emerald-400' : g.response === 'declined' ? 'text-destructive' : 'text-muted-foreground']" :aria-label="responseLabel(g.response)" />
              <span class="min-w-0 flex-1 truncate">{{ g.name || g.email }}<span v-if="g.email === ev.organizer?.email" class="text-muted-foreground"> · organizer</span></span>
            </li>
            <li v-if="guests.length > 8" class="text-muted-foreground text-xs">+{{ guests.length - 8 }} more</li>
          </ul>
        </div>

        <p v-if="ev.description" class="text-muted-foreground selectable line-clamp-4 text-xs leading-relaxed whitespace-pre-line">{{ ev.description }}</p>
        <p v-if="!canEdit && !canRespond" class="text-muted-foreground text-xs">Read-only calendar</p>
      </div>

      <div v-if="canRespond" class="border-border mt-3 flex items-center gap-1.5 border-t pt-3">
        <span class="text-muted-foreground mr-auto text-xs">Going?</span>
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
      <div v-if="guests.some((g) => g.email && g.email.toLowerCase() !== myEmail)" class="mt-2 flex justify-end">
        <Button variant="ghost" size="xs" class="text-muted-foreground" @click="emailGuests"><MailIcon /> Email guests</Button>
      </div>
    </div>
  </FloatingCard>
</template>
