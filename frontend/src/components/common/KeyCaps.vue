<script setup lang="ts">
import { computed } from 'vue'
import { shortcutKeys } from '@/lib/platform'

const props = defineProps<{ combo: string; size?: 'sm' | 'md' }>()
const keys = computed(() => props.combo.split(' ').map((part) => shortcutKeys(part)))
</script>

<template>
  <span class="inline-flex items-center gap-1">
    <template v-for="(group, gi) in keys" :key="gi">
      <span v-if="gi > 0" class="text-muted-foreground text-[10px]">then</span>
      <span class="inline-flex items-center gap-0.5">
        <kbd
          v-for="k in group"
          :key="k"
          :class="[
            'bg-muted text-muted-foreground border-border inline-flex min-w-5 items-center justify-center rounded border px-1 font-sans font-medium',
            size === 'md' ? 'h-6 text-xs' : 'h-5 text-[11px]',
          ]"
        >{{ k }}</kbd>
      </span>
    </template>
  </span>
</template>
