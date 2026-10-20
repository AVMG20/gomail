<script setup lang="ts">
// Compact meeting-invitation card at the top of the reading pane (Message.invite).
import { computed, ref } from 'vue'
import { CalendarDaysIcon, MapPinIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { eventDate, formatEventWhen } from '@/lib/calendar'
import { displayName } from '@/lib/format'
import type { AttendeeResponse, EventInvite } from '@/lib/types'
import { useCalendarStore } from '@/stores/calendar'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{ invite: EventInvite }>()
const ui = useUiStore()
// The calendar store applies Settings → Calendar → Time format to every displayed time.
useCalendarStore()
const response = ref<AttendeeResponse>(props.invite.myResponse ?? '')
const busy = ref(false)
const cancelled = computed(() => props.invite.method?.toUpperCase() === 'CANCEL')
const canRespond = computed(() => !!props.invite.eventId && hasMethod('RespondToEvent') && !cancelled.value)
/** Not on a synced calendar yet: show the RSVP buttons disabled with an explanation. */
const pending = computed(() => !props.invite.eventId && hasMethod('RespondToEvent') && !cancelled.value)
const OPTIONS: { value: AttendeeResponse; label: string }[] = [
  { value: 'accepted', label: 'Accept' }, { value: 'tentative', label: 'Maybe' }, { value: 'declined', label: 'Decline' },
]

async function respond(r: AttendeeResponse) {
  if (!props.invite.eventId || busy.value) return
  const before = response.value
  response.value = r
  busy.value = true
  try {
    await api.respondToEvent(props.invite.eventId, r)
    toast.success(r === 'accepted' ? 'Accepted' : r === 'declined' ? 'Declined' : 'Marked as maybe', { description: `${props.invite.organizer?.name || props.invite.organizer?.email || 'The organizer'} will be notified.` })
    const cal = useCalendarStore()
    if (cal.loaded) void cal.fetchEvents()
  } catch (e) {
    response.value = before
    toast.error('Couldn’t send your response', { description: errorMessage(e) })
  } finally {
    busy.value = false
  }
}

function viewInCalendar() {
  const cal = useCalendarStore()
  cal.focusDate = eventDate(props.invite.start, props.invite.allDay)
  // Open the event itself when it's in a synced calendar.
  if (props.invite.eventId) cal.openEventId = props.invite.eventId
  ui.mainView = 'calendar'
}
</script>

<template>
  <div class="border-border bg-muted/30 mx-6 mt-4 flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3" role="region" aria-label="Meeting invitation">
    <div class="bg-background text-primary flex size-10 shrink-0 flex-col items-center justify-center rounded-lg border text-center leading-none">
      <span class="text-[9px] font-semibold uppercase">{{ eventDate(invite.start, invite.allDay).toLocaleDateString(undefined, { month: 'short' }) }}</span>
      <span class="text-foreground text-sm font-semibold">{{ eventDate(invite.start, invite.allDay).getDate() }}</span>
    </div>
    <!-- Text takes at least ~17rem; the buttons wrap below it before the time gets cut. -->
    <div class="min-w-0 flex-[1_1_17rem]">
      <p :class="['truncate text-[13px] font-semibold', cancelled && 'line-through']">{{ invite.title || 'Invitation' }}</p>
      <p class="text-muted-foreground text-xs">{{ cancelled ? 'Cancelled · ' : '' }}{{ formatEventWhen(invite) }}</p>
      <p v-if="invite.organizer?.email" class="text-muted-foreground truncate text-xs">from {{ displayName(invite.organizer) }}</p>
      <p v-if="invite.location" class="text-muted-foreground mt-0.5 flex items-center gap-1 truncate text-xs"><MapPinIcon class="size-3 shrink-0" /> {{ invite.location }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-1.5">
      <template v-if="canRespond || pending">
        <Button
          v-for="o in OPTIONS"
          :key="o.value"
          size="xs"
          :variant="response === o.value ? 'default' : 'outline'"
          :aria-pressed="response === o.value"
          :disabled="busy || pending"
          :data-tip="pending ? 'Available once this event syncs to your calendar' : undefined"
          @click="respond(o.value)"
        >{{ o.label }}</Button>
      </template>
      <Button v-if="hasMethod('ListCalendars')" variant="ghost" size="xs" class="text-muted-foreground" @click="viewInCalendar"><CalendarDaysIcon /> View in calendar</Button>
    </div>
  </div>
</template>
