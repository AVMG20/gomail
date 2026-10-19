<script setup lang="ts">
import { computed } from 'vue'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { shortcutKeys } from '@/lib/platform'

const props = withDefaults(defineProps<{ label?: string; shortcut?: string; side?: 'top' | 'bottom' | 'left' | 'right'; disabled?: boolean }>(), {
  label: '', shortcut: '', side: 'bottom', disabled: false,
})
const keys = computed(() => (props.shortcut ? shortcutKeys(props.shortcut) : []))
</script>

<template>
  <Tooltip v-if="!disabled && (label || $slots.content)" :delay-duration="350">
    <TooltipTrigger as-child>
      <slot />
    </TooltipTrigger>
    <TooltipContent :side="side" :side-offset="6" class="px-2 py-1">
      <slot name="content">{{ label }}</slot>
      <span v-if="keys.length" class="ml-1 flex items-center gap-0.5 opacity-60">
        <kbd v-for="k in keys" :key="k" class="font-sans text-[10px] leading-none">{{ k }}</kbd>
      </span>
    </TooltipContent>
  </Tooltip>
  <slot v-else />
</template>
