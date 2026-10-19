<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <section class="mb-8">
    <div class="mb-1">
      <h3 class="text-sm font-semibold">{{ title }}</h3>
      <p v-if="description" class="text-muted-foreground mt-0.5 text-xs">{{ description }}</p>
    </div>
    <div class="divide-border divide-y">
      <slot />
    </div>
  </section>
</template>
