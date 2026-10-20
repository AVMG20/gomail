<script setup lang="ts">
// Quick-create card after clicking or dragging on an empty slot.
import { computed, ref } from 'vue'
import { ClockIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FloatingCard, { type AnchorRect } from '@/components/common/FloatingCard.vue'
import { formatEventWhen, localTimeZone, toRfc3339, toYmd } from '@/lib/calendar'
import type { EventInput } from '@/lib/types'
import { useCalendarStore } from '@/stores/calendar'
import { useSettingsStore } from '@/stores/settings'
import CalendarPicker from './CalendarPicker.vue'
import { openEditor } from './editorState'
import { askConfirm } from './scopePrompt'

const props = defineProps<{ anchor: AnchorRect; start: Date; end: Date; allDay: boolean }>()
const emit = defineEmits<{ close: [] }>()
const cal = useCalendarStore()
const settings = useSettingsStore()

const title = ref('')
const calendarId = ref(cal.defaultCalendar)
const saving = ref(false)

const times = computed(() => ({
  start: props.allDay ? toYmd(props.start) : toRfc3339(props.start),
  end: props.allDay ? toYmd(props.end) : toRfc3339(props.end),
  allDay: props.allDay,
}))

function draft(): EventInput {
  const r = settings.settings?.calendarDefaultReminder ?? 10
  return {
    calendarId: calendarId.value, title: title.value.trim(), ...times.value, timeZone: props.allDay ? '' : localTimeZone(),
    location: '', description: '', attendees: [], meetingUrl: '', recurrence: '', reminders: r >= 0 ? [r] : [],
  }
}

async function save() {
  if (!calendarId.value || saving.value) return
  saving.value = true
  const input = draft()
  if (!input.title) input.title = 'Untitled event'
  emit('close')
  await cal.create(input)
}

// A typed title isn't dropped by a stray click outside or Esc without asking.
let confirming = false
async function requestClose() {
  if (title.value.trim() && !saving.value) {
    if (confirming) return
    confirming = true
    const ok = await askConfirm('Discard this event?', `“${title.value.trim()}” hasn’t been saved.`, 'Discard')
    confirming = false
    if (!ok) return
  }
  emit('close')
}

function more() {
  openEditor({ draft: draft() })
  emit('close')
}
</script>

<template>
  <FloatingCard :anchor="anchor" :width="320" label="New event" @close="requestClose">
    <form class="space-y-3 p-4" @submit.prevent="save">
      <input
        v-model="title"
        autofocus
        placeholder="Add a title"
        aria-label="Title"
        class="placeholder:text-muted-foreground/70 w-full bg-transparent text-base font-semibold outline-none"
      />
      <p class="text-muted-foreground flex items-center gap-2 text-xs"><ClockIcon class="size-3.5" /> {{ formatEventWhen(times) }}</p>
      <CalendarPicker v-if="cal.writableCalendars.length" v-model="calendarId" compact />
      <p v-else class="text-muted-foreground text-xs">Turn on a mailbox with an editable calendar to add events.</p>
      <div class="flex items-center justify-between gap-2 pt-1">
        <Button type="button" variant="ghost" size="sm" class="text-muted-foreground -ml-2" @click="more">More options</Button>
        <Button type="submit" size="sm" :disabled="!calendarId || saving">Save</Button>
      </div>
    </form>
  </FloatingCard>
</template>
