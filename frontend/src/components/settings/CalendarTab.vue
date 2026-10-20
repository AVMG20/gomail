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
import { useUiStore } from '@/stores/ui'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const accounts = useAccountsStore()
const cal = useCalendarStore()
const ui = useUiStore()
const s = computed(() => settings.settings!)
const locale = typeof navigator !== 'undefined' ? navigator.language : 'en-US'
const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const localeDay = DAY_NAMES[localeWeekStart(locale)]
const duration = (m: number) => (m < 60 ? `${m} minutes` : m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m / 60} hour${m === 60 ? '' : 's'}`)
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="View" all-mailboxes>
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
    <SettingSection title="New events" all-mailboxes>
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
    <SettingSection title="Per mailbox" description="Hidden calendars don’t sync. Muted ones still show their events.">
      <p v-if="s.calendarNotifications === false" class="text-muted-foreground pb-2 text-xs">
        Event reminders are off for all mailboxes.
        <button type="button" class="text-foreground underline-offset-2 hover:underline" @click="ui.settingsTab = 'notifications'">Turn them on in Notifications</button>
      </p>
      <p v-if="!accounts.accounts.length" class="text-muted-foreground py-3.5 text-xs">Add an account to see its calendar here.</p>
      <div v-for="a in accounts.accounts" :key="a.id" class="flex items-center gap-3 py-2.5">
        <AccountAvatar :account="a" :size="22" :glyph="false" />
        <button type="button" class="hover:text-foreground min-w-0 flex-1 truncate text-left text-[13px] underline-offset-2 hover:underline" :title="`Open the settings of ${a.email}`" @click="ui.settingsTab = `mailbox:${a.id}`">{{ a.email }}</button>
        <Button v-if="a.calendarConsent === false" variant="outline" size="sm" :disabled="cal.connecting === a.id" @click="cal.connect(a.id)">Connect</Button>
        <template v-else>
          <label class="text-muted-foreground flex items-center gap-2 text-xs">
            Show
            <Switch :model-value="a.calendarEnabled !== false" :aria-label="`Show the calendar of ${a.email}`" @update:model-value="(v: boolean) => cal.setAccountEnabled(a.id, v, false)" />
          </label>
          <label class="text-muted-foreground flex items-center gap-2 text-xs">
            Reminders
            <Switch
              :model-value="!a.calendarMuted && s.calendarNotifications !== false"
              :disabled="a.calendarEnabled === false || s.calendarNotifications === false"
              :aria-label="`Reminders for ${a.email}`"
              @update:model-value="(v: boolean) => cal.setAccountMuted(a.id, !v)"
            />
          </label>
        </template>
      </div>
    </SettingSection>
  </div>
</template>
