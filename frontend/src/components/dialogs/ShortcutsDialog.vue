<script setup lang="ts">
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import KeyCaps from '@/components/common/KeyCaps.vue'
import { useUiStore } from '@/stores/ui'
import { SHORTCUT_GROUPS } from './shortcuts'

const ui = useUiStore()
</script>

<template>
  <Dialog v-model:open="ui.shortcutsOpen">
    <DialogContent class="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-[min(960px,calc(100vw-2rem))]">
      <DialogHeader>
        <DialogTitle>Keyboard shortcuts</DialogTitle>
        <DialogDescription>Single-key shortcuts work whenever you are not typing.</DialogDescription>
      </DialogHeader>
      <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        <section v-for="g in SHORTCUT_GROUPS" :key="g.title">
          <h3 class="text-muted-foreground mb-2 text-[11px] font-medium tracking-wide uppercase">{{ g.title }}</h3>
          <ul class="space-y-1.5">
            <li v-for="s in g.items" :key="s.keys" class="flex items-center justify-between gap-3 text-[13px]">
              <span>{{ s.label }}</span>
              <KeyCaps :combo="s.keys" />
            </li>
          </ul>
        </section>
      </div>
    </DialogContent>
  </Dialog>
</template>
