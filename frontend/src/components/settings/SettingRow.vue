<script setup lang="ts">
defineProps<{ title: string; description?: string; stacked?: boolean }>()
</script>

<template>
  <div :class="['py-3.5', stacked ? 'space-y-2.5' : 'flex items-center justify-between gap-6']">
    <div class="min-w-0">
      <div class="text-[13px] font-medium">{{ title }}</div>
      <div v-if="description || $slots.description" class="text-muted-foreground mt-0.5 text-xs leading-relaxed"><slot name="description">{{ description }}</slot></div>
    </div>
    <div :class="stacked ? '' : 'shrink-0'"><slot /></div>
  </div>
</template>
