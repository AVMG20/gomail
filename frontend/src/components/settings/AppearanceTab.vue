<script setup lang="ts">
import { computed } from 'vue'
import { CheckIcon, MonitorIcon, MoonIcon, SunIcon } from '@lucide/vue'
import { Slider } from '@/components/ui/slider'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { localeUses12h } from '@/lib/calendar'
import { ACCENT_PRESETS, readableForeground } from '@/lib/color'
import { shortcutLabel } from '@/lib/platform'
import { DEFAULT_PRIMARY } from '@/lib/theme'
import { useSettingsStore } from '@/stores/settings'
import SegmentedControl from './SegmentedControl.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const s = computed(() => settings.settings!)
const localeClock = localeUses12h() ? '12-hour' : '24-hour'

const themes = [
  { value: 'system', label: 'System', icon: MonitorIcon },
  { value: 'light', label: 'Light', icon: SunIcon },
  { value: 'dark', label: 'Dark', icon: MoonIcon },
]
const isPreset = computed(() => ACCENT_PRESETS.some((p) => p.value.toLowerCase() === s.value.primaryColor.toLowerCase()))
/** One line per email background choice; "All mail dark" flips designed mail's colors (experimental). */
const EMAIL_BACKGROUND_HINT: Record<string, string> = {
  light: 'Every email is shown on a light background.',
  auto: 'Plain mail is shown in dark colors. Designed mail stays light.',
  dark: 'Experimental: designed mail is shown dark too, by flipping its colors. Photos and logos keep theirs. Some mail may look off; switch back if so.',
}
const radiusLabel = computed(() => (s.value.radius === 0 ? 'Square' : s.value.radius <= 0.375 ? 'Subtle' : s.value.radius <= 0.75 ? 'Rounded' : 'Very rounded'))

/** Theme cards are a radio group: one tab stop, arrow keys pick the next theme. */
function onThemeKey(e: KeyboardEvent) {
  const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const i = Math.max(0, themes.findIndex((t) => t.value === s.value.theme))
  const j = (i + step + themes.length) % themes.length
  void settings.update({ theme: themes[j].value })
  ;(e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>('[role="radio"]')[j]?.focus()
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Theme">
      <div class="grid grid-cols-3 gap-3 py-3.5" role="radiogroup" aria-label="Theme" @keydown="onThemeKey">
        <button
          v-for="t in themes"
          :key="t.value"
          type="button"
          role="radio"
          :aria-checked="s.theme === t.value"
          :tabindex="s.theme === t.value || (!themes.some((x) => x.value === s.theme) && t.value === 'system') ? 0 : -1"
          :class="['group rounded-lg border p-1.5 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring', s.theme === t.value ? 'border-primary ring-primary/20 ring-2' : 'border-border hover:border-foreground/20']"
          @click="settings.update({ theme: t.value })"
        >
          <div aria-hidden="true" :class="['flex h-16 overflow-hidden rounded-md border', t.value === 'dark' ? 'border-neutral-700 bg-neutral-900' : t.value === 'light' ? 'border-neutral-200 bg-white' : 'border-neutral-300 bg-gradient-to-r from-white from-50% to-neutral-900 to-50%']">
            <div :class="['w-1/4', t.value === 'dark' ? 'bg-neutral-800' : 'bg-neutral-100']" />
            <div class="flex flex-1 flex-col gap-1.5 p-2">
              <div class="h-1.5 w-3/4 rounded-full" :style="{ background: s.primaryColor }" />
              <div :class="['h-1.5 w-1/2 rounded-full', t.value === 'dark' ? 'bg-neutral-600' : 'bg-neutral-300']" />
              <div :class="['h-1.5 w-2/3 rounded-full', t.value === 'dark' ? 'bg-neutral-700' : 'bg-neutral-200']" />
            </div>
          </div>
          <div class="mt-1.5 flex items-center gap-1.5 px-0.5 text-xs font-medium"><component :is="t.icon" class="size-3.5" aria-hidden="true" /> {{ t.label }}</div>
        </button>
      </div>
    </SettingSection>

    <SettingSection title="Accent color" description="Used for buttons, selection and focus rings.">
      <div class="flex flex-wrap items-center gap-2 py-3.5" role="group" aria-label="Accent color">
        <button
          v-for="p in ACCENT_PRESETS"
          :key="p.value"
          type="button"
          :title="p.name"
          :aria-label="p.name"
          :aria-pressed="s.primaryColor.toLowerCase() === p.value"
          class="flex size-8 items-center justify-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          :style="{ background: p.value, boxShadow: s.primaryColor.toLowerCase() === p.value ? `0 0 0 2px var(--background), 0 0 0 4px ${p.value}` : undefined }"
          @click="settings.update({ primaryColor: p.value })"
        >
          <CheckIcon v-if="s.primaryColor.toLowerCase() === p.value" class="size-4" :style="{ color: readableForeground(p.value) }" />
        </button>
        <label
          class="border-border focus-within:ring-ring relative flex size-8 cursor-pointer items-center justify-center overflow-hidden rounded-full border border-dashed focus-within:ring-2 focus-within:ring-offset-2"
          :style="!isPreset ? { background: s.primaryColor, boxShadow: `0 0 0 2px var(--background), 0 0 0 4px ${s.primaryColor}` } : { background: 'conic-gradient(red, yellow, lime, aqua, blue, magenta, red)' }"
          title="Custom color"
        >
          <input type="color" class="absolute inset-0 cursor-pointer opacity-0" :value="s.primaryColor" aria-label="Custom accent color" @input="(e) => settings.update({ primaryColor: (e.target as HTMLInputElement).value }, 500)" />
          <CheckIcon v-if="!isPreset" class="pointer-events-none size-4" :style="{ color: readableForeground(s.primaryColor) }" />
        </label>
        <button v-if="s.primaryColor.toLowerCase() !== DEFAULT_PRIMARY" type="button" class="text-muted-foreground hover:text-foreground ml-2 text-xs" aria-label="Reset accent color" @click="settings.update({ primaryColor: DEFAULT_PRIMARY })">Reset</button>
      </div>
    </SettingSection>

    <SettingSection title="Layout">
      <SettingRow title="Reading pane" description="Off: the list uses the full width and mail opens in its place.">
        <SegmentedControl
          :model-value="settings.settings.readingPane ?? 'right'"
          :options="[{ value: 'right', label: 'Right' }, { value: 'off', label: 'Off' }]"
          @update:model-value="(v) => settings.update({ readingPane: v as 'right' | 'off' })"
        />
      </SettingRow>
      <SettingRow title="Density" description="Compact shows one line per message.">
        <SegmentedControl
          :model-value="s.density"
          :options="[{ value: 'comfortable', label: 'Comfortable' }, { value: 'compact', label: 'Compact' }]"
          @update:model-value="(v) => settings.update({ density: v })"
        />
      </SettingRow>
      <SettingRow title="Corner radius" :description="radiusLabel">
        <div class="flex w-56 items-center gap-3">
          <Slider :model-value="[s.radius]" :min="0" :max="1" :step="0.125" class="flex-1" @update:model-value="(v) => v && settings.update({ radius: v[0] })" />
          <div class="border-primary bg-primary/10 size-7 border-2" aria-hidden="true" :style="{ borderRadius: `${s.radius}rem` }" />
        </div>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Text & time">
      <SettingRow title="Text size" :description="`${Math.round((s.zoom ?? 1) * 100)}% · ${shortcutLabel('mod+=')} / ${shortcutLabel('mod+-')} / ${shortcutLabel('mod+0')}`">
        <div class="flex w-56 items-center gap-3">
          <Slider :model-value="[s.zoom ?? 1]" :min="0.9" :max="1.25" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ zoom: Math.round(v[0] * 100) / 100 })" />
          <button type="button" class="text-muted-foreground hover:text-foreground text-xs" aria-label="Reset text size" @click="settings.update({ zoom: 1 })">Reset</button>
        </div>
      </SettingRow>
      <SettingRow title="Time format" :description="`For mail and calendar. Automatic follows your language and region (${localeClock}).`">
        <Select :model-value="s.calendarTimeFormat || 'auto'" @update:model-value="(v) => settings.update({ calendarTimeFormat: String(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">Automatic ({{ localeClock }})</SelectItem>
            <SelectItem value="12h">12-hour (1:30 PM)</SelectItem>
            <SelectItem value="24h">24-hour (13:30)</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Email background in dark mode" :description="EMAIL_BACKGROUND_HINT[s.emailBackground ?? 'auto']" stacked>
        <SegmentedControl
          :model-value="s.emailBackground ?? 'auto'"
          :options="[{ value: 'light', label: 'Always light' }, { value: 'auto', label: 'Plain mail dark' }, { value: 'dark', label: 'All mail dark' }]"
          @update:model-value="(v) => settings.update({ emailBackground: v as 'auto' | 'light' | 'dark' })"
        />
      </SettingRow>
    </SettingSection>
  </div>
</template>
