<script setup lang="ts">
// Small month navigator: click a day to jump the main calendar there. One tab stop;
// arrows move by day/week, Home/End to the week's ends, PageUp/PageDown by month.
import { computed, nextTick, ref, watch } from 'vue'
import { ChevronLeftIcon, ChevronRightIcon } from '@lucide/vue'
import { addDays, fromYmd, startOfWeek, toYmd } from '@/lib/calendar'

const props = defineProps<{ selected: Date; weekStart: number; rangeStart?: Date; rangeEnd?: Date; busyDays?: Set<string> }>()
const emit = defineEmits<{ pick: [Date] }>()

const month = ref(new Date(props.selected.getFullYear(), props.selected.getMonth(), 1))
/** The day that holds the grid's tab stop. */
const cursor = ref(toYmd(props.selected))
watch(() => props.selected, (d) => {
  if (d.getMonth() !== month.value.getMonth() || d.getFullYear() !== month.value.getFullYear()) month.value = new Date(d.getFullYear(), d.getMonth(), 1)
  cursor.value = toYmd(d)
})
const grid = ref<HTMLElement | null>(null)
/** The tab stop: the cursor day when it's on this page, else the 1st of the month. */
const tabDay = computed(() => (days.value.some((d) => toYmd(d) === cursor.value) ? cursor.value : toYmd(month.value)))

function onKey(e: KeyboardEvent) {
  const cur = fromYmd(tabDay.value)
  const col = (cur.getDay() - props.weekStart + 7) % 7
  const step: Record<string, () => Date> = {
    ArrowLeft: () => addDays(cur, -1),
    ArrowRight: () => addDays(cur, 1),
    ArrowUp: () => addDays(cur, -7),
    ArrowDown: () => addDays(cur, 7),
    Home: () => addDays(cur, -col),
    End: () => addDays(cur, 6 - col),
    PageUp: () => new Date(cur.getFullYear(), cur.getMonth() - 1, Math.min(cur.getDate(), 28)),
    PageDown: () => new Date(cur.getFullYear(), cur.getMonth() + 1, Math.min(cur.getDate(), 28)),
  }
  const next = step[e.key]?.()
  if (!next) return
  e.preventDefault()
  e.stopPropagation() // not the calendar's own ←/→ (previous/next period)
  cursor.value = toYmd(next)
  if (next.getMonth() !== month.value.getMonth() || next.getFullYear() !== month.value.getFullYear()) month.value = new Date(next.getFullYear(), next.getMonth(), 1)
  void nextTick(() => grid.value?.querySelector<HTMLElement>(`[data-ymd="${cursor.value}"]`)?.focus())
}

const locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'
const title = computed(() => month.value.toLocaleDateString(locale, { month: 'long', year: 'numeric' }))
const weekdays = computed(() => {
  const s = startOfWeek(new Date(), props.weekStart)
  return Array.from({ length: 7 }, (_, i) => addDays(s, i).toLocaleDateString(locale, { weekday: 'narrow' }))
})
const days = computed(() => {
  const s = startOfWeek(month.value, props.weekStart)
  return Array.from({ length: 42 }, (_, i) => addDays(s, i))
})
const today = toYmd(new Date())

function inRange(d: Date) {
  return !!props.rangeStart && !!props.rangeEnd && d >= props.rangeStart && d < props.rangeEnd
}
function shift(n: number) {
  month.value = new Date(month.value.getFullYear(), month.value.getMonth() + n, 1)
}
</script>

<template>
  <div class="select-none">
    <div class="mb-1 flex items-center justify-between pl-1">
      <span class="text-[13px] font-semibold capitalize">{{ title }}</span>
      <div class="flex">
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent rounded-md p-1" aria-label="Previous month" @click="shift(-1)"><ChevronLeftIcon class="size-4" /></button>
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-sidebar-accent rounded-md p-1" aria-label="Next month" @click="shift(1)"><ChevronRightIcon class="size-4" /></button>
      </div>
    </div>
    <div ref="grid" class="grid grid-cols-7 text-center" role="group" :aria-label="title" @keydown="onKey">
      <span v-for="(w, i) in weekdays" :key="i" class="text-muted-foreground py-1 text-[10.5px] font-medium" aria-hidden="true">{{ w }}</span>
      <button
        v-for="d in days"
        :key="d.getTime()"
        type="button"
        :data-ymd="toYmd(d)"
        :tabindex="toYmd(d) === tabDay ? 0 : -1"
        :aria-label="d.toLocaleDateString(locale, { weekday: 'long', day: 'numeric', month: 'long' })"
        :aria-current="toYmd(d) === today ? 'date' : undefined"
        :class="[
          'focus-visible:ring-ring relative mx-auto my-px flex size-7 items-center justify-center rounded-full text-[12px] tabular-nums transition-colors outline-none focus-visible:ring-2',
          d.getMonth() !== month.getMonth() ? 'text-muted-foreground' : '',
          toYmd(d) === today ? 'bg-primary text-primary-foreground font-semibold' : inRange(d) ? 'bg-sidebar-accent text-foreground' : 'hover:bg-sidebar-accent',
        ]"
        @click="(cursor = toYmd(d)), emit('pick', d)"
      >
        {{ d.getDate() }}
        <span v-if="busyDays?.has(toYmd(d)) && toYmd(d) !== today" class="bg-muted-foreground/50 absolute bottom-0.5 left-1/2 size-[3px] -translate-x-1/2 rounded-full" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
