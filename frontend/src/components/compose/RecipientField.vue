<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api, hasMethod } from '@/lib/api'
import { XIcon } from '@lucide/vue'
import { displayName, formatAddress, isEmail, parseAddress, splitAddressList } from '@/lib/format'
import type { Address } from '@/lib/types'
import { useMailStore } from '@/stores/mail'

const props = defineProps<{ label: string; autofocus?: boolean }>()
const model = defineModel<Address[]>({ required: true })
const mail = useMailStore()

const text = ref('')
const focused = ref(false)
const active = ref(0)
const input = ref<HTMLInputElement | null>(null)

// Frequency-ranked contacts from the backend (all mail), falling back to
// addresses seen in loaded messages. Stale responses are dropped.
const remote = ref<Address[]>([])
let token = 0
let timer: ReturnType<typeof setTimeout> | null = null
watch(text, (q) => {
  if (timer) clearTimeout(timer)
  const query = q.trim()
  if (!query || !hasMethod('SearchContacts')) {
    token++
    remote.value = []
    return
  }
  timer = setTimeout(async () => {
    const t = ++token
    try {
      const list = await api.searchContacts(query, 8)
      if (t === token) remote.value = list.map((c) => ({ name: c.name, email: c.email }))
    } catch {
      if (t === token) remote.value = []
    }
  }, 100)
})
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
  token++
  // Minimising the window (Esc) or opening another one unmounts the field: keep what was typed.
  if (text.value.trim()) commit()
})
/** Esc hid the suggestions; typing shows them again. */
const dismissed = ref(false)
watch(text, () => (dismissed.value = false))
const suggestions = computed(() => {
  if (!text.value.trim() || dismissed.value) return []
  const taken = new Set(model.value.map((a) => a.email.toLowerCase()))
  const seen = new Set<string>()
  return [...remote.value, ...mail.suggestAddresses(text.value, 7)].filter((a) => {
    const k = a.email.toLowerCase()
    if (taken.has(k) || seen.has(k)) return false
    seen.add(k)
    return true
  }).slice(0, 7)
})
watch(suggestions, () => (active.value = 0))

/** Add chips in one update: the model only reflects a change after the parent re-renders,
 *  so adding one by one would keep just the last address. */
function addAll(list: Address[]) {
  const next = [...model.value]
  for (const a of list) {
    if (!next.some((x) => x.email.toLowerCase() === a.email.toLowerCase())) next.push(a)
  }
  if (next.length !== model.value.length) model.value = next
  text.value = ''
  active.value = 0
}

function add(a: Address) {
  addAll([a])
}

function commit(): boolean {
  const raw = splitAddressList(text.value)
  if (!raw.length) return false
  // Invalid entries become flagged chips (sending refuses them) instead of being dropped.
  addAll(raw.map((r) => parseAddress(r) ?? { name: '', email: r }))
  return true
}

/** The text so far ends inside "a quoted name" or an <address>. */
function insideQuoteOrAngle(t: string): boolean {
  let quoted = false
  let angle = false
  for (const c of t) {
    if (c === '"' && !angle) quoted = !quoted
    else if (c === '<' && !quoted) angle = true
    else if (c === '>' && !quoted) angle = false
  }
  return quoted || angle
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' && suggestions.value.length) {
    e.preventDefault()
    active.value = (active.value + 1) % suggestions.value.length
  } else if (e.key === 'ArrowUp' && suggestions.value.length) {
    e.preventDefault()
    active.value = (active.value - 1 + suggestions.value.length) % suggestions.value.length
  } else if ((e.key === ',' || e.key === ';') && insideQuoteOrAngle(text.value)) {
    // Part of a display name like "Doe, John": type it, don't split there.
  } else if (e.key === 'Enter' || e.key === 'Tab' || e.key === ',' || e.key === ';') {
    // A complete address that differs from the top suggestion is taken as typed (unless the
    // user arrowed to a suggestion).
    const typed = parseAddress(text.value.trim())
    const pick = suggestions.value[active.value]
    const takeSuggestion = pick && e.key !== ',' && e.key !== ';' && !(typed && active.value === 0 && typed.email.toLowerCase() !== pick.email.toLowerCase())
    // Tab picks/commits and still moves on to the next field.
    if (takeSuggestion) {
      if (e.key !== 'Tab') e.preventDefault()
      add(pick)
    } else if (text.value.trim()) {
      commit()
      if (e.key !== 'Tab') e.preventDefault()
    }
  } else if (e.key === 'Backspace' && !text.value && model.value.length) {
    model.value = model.value.slice(0, -1)
  } else if (e.key === 'Escape' && suggestions.value.length) {
    // Close the suggestions, keep what was typed (a second Esc minimises the window).
    e.stopPropagation()
    dismissed.value = true
  }
}

function onPaste(e: ClipboardEvent) {
  const data = e.clipboardData?.getData('text') ?? ''
  if (/[,;\n]/.test(data) || isEmail(data.trim())) {
    e.preventDefault()
    text.value = data
    commit()
  }
}

function onBlur() {
  focused.value = false
  // Delayed so a click on a suggestion wins; whatever is still typed then becomes a chip.
  setTimeout(() => {
    if (!focused.value && text.value.trim()) commit()
  }, 150)
}

/** Double-click a chip to correct it: it goes back into the text box. */
function editChip(a: Address) {
  if (text.value.trim()) commit()
  model.value = model.value.filter((x) => x !== a)
  // A name with a comma is quoted, so it isn't split into two addresses again.
  text.value = !a.name ? a.email : /[,;"]/.test(a.name) ? `"${a.name.replace(/"/g, '')}" <${a.email}>` : formatAddress(a)
  input.value?.focus()
}

/** Turn typed text into chips now (before send/save, which only read chips). */
defineExpose({ focus: () => input.value?.focus(), commit })
</script>

<template>
  <div class="relative flex min-h-9 flex-wrap items-center gap-1 py-1" @click="input?.focus()">
    <span class="text-muted-foreground w-10 shrink-0 text-[13px]">{{ props.label }}</span>
    <span
      v-for="a in model"
      :key="a.email"
      :class="['bg-muted inline-flex h-6 max-w-60 items-center gap-1 rounded-full pr-1 pl-2.5 text-xs', !isEmail(a.email) && 'bg-destructive/10 text-destructive ring-destructive/30 ring-1']"
      :title="isEmail(a.email) ? `${a.email} · double-click to edit` : `${a.email} is not a valid address · double-click to fix it`"
      @dblclick.stop="editChip(a)"
    >
      <span class="truncate">{{ displayName(a) }}</span>
      <button type="button" class="text-muted-foreground hover:text-foreground rounded-full p-0.5" :aria-label="`Remove ${a.email}`" @click.stop="model = model.filter((x) => x !== a)">
        <XIcon class="size-3" />
      </button>
    </span>
    <input
      ref="input"
      v-model="text"
      type="text"
      :autofocus="autofocus"
      class="h-7 min-w-32 flex-1 bg-transparent text-[13px] outline-none"
      :aria-label="props.label"
      autocomplete="off"
      spellcheck="false"
      @keydown="onKey"
      @focus="focused = true"
      @blur="onBlur"
      @paste="onPaste"
    />
    <div v-if="focused && suggestions.length" class="bg-popover text-popover-foreground border-border absolute top-full left-10 z-50 mt-1 w-80 overflow-hidden rounded-lg border p-1 shadow-lg">
      <button
        v-for="(s, i) in suggestions"
        :key="s.email"
        type="button"
        :class="['flex w-full flex-col items-start rounded-md px-2.5 py-1.5 text-left', i === active ? 'bg-muted' : '']"
        @mousedown.prevent="add(s)"
        @mouseenter="active = i"
      >
        <span class="text-[13px] font-medium">{{ s.name || s.email }}</span>
        <span v-if="s.name" class="text-muted-foreground text-xs">{{ s.email }}</span>
      </button>
    </div>
  </div>
</template>
