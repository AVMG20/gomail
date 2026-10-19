<script setup lang="ts">
import { computed } from 'vue'
import { CheckIcon, MonitorIcon, MoonIcon, SunIcon } from '@lucide/vue'
import { Slider } from '@/components/ui/slider'
import { ACCENT_PRESETS, readableForeground } from '@/lib/color'
import { DEFAULT_PRIMARY } from '@/lib/theme'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const s = computed(() => settings.settings!)

const themes = [
  { value: 'system', label: 'System', icon: MonitorIcon },
  { value: 'light', label: 'Light', icon: SunIcon },
  { value: 'dark', label: 'Dark', icon: MoonIcon },
]
const isPreset = computed(() => ACCENT_PRESETS.some((p) => p.value.toLowerCase() === s.value.primaryColor.toLowerCase()))
const radiusLabel = computed(() => (s.value.radius === 0 ? 'Square' : s.value.radius <= 0.375 ? 'Subtle' : s.value.radius <= 0.75 ? 'Rounded' : 'Very rounded'))
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Theme">
      <div class="grid grid-cols-3 gap-3 py-3.5">
        <button
          v-for="t in themes"
          :key="t.value"
          type="button"
          :class="['group rounded-lg border p-1.5 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring', s.theme === t.value ? 'border-primary ring-primary/20 ring-2' : 'border-border hover:border-foreground/20']"
          @click="settings.update({ theme: t.value })"
        >
          <div :class="['flex h-16 overflow-hidden rounded-md border', t.value === 'dark' ? 'border-neutral-700 bg-neutral-900' : t.value === 'light' ? 'border-neutral-200 bg-white' : 'border-neutral-300 bg-gradient-to-r from-white from-50% to-neutral-900 to-50%']">
            <div :class="['w-1/4', t.value === 'dark' ? 'bg-neutral-800' : 'bg-neutral-100']" />
            <div class="flex flex-1 flex-col gap-1.5 p-2">
              <div class="h-1.5 w-3/4 rounded-full" :style="{ background: s.primaryColor }" />
              <div :class="['h-1.5 w-1/2 rounded-full', t.value === 'dark' ? 'bg-neutral-600' : 'bg-neutral-300']" />
              <div :class="['h-1.5 w-2/3 rounded-full', t.value === 'dark' ? 'bg-neutral-700' : 'bg-neutral-200']" />
            </div>
          </div>
          <div class="mt-1.5 flex items-center gap-1.5 px-0.5 text-xs font-medium"><component :is="t.icon" class="size-3.5" /> {{ t.label }}</div>
        </button>
      </div>
    </SettingSection>

    <SettingSection title="Accent color" description="Used for buttons, selection and focus rings.">
      <div class="flex flex-wrap items-center gap-2 py-3.5">
        <button
          v-for="p in ACCENT_PRESETS"
          :key="p.value"
          type="button"
          :title="p.name"
          :aria-label="p.name"
          class="flex size-8 items-center justify-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          :style="{ background: p.value, boxShadow: s.primaryColor.toLowerCase() === p.value ? `0 0 0 2px var(--background), 0 0 0 4px ${p.value}` : undefined }"
          @click="settings.update({ primaryColor: p.value })"
        >
          <CheckIcon v-if="s.primaryColor.toLowerCase() === p.value" class="size-4" :style="{ color: readableForeground(p.value) }" />
        </button>
        <label
          class="border-border relative flex size-8 cursor-pointer items-center justify-center overflow-hidden rounded-full border border-dashed"
          :style="!isPreset ? { background: s.primaryColor, boxShadow: `0 0 0 2px var(--background), 0 0 0 4px ${s.primaryColor}` } : { background: 'conic-gradient(red, yellow, lime, aqua, blue, magenta, red)' }"
          title="Custom color"
        >
          <input type="color" class="absolute inset-0 cursor-pointer opacity-0" :value="s.primaryColor" aria-label="Custom accent color" @input="(e) => settings.update({ primaryColor: (e.target as HTMLInputElement).value }, 500)" />
          <CheckIcon v-if="!isPreset" class="pointer-events-none size-4" :style="{ color: readableForeground(s.primaryColor) }" />
        </label>
        <button v-if="s.primaryColor.toLowerCase() !== DEFAULT_PRIMARY" type="button" class="text-muted-foreground hover:text-foreground ml-2 text-xs" @click="settings.update({ primaryColor: DEFAULT_PRIMARY })">Reset</button>
      </div>
    </SettingSection>

    <SettingSection title="Layout">
      <SettingRow title="Corner radius" :description="radiusLabel">
        <div class="flex w-56 items-center gap-3">
          <Slider :model-value="[s.radius]" :min="0" :max="1" :step="0.125" class="flex-1" @update:model-value="(v) => v && settings.update({ radius: v[0] })" />
          <div class="border-primary bg-primary/10 size-7 border-2" :style="{ borderRadius: `${s.radius}rem` }" />
        </div>
      </SettingRow>
      <SettingRow title="Density" description="Compact shows one line per message.">
        <div class="bg-muted inline-flex rounded-lg p-0.5">
          <button
            v-for="d in ['comfortable', 'compact']"
            :key="d"
            type="button"
            :aria-pressed="s.density === d"
            :class="['rounded-md px-3 py-1 text-xs font-medium capitalize transition-colors', s.density === d ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']"
            @click="settings.update({ density: d })"
          >{{ d }}</button>
        </div>
      </SettingRow>
      <SettingRow title="Email background" description="In dark mode, Auto shows plain and simple emails in dark colors. Designed emails always keep a light background.">
        <div class="bg-muted inline-flex rounded-lg p-0.5">
          <button
            v-for="o in [{ v: 'auto', l: 'Auto' }, { v: 'light', l: 'Always light' }]"
            :key="o.v"
            type="button"
            :aria-pressed="(s.emailBackground ?? 'auto') === o.v"
            :class="['rounded-md px-3 py-1 text-xs font-medium whitespace-nowrap transition-colors', (s.emailBackground ?? 'auto') === o.v ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']"
            @click="settings.update({ emailBackground: o.v as 'auto' | 'light' })"
          >{{ o.l }}</button>
        </div>
      </SettingRow>
    </SettingSection>
  </div>
</template>
