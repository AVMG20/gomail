<script setup lang="ts">
// Time-of-day field that follows Settings → Time format (a native <input type="time">
// follows the OS locale instead). Type a time ("14:30", "2:30pm", "930") or pick one
// from the list; ↑/↓ move through the list, Enter picks, Esc closes it.
import { computed, nextTick, ref, useId, watch } from 'vue'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { formatDuration, formatHm, parseTimeInput, timeSlots, uses12h } from '@/lib/calendar'

const model = defineModel<string>({ required: true })
const props = defineProps<{
  label: string
  disabled?: boolean
  /** Start time on the same day: list only later times, each with its duration ("30 min"). */
  durationFrom?: string
}>()

const listId = useId()
const open = ref(false)
const inputEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)
const h12 = computed(() => uses12h())
const text = ref(formatHm(model.value, h12.value))
watch([model, h12], () => {
  if (document.activeElement !== inputEl.value) text.value = formatHm(model.value, h12.value)
})

const minutes = (hm: string) => {
  const [h, m] = hm.split(':').map(Number)
  return h * 60 + m
}
const options = computed(() => {
  const from = props.durationFrom ? minutes(props.durationFrom) : -1
  return timeSlots(15)
    .filter((hm) => minutes(hm) > from)
    .map((hm) => ({ hm, label: formatHm(hm, h12.value), hint: from >= 0 ? formatDuration(minutes(hm) - from) : '' }))
})
const active = ref(-1)
/** The highlighted option was chosen with ↑/↓ or the mouse (not just the nearest to what was typed). */
let navigated = false

/** The option at or right after a time (the list's position while typing or on open). */
function nearest(hm: string | null): number {
  if (!hm) return -1
  const t = minutes(hm)
  const i = options.value.findIndex((o) => minutes(o.hm) >= t)
  return i === -1 ? options.value.length - 1 : i
}

async function show() {
  if (props.disabled) return
  active.value = nearest(parseTimeInput(text.value) ?? model.value)
  navigated = false
  open.value = true
  await nextTick()
  scrollToActive('center')
}

function scrollToActive(block: ScrollLogicalPosition = 'nearest') {
  listEl.value?.querySelector<HTMLElement>(`[data-i="${active.value}"]`)?.scrollIntoView({ block })
}

function pick(hm: string) {
  model.value = hm
  text.value = formatHm(hm, h12.value)
  open.value = false
}

/** Typed text → the model; anything unreadable snaps back to the current time. */
function commit() {
  const hm = parseTimeInput(text.value)
  if (hm && hm !== model.value) model.value = hm
  text.value = formatHm(hm ?? model.value, h12.value)
}

function onInput() {
  if (!open.value) void show()
  navigated = false
  active.value = nearest(parseTimeInput(text.value))
  void nextTick(() => scrollToActive('center'))
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (!open.value) return void show()
    const n = options.value.length
    navigated = true
    active.value = active.value < 0 ? 0 : Math.min(n - 1, Math.max(0, active.value + (e.key === 'ArrowDown' ? 1 : -1)))
    void nextTick(() => scrollToActive())
  } else if (e.key === 'Enter') {
    // Open list: take the highlighted option if one was chosen, else exactly what was typed
    // ("9:10" stays 9:10, not the nearest slot). Closed: take the typed time and let Enter
    // submit the form.
    if (open.value) {
      e.preventDefault()
      const typed = parseTimeInput(text.value)
      if (navigated && options.value[active.value]) pick(options.value[active.value].hm)
      else if (typed) pick(typed)
      else {
        commit()
        open.value = false
      }
    } else commit()
  } else if (e.key === 'Escape' && open.value) {
    // Close the list only, not the dialog around it.
    e.preventDefault()
    e.stopPropagation()
    open.value = false
  } else if (e.key === 'Tab') open.value = false
}

function hover(i: number, e: MouseEvent) {
  // Scrolling the list under a resting pointer fires mousemove without movement: not a choice.
  if (!e.movementX && !e.movementY) return
  active.value = i
  navigated = true
}

function onBlur() {
  commit()
  open.value = false
}

/** Clicks on the field itself don't count as "outside" the list. */
function onOutside(e: Event) {
  if (inputEl.value && e.target instanceof Node && inputEl.value.contains(e.target)) e.preventDefault()
}
</script>

<template>
  <Popover :open="open" @update:open="(v: boolean) => (open = v)">
    <PopoverAnchor as-child>
      <input
        ref="inputEl"
        v-model="text"
        type="text"
        inputmode="text"
        autocomplete="off"
        spellcheck="false"
        role="combobox"
        aria-autocomplete="list"
        :aria-label="label"
        :aria-expanded="open"
        :aria-controls="open ? listId : undefined"
        :aria-activedescendant="open && active >= 0 ? `${listId}-${active}` : undefined"
        :disabled="disabled"
        :class="[
          'dark:bg-input/30 border-input focus-visible:border-ring focus-visible:ring-ring/50 h-8 rounded-md border bg-transparent px-2.5 text-sm tabular-nums shadow-xs outline-none transition-[color,box-shadow] focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50',
          h12 ? 'w-[6.25rem]' : 'w-[4.75rem]',
        ]"
        @click="!open && show()"
        @input="onInput"
        @keydown="onKey"
        @blur="onBlur"
      />
    </PopoverAnchor>
    <PopoverContent
      align="start"
      class="w-auto min-w-(--reka-popover-trigger-width) gap-0 p-1"
      @open-auto-focus.prevent
      @close-auto-focus.prevent
      @interact-outside="onOutside"
    >
      <ul :id="listId" ref="listEl" role="listbox" :aria-label="label" class="thin-scroll max-h-56 overflow-y-auto">
        <li
          v-for="(o, i) in options"
          :id="`${listId}-${i}`"
          :key="o.hm"
          :data-i="i"
          role="option"
          :aria-selected="o.hm === model"
          :class="[
            'flex cursor-default items-center gap-3 rounded-sm px-2 py-1 text-sm whitespace-nowrap tabular-nums',
            i === active ? 'bg-accent text-accent-foreground' : '',
            o.hm === model ? 'font-semibold' : '',
          ]"
          @mousedown.prevent="pick(o.hm)"
          @mousemove="hover(i, $event)"
        >
          {{ o.label }}<span v-if="o.hint" class="text-muted-foreground ml-auto text-xs font-normal">{{ o.hint }}</span>
        </li>
        <li v-if="!options.length" class="text-muted-foreground px-2 py-1 text-xs">No later times today</li>
      </ul>
    </PopoverContent>
  </Popover>
</template>
