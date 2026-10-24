<script setup lang="ts">
import { Maximize2Icon, XIcon } from '@lucide/vue'
import { useComposeStore } from '@/stores/compose'
import ComposeWindow from './ComposeWindow.vue'

const compose = useComposeStore()
</script>

<template>
  <!-- Gmail-style dock: open windows bottom-right, minimised ones as compact bars -->
  <!-- data-compose-dock: Backspace/Delete pressed on a minimised tab never act on the open mail
       (navigation shortcuts still work after Esc minimises a window). -->
  <div class="pointer-events-none fixed right-4 bottom-0 z-40 flex items-end gap-3" data-compose-dock>
    <template v-for="d in compose.drafts.filter((x) => !x.queued)" :key="d.id">
      <div v-if="d.minimized" class="bg-background border-border pointer-events-auto flex h-10 w-64 items-center gap-1 rounded-t-lg border border-b-0 pr-1 pl-1.5 shadow-xl">
        <!-- data-parked: focused there after Esc (see compose.afterStepAway), so no ring until the user tabs to it -->
        <button
          type="button"
          class="hover:bg-muted focus-visible:ring-ring min-w-0 flex-1 truncate rounded-md px-1.5 py-0.5 text-left text-[13px] font-medium outline-none focus-visible:ring-2 data-parked:focus-visible:ring-0"
          :data-dock-tab="d.id"
          @click="compose.focus(d.id)"
          @blur="delete ($event.target as HTMLElement).dataset.parked"
        >
          {{ d.subject.trim() || 'New message' }}
        </button>
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded" aria-label="Open" @click="compose.focus(d.id)"><Maximize2Icon class="size-3.5" /></button>
        <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded" aria-label="Close" @click="compose.saveAndClose(d.id).then((ok) => !ok && compose.focus(d.id))"><XIcon class="size-3.5" /></button>
      </div>
      <ComposeWindow v-else :draft="d" />
    </template>
  </div>
  <div v-if="compose.drafts.some((d) => d.expanded && !d.minimized)" class="fixed inset-0 z-40 bg-black/30" @click="compose.drafts.forEach((d) => (d.expanded = false))" />
</template>
