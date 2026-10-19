<script setup lang="ts">
import { computed, ref } from 'vue'
import { XIcon } from '@lucide/vue'
import { displayName, isEmail, parseAddressList } from '@/lib/format'
import type { Address } from '@/lib/types'
import { useMailStore } from '@/stores/mail'

const props = defineProps<{ label: string; autofocus?: boolean }>()
const model = defineModel<Address[]>({ required: true })
const mail = useMailStore()

const text = ref('')
const focused = ref(false)
const active = ref(0)
const input = ref<HTMLInputElement | null>(null)

const suggestions = computed(() => {
  if (!text.value.trim()) return []
  const taken = new Set(model.value.map((a) => a.email.toLowerCase()))
  return mail.suggestAddresses(text.value, 7).filter((a) => !taken.has(a.email.toLowerCase()))
})

function add(a: Address) {
  if (!model.value.some((x) => x.email.toLowerCase() === a.email.toLowerCase())) model.value = [...model.value, a]
  text.value = ''
  active.value = 0
}

function commit(): boolean {
  const parsed = parseAddressList(text.value)
  if (parsed.length) {
    parsed.forEach(add)
    return true
  }
  return false
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' && suggestions.value.length) {
    e.preventDefault()
    active.value = (active.value + 1) % suggestions.value.length
  } else if (e.key === 'ArrowUp' && suggestions.value.length) {
    e.preventDefault()
    active.value = (active.value - 1 + suggestions.value.length) % suggestions.value.length
  } else if (e.key === 'Enter' || e.key === 'Tab' || e.key === ',' || e.key === ';') {
    if (suggestions.value.length && e.key !== ',' && e.key !== ';') {
      e.preventDefault()
      add(suggestions.value[active.value])
    } else if (text.value.trim()) {
      if (commit() || e.key !== 'Tab') e.preventDefault()
    }
  } else if (e.key === 'Backspace' && !text.value && model.value.length) {
    model.value = model.value.slice(0, -1)
  } else if (e.key === 'Escape' && suggestions.value.length) {
    e.stopPropagation()
    text.value = ''
  }
}

function onPaste(e: ClipboardEvent) {
  const data = e.clipboardData?.getData('text') ?? ''
  if (/[,;\n]/.test(data)) {
    e.preventDefault()
    parseAddressList(data).forEach(add)
  }
}

function onBlur() {
  focused.value = false
  setTimeout(() => {
    if (!focused.value && isEmail(text.value.trim())) commit()
  }, 150)
}

defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <div class="relative flex min-h-9 flex-wrap items-center gap-1 py-1" @click="input?.focus()">
    <span class="text-muted-foreground w-10 shrink-0 text-[13px]">{{ props.label }}</span>
    <span
      v-for="a in model"
      :key="a.email"
      :class="['bg-muted inline-flex h-6 max-w-60 items-center gap-1 rounded-full pr-1 pl-2.5 text-xs', !isEmail(a.email) && 'bg-destructive/10 text-destructive']"
      :title="a.email"
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
