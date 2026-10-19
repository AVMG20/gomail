<script setup lang="ts">
import { computed } from 'vue'
import { colorForString, initials } from '@/lib/format'
import type { Address } from '@/lib/types'

const props = withDefaults(defineProps<{ address: Address; size?: number }>(), { size: 32 })
const text = computed(() => initials(props.address.name || props.address.email))
const bg = computed(() => colorForString(props.address.email || props.address.name))
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center justify-center rounded-full font-medium text-white select-none"
    :style="{ width: `${size}px`, height: `${size}px`, background: bg, fontSize: `${Math.round(size * 0.38)}px` }"
  >{{ text }}</span>
</template>
