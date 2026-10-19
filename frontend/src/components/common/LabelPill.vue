<script setup lang="ts">
import { computed } from 'vue'
import { SparklesIcon, XIcon } from '@lucide/vue'
import { cn } from 'cn'
import type { Label, MessageLabel } from '@/lib/types'
import Tip from './Tip.vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ label: Label; detail?: MessageLabel; removable?: boolean; size?: 'xs' | 'sm'; class?: string }>(), {
  removable: false, size: 'xs',
})
defineEmits<{ remove: [] }>()

const isAi = computed(() => props.detail?.source === 'ai')
const tooltip = computed(() => {
  const parts = [props.label.name]
  if (isAi.value) parts.push(`Labeled by AI · ${Math.round((props.detail?.confidence ?? 0) * 100)}% confidence`)
  else if (props.label.source === 'system') parts.push('Built-in label')
  else if (props.label.source === 'provider') parts.push(props.label.provider ? `Imported from ${props.label.provider === 'outlook' ? 'Outlook' : 'Gmail'}` : 'Imported from your provider')
  if (props.detail?.source === 'provider' && !isAi.value) parts.push('Set in Gmail/Outlook')
  else if (props.detail?.source === 'manual') parts.push('Added by you')
  return parts.join(' — ')
})
</script>

<template>
  <Tip :label="tooltip">
    <span
      :class="cn(
        'gm-pill inline-flex max-w-36 shrink-0 items-center gap-1 rounded-full border font-medium whitespace-nowrap',
        size === 'xs' ? 'h-[18px] px-1.5 text-[10.5px]' : 'h-6 px-2 text-xs',
        props.class,
      )"
      :style="{ '--pill': label.color }"
    >
      <span v-if="!isAi" class="size-1.5 shrink-0 rounded-full" :style="{ background: label.color }" />
      <SparklesIcon v-else class="size-2.5 shrink-0" />
      <span class="truncate">{{ label.name }}</span>
      <button v-if="removable" type="button" class="-mr-0.5 rounded-full opacity-60 hover:opacity-100" :aria-label="`Remove ${label.name}`" @click.stop="$emit('remove')">
        <XIcon class="size-3" />
      </button>
    </span>
  </Tip>
</template>
