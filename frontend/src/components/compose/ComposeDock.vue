<script setup lang="ts">
import { Maximize2Icon, XIcon } from '@lucide/vue'
import { useComposeStore } from '@/stores/compose'
import ComposeWindow from './ComposeWindow.vue'

const compose = useComposeStore()
</script>

<template>
  <!-- Gmail-style dock: open windows bottom-right, minimised ones as compact bars -->
  <div class="pointer-events-none fixed right-4 bottom-0 z-40 flex items-end gap-3">
    <template v-for="d in compose.drafts" :key="d.id">
      <div v-if="d.minimized" class="bg-background border-border pointer-events-auto flex h-10 w-64 items-center gap-1 rounded-t-lg border border-b-0 pr-1 pl-3 shadow-xl">
        <button type="button" class="min-w-0 flex-1 truncate text-left text-[13px] font-medium" @click="compose.focus(d.id)">
          {{ d.subject.trim() || 'New message' }}
        </button>
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded" aria-label="Open" @click="compose.focus(d.id)"><Maximize2Icon class="size-3.5" /></button>
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded" aria-label="Close" @click="compose.close(d.id)"><XIcon class="size-3.5" /></button>
      </div>
      <ComposeWindow v-else :draft="d" />
    </template>
  </div>
  <div v-if="compose.drafts.some((d) => d.expanded && !d.minimized)" class="fixed inset-0 z-40 bg-black/30" @click="compose.drafts.forEach((d) => (d.expanded = false))" />
</template>
