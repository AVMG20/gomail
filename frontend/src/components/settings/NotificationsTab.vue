<script setup lang="ts">
// Settings → Notifications: the switches for all mailboxes, then every mailbox at a glance.
// The per-mailbox values are also on each mailbox's own page.
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { hasMethod } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import QuietLabels from './QuietLabels.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const accounts = useAccountsStore()
const ui = useUiStore()
const hasCalendar = hasMethod('ListCalendars')
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="New mail">
      <SettingRow title="Desktop notifications" description="Off silences every mailbox.">
        <Switch :model-value="settings.settings.notifications" @update:model-value="(v: boolean) => settings.update({ notifications: v })" />
      </SettingRow>
      <QuietLabels v-if="settings.settings.notifications && accounts.accounts.length" />
    </SettingSection>

    <SettingSection v-if="hasCalendar" title="Calendar">
      <SettingRow title="Event reminders" description="Before events start. Off silences every mailbox.">
        <Switch :model-value="settings.settings.calendarNotifications !== false" @update:model-value="(v: boolean) => settings.update({ calendarNotifications: v })" />
      </SettingRow>
      <SettingRow v-if="settings.settings.calendarNotifications !== false && accounts.accounts.length" title="Per mailbox" description="Mute the reminders of one mailbox’s calendar.">
        <Button size="sm" variant="outline" @click="ui.settingsTab = 'calendar'">Calendar settings</Button>
      </SettingRow>
    </SettingSection>
  </div>
</template>
