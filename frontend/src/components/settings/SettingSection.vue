<script setup lang="ts">
import { useId } from 'vue'

// allMailboxes: the section's settings apply to every mailbox (shown as a small tag),
// so it's clear they aren't set per mailbox.
defineProps<{ title: string; description?: string; allMailboxes?: boolean }>()
const id = useId()
</script>

<template>
  <section class="mb-8" :aria-labelledby="`${id}-h`">
    <div class="mb-1">
      <div class="flex items-center gap-2">
        <h3 :id="`${id}-h`" class="text-sm font-semibold">{{ title }}</h3>
        <span v-if="allMailboxes" class="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-[11px] font-medium">All mailboxes</span>
        <slot name="actions" />
      </div>
      <p v-if="description" class="text-muted-foreground mt-0.5 text-xs leading-relaxed">{{ description }}</p>
    </div>
    <div class="divide-border divide-y">
      <slot />
    </div>
  </section>
</template>
