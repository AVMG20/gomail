<script setup lang="ts">
import { computed, ref, type Component } from 'vue'
import { InfoIcon, PaletteIcon, SlidersHorizontalIcon, SparklesIcon, TagIcon, UsersIcon, XIcon, CheckIcon, LoaderIcon } from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore, type SettingsTab } from '@/stores/ui'
import AboutTab from './AboutTab.vue'
import AccountsTab from './AccountsTab.vue'
import AiTab from './AiTab.vue'
import AppearanceTab from './AppearanceTab.vue'
import GeneralTab from './GeneralTab.vue'
import LabelsTab from './LabelsTab.vue'
import ErrorBoundary from '@/components/common/ErrorBoundary.vue'

const ui = useUiStore()
const settings = useSettingsStore()

const tabs: { id: SettingsTab; label: string; icon: Component; comp: Component }[] = [
  { id: 'general', label: 'General', icon: SlidersHorizontalIcon, comp: GeneralTab },
  { id: 'appearance', label: 'Appearance', icon: PaletteIcon, comp: AppearanceTab },
  { id: 'accounts', label: 'Accounts', icon: UsersIcon, comp: AccountsTab },
  { id: 'labels', label: 'Labels', icon: TagIcon, comp: LabelsTab },
  { id: 'ai', label: 'AI', icon: SparklesIcon, comp: AiTab },
  { id: 'about', label: 'About', icon: InfoIcon, comp: AboutTab },
]
const scrolled = ref(false)
const current = computed(() => tabs.find((t) => t.id === ui.settingsTab) ?? tabs[0])
</script>

<template>
  <Dialog v-model:open="ui.settingsOpen">
    <DialogContent class="flex h-[min(720px,calc(100vh-2.5rem))] max-h-[calc(100vh-2.5rem)] gap-0 overflow-hidden p-0 sm:max-w-[880px]" :show-close-button="false">
      <DialogTitle class="sr-only">Settings</DialogTitle>
      <DialogDescription class="sr-only">Configure gomail</DialogDescription>
      <nav class="bg-sidebar border-border flex w-48 shrink-0 flex-col gap-0.5 border-r p-3" aria-label="Settings sections">
        <div class="text-muted-foreground px-2 pt-1 pb-3 text-xs font-medium">Settings</div>
        <button
          v-for="t in tabs"
          :key="t.id"
          type="button"
          :class="['flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring', ui.settingsTab === t.id ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/80 hover:bg-sidebar-accent/60']"
          @click="ui.settingsTab = t.id"
        >
          <component :is="t.icon" class="size-4" /> {{ t.label }}
        </button>
        <div class="flex-1" />
        <div class="text-muted-foreground flex h-6 items-center gap-1.5 px-2 text-[11px]">
          <template v-if="settings.saving"><LoaderIcon class="size-3 animate-spin" /> Saving…</template>
          <template v-else><CheckIcon class="size-3" /> Changes save automatically</template>
        </div>
      </nav>
      <div class="flex min-w-0 flex-1 flex-col">
        <header :class="['flex h-14 shrink-0 items-center justify-between border-b px-8 transition-colors', scrolled ? 'border-border' : 'border-transparent']">
          <h2 class="text-base font-semibold">{{ current.label }}</h2>
          <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted rounded-md p-1.5" aria-label="Close settings" @click="ui.settingsOpen = false"><XIcon class="size-4" /></button>
        </header>
        <div class="thin-scroll min-h-0 flex-1 overflow-y-auto px-8 pt-2 pb-10" @scroll.passive="(e: Event) => (scrolled = (e.target as HTMLElement).scrollTop > 4)">
          <ErrorBoundary name="settings page" :reset-key="current.id"><KeepAlive><component :is="current.comp" :key="current.id" /></KeepAlive></ErrorBoundary>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
