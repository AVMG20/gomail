<script setup lang="ts">
import { computed } from 'vue'
import { initials } from '@/lib/format'
import type { Account } from '@/lib/types'
import ProviderIcon from './ProviderIcon.vue'

const props = withDefaults(defineProps<{ account: Account; size?: number; glyph?: boolean; active?: boolean }>(), {
  size: 36, glyph: true, active: false,
})
const label = computed(() => initials(props.account.displayName, props.account.email))
</script>

<template>
  <span class="relative inline-flex shrink-0" aria-hidden="true" :style="{ width: `${size}px`, height: `${size}px` }">
    <span
      class="flex size-full items-center justify-center rounded-full font-semibold text-white shadow-sm select-none"
      :style="{
        background: `linear-gradient(140deg, color-mix(in oklab, ${account.color} 88%, white), ${account.color})`,
        fontSize: `${Math.round(size * 0.36)}px`,
        boxShadow: active ? `0 0 0 2px var(--sidebar), 0 0 0 4px ${account.color}` : undefined,
      }"
    >{{ label }}</span>
    <span
      v-if="glyph"
      class="bg-background absolute -right-0.5 -bottom-0.5 flex items-center justify-center rounded-full p-[2px] shadow-sm"
      :style="{ width: `${Math.round(size * 0.46)}px`, height: `${Math.round(size * 0.46)}px` }"
    >
      <ProviderIcon :provider="account.provider" class="size-full" />
    </span>
  </span>
</template>
