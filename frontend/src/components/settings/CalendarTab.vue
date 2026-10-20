<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { DURATION_CHOICES, formatReminder, localeWeekStart, REMINDER_CHOICES } from '@/lib/calendar'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { Button } from '@/components/ui/button'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const accounts = useAccountsStore()
const cal = useCalendarStore()
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
    <SettingSection title="Mailboxes" description="A disabled calendar doesn’t sync and sends no reminders; turning reminders off keeps its events visible.">
      <div v-for="a in accounts.accounts" :key="a.id" class="flex items-center gap-3 py-2">
        <AccountAvatar :account="a" :size="22" :glyph="false" />
        <span class="min-w-0 flex-1 truncate text-sm" :title="a.email">{{ a.email }}</span>
        <Button v-if="a.calendarConsent === false" variant="outline" size="sm" :disabled="cal.connecting === a.id" @click="cal.connect(a.id)">Connect</Button>
        <template v-else>
          <label class="text-muted-foreground flex items-center gap-2 text-xs">
            Enabled
            <Switch :model-value="a.calendarEnabled !== false" :aria-label="`Calendar enabled for ${a.email}`" @update:model-value="(v: boolean) => cal.setAccountEnabled(a.id, v, false)" />
          </label>
          <label class="text-muted-foreground flex items-center gap-2 text-xs">
            Reminders
            <Switch
              :model-value="!a.calendarMuted"
              :disabled="a.calendarEnabled === false"
              :aria-label="`Reminders for ${a.email}`"
              @update:model-value="(v: boolean) => cal.setAccountMuted(a.id, !v)"
            />
          </label>
        </template>
      </div>
    </SettingSection>
  </div>
</template>
