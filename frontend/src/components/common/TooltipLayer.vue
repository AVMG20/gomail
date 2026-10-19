<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { shortcutKeys } from '@/lib/platform'

const visible = ref(false)
const text = ref('')
const keys = ref<string[]>([])
const pos = ref({ x: 0, y: 0 })
const bubble = ref<HTMLElement | null>(null)

let current: HTMLElement | null = null
let showTimer: ReturnType<typeof setTimeout> | null = null
let lastHidden = 0
let watchdog: ReturnType<typeof setInterval> | null = null

function place(el: HTMLElement) {
  const r = el.getBoundingClientRect()
  const side = el.dataset.tipSide ?? 'bottom'
  const b = bubble.value
  const w = b?.offsetWidth ?? 0
  const h = b?.offsetHeight ?? 0
  let x = r.left + r.width / 2 - w / 2
  let y = r.bottom + 6
  if (side === 'top') y = r.top - h - 6
  else if (side === 'right') { x = r.right + 8; y = r.top + r.height / 2 - h / 2 }
  else if (side === 'left') { x = r.left - w - 8; y = r.top + r.height / 2 - h / 2 }
  if (side === 'bottom' && y + h > window.innerHeight - 4) y = r.top - h - 6
  x = Math.max(6, Math.min(window.innerWidth - w - 6, x))
  y = Math.max(6, Math.min(window.innerHeight - h - 6, y))
  pos.value = { x, y }
}

async function show(el: HTMLElement) {
  current = el
  text.value = el.dataset.tip ?? ''
  keys.value = el.dataset.tipKeys ? shortcutKeys(el.dataset.tipKeys) : []
  visible.value = true
  await nextTick()
  if (current === el) place(el)
  watchdog ??= setInterval(() => { if (current && !current.isConnected) hide() }, 400)
}

function hide() {
  if (showTimer) clearTimeout(showTimer)
  showTimer = null
  if (visible.value) lastHidden = Date.now()
  visible.value = false
  current = null
  if (watchdog) { clearInterval(watchdog); watchdog = null }
}

function schedule(el: HTMLElement) {
  if (showTimer) clearTimeout(showTimer)
  const warm = visible.value || Date.now() - lastHidden < 400
  showTimer = setTimeout(() => show(el), warm ? 0 : 450)
}

function onOver(e: PointerEvent) {
  if (e.pointerType === 'touch') return
  const el = (e.target as Element | null)?.closest?.('[data-tip]') as HTMLElement | null
  if (!el) return
  if (el === current) return
  if ((el as HTMLButtonElement).disabled && !el.dataset.tip) return
  schedule(el)
}
function onOut(e: PointerEvent) {
  const to = e.relatedTarget as Node | null
  const el = (e.target as Element | null)?.closest?.('[data-tip]')
  if (!el || (to && el.contains(to))) return
  hide()
}
function onFocus(e: FocusEvent) {
  const el = (e.target as Element | null)?.closest?.('[data-tip]') as HTMLElement | null
  if (el && el.matches(':focus-visible')) void show(el)
}

const keyOpts = { capture: true, passive: true } as const
onMounted(() => {
  document.addEventListener('pointerover', onOver, keyOpts)
  document.addEventListener('pointerout', onOut, keyOpts)
  document.addEventListener('focusin', onFocus, keyOpts)
  document.addEventListener('focusout', hide, keyOpts)
  document.addEventListener('pointerdown', hide, keyOpts)
  document.addEventListener('keydown', hide, keyOpts)
  document.addEventListener('scroll', hide, keyOpts)
  window.addEventListener('blur', hide)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerover', onOver, keyOpts)
  document.removeEventListener('pointerout', onOut, keyOpts)
  document.removeEventListener('focusin', onFocus, keyOpts)
  document.removeEventListener('focusout', hide, keyOpts)
  document.removeEventListener('pointerdown', hide, keyOpts)
  document.removeEventListener('keydown', hide, keyOpts)
  document.removeEventListener('scroll', hide, keyOpts)
  window.removeEventListener('blur', hide)
  hide()
})

const style = computed(() => ({ transform: `translate(${Math.round(pos.value.x)}px, ${Math.round(pos.value.y)}px)` }))
</script>

<template>
  <div
    v-show="visible"
    ref="bubble"
    role="tooltip"
    class="bg-foreground text-background pointer-events-none fixed top-0 left-0 z-[100] flex max-w-xs items-center gap-1.5 rounded-md px-2 py-1 text-xs whitespace-pre-line shadow-md"
    :style="style"
  >
    <span>{{ text }}</span>
    <span v-if="keys.length" class="flex items-center gap-0.5 opacity-60">
      <kbd v-for="k in keys" :key="k" class="font-sans text-[10px] leading-none">{{ k }}</kbd>
    </span>
  </div>
</template>
