<script setup lang="ts">
// The color grid used by the calendar sidebar's menus (mailbox and calendar colors).
import { CheckIcon, RotateCcwIcon } from '@lucide/vue'
import { LABEL_COLORS } from '@/lib/color'

defineProps<{ current: string; resetLabel?: string }>()
const emit = defineEmits<{ pick: [color: string] }>()
</script>

<template>
  <div class="p-1.5">
    <div class="grid grid-cols-5 gap-1.5">
      <button
        v-for="c in LABEL_COLORS"
        :key="c"
        type="button"
        class="flex size-6 items-center justify-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :style="{ background: c }"
        :aria-label="`Color ${c}`"
        @click="emit('pick', c)"
      ><CheckIcon v-if="current.toLowerCase() === c.toLowerCase()" class="size-3.5 text-white" /></button>
    </div>
    <button
      v-if="resetLabel"
      type="button"
      class="text-muted-foreground hover:text-foreground hover:bg-muted mt-1.5 flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-xs"
      @click="emit('pick', '')"
    ><RotateCcwIcon class="size-3" /> {{ resetLabel }}</button>
  </div>
</template>
