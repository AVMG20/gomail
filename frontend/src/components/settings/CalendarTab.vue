<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { DURATION_CHOICES, formatReminder, localeWeekStart, REMINDER_CHOICES } from '@/lib/calendar'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const s = computed(() => settings.settings!)
const locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'
const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const localeDay = DAY_NAMES[localeWeekStart(locale)]
const duration = (m: number) => (m < 60 ? `${m} minutes` : m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m / 60} hour${m === 60 ? '' : 's'}`)
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="View">
      <SettingRow title="Week starts on" :description="`Follows your language and region by default (${localeDay}).`">
        <Select :model-value="s.calendarWeekStart || 'auto'" @update:model-value="(v) => settings.update({ calendarWeekStart: String(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">Automatic ({{ localeDay }})</SelectItem>
            <SelectItem value="monday">Monday</SelectItem>
            <SelectItem value="sunday">Sunday</SelectItem>
            <SelectItem value="saturday">Saturday</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Default view" description="What the calendar shows when you open it.">
        <Select :model-value="s.calendarDefaultView ?? 'week'" @update:model-value="(v) => settings.update({ calendarDefaultView: v as 'week' })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="day">Day</SelectItem>
            <SelectItem value="week">Week</SelectItem>
            <SelectItem value="month">Month</SelectItem>
            <SelectItem value="agenda">Agenda</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Show declined events" description="Declined events appear faded and struck through.">
        <Switch :model-value="!!s.calendarShowDeclined" @update:model-value="(v: boolean) => settings.update({ calendarShowDeclined: v })" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="New events">
      <SettingRow title="Default duration">
        <Select :model-value="String(s.calendarDefaultDuration ?? 30)" @update:model-value="(v) => settings.update({ calendarDefaultDuration: Number(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="m in DURATION_CHOICES" :key="m" :value="String(m)">{{ duration(m) }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Default reminder" description="Used for events without reminders of their own.">
        <Select :model-value="String(s.calendarDefaultReminder ?? 10)" @update:model-value="(v) => settings.update({ calendarDefaultReminder: Number(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="m in REMINDER_CHOICES" :key="m" :value="String(m)">{{ formatReminder(m) }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Notifications">
      <SettingRow title="Event reminders" description="Show a desktop notification before events start.">
        <Switch :model-value="s.calendarNotifications !== false" @update:model-value="(v: boolean) => settings.update({ calendarNotifications: v })" />
      </SettingRow>
    </SettingSection>
  </div>
</template>
