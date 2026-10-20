<script setup lang="ts">
// Small month navigator: click a day to jump the main calendar there.
import { computed, ref, watch } from 'vue'
import { ChevronLeftIcon, ChevronRightIcon } from '@lucide/vue'
import { addDays, startOfWeek, toYmd } from '@/lib/calendar'

const props = defineProps<{ selected: Date; weekStart: number; rangeStart?: Date; rangeEnd?: Date; busyDays?: Set<string> }>()
const emit = defineEmits<{ pick: [Date] }>()

const month = ref(new Date(props.selected.getFullYear(), props.selected.getMonth(), 1))
watch(() => props.selected, (d) => {
  if (d.getMonth() !== month.value.getMonth() || d.getFullYear() !== month.value.getFullYear()) month.value = new Date(d.getFullYear(), d.getMonth(), 1)
})

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
    <div class="grid grid-cols-7 text-center" role="group" :aria-label="title">
      <span v-for="(w, i) in weekdays" :key="i" class="text-muted-foreground py-1 text-[10.5px] font-medium" aria-hidden="true">{{ w }}</span>
      <button
        v-for="d in days"
        :key="d.getTime()"
        type="button"
        :aria-label="d.toLocaleDateString(locale, { weekday: 'long', day: 'numeric', month: 'long' })"
        :aria-current="toYmd(d) === today ? 'date' : undefined"
        :class="[
          'relative mx-auto my-px flex size-7 items-center justify-center rounded-full text-[12px] tabular-nums transition-colors',
          d.getMonth() !== month.getMonth() ? 'text-muted-foreground' : '',
          toYmd(d) === today ? 'bg-primary text-primary-foreground font-semibold' : inRange(d) ? 'bg-sidebar-accent text-foreground' : 'hover:bg-sidebar-accent',
        ]"
        @click="emit('pick', d)"
      >
        {{ d.getDate() }}
        <span v-if="busyDays?.has(toYmd(d)) && toYmd(d) !== today" class="bg-muted-foreground/50 absolute bottom-0.5 left-1/2 size-[3px] -translate-x-1/2 rounded-full" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
