<script setup lang="ts">
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useSettingsStore } from '@/stores/settings'
import { useMailStore } from '@/stores/mail'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const intervals = [1, 5, 15, 30, 60]
const mail = useMailStore()
async function setConversation(v: boolean) {
  await settings.update({ conversationView: v }, 0)
  void mail.reload()
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Reading">
      <SettingRow title="Group messages into conversations" description="Show one row per conversation instead of one per message.">
        <Switch :model-value="settings.settings.conversationView ?? true" @update:model-value="(v: boolean) => setConversation(v)" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="Sync">
      <SettingRow title="Check for new mail" description="How often gomail syncs in the background.">
        <Select :model-value="String(settings.settings.syncIntervalMinutes)" @update:model-value="(v) => settings.update({ syncIntervalMinutes: Number(v) })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="i in intervals" :key="i" :value="String(i)">Every {{ i === 1 ? 'minute' : i === 60 ? 'hour' : `${i} minutes` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Notifications">
      <SettingRow title="Desktop notifications for new mail" description="Show a system notification when new mail arrives.">
        <Switch :model-value="settings.settings.notifications" @update:model-value="(v: boolean) => settings.update({ notifications: v })" />
      </SettingRow>
      <SettingRow title="Show in system tray" description="Keep gomail in the tray / menu bar with an unread badge.">
        <Switch :model-value="settings.settings.showInTray ?? true" @update:model-value="(v: boolean) => settings.update({ showInTray: v })" />
      </SettingRow>
      <SettingRow v-if="settings.settings.showInTray ?? true" title="Keep running when the window is closed" description="Closing the window hides gomail to the tray so new mail still arrives.">
        <Switch :model-value="settings.settings.closeToTray ?? false" @update:model-value="(v: boolean) => settings.update({ closeToTray: v })" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="Sending">
      <SettingRow title="Undo send" description="Wait a few seconds before sending so you can take it back.">
        <Select :model-value="String(settings.settings.undoSendSeconds ?? 5)" @update:model-value="(v) => settings.update({ undoSendSeconds: Number(v) })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="n in [0, 5, 10, 20]" :key="n" :value="String(n)">{{ n === 0 ? 'Off' : `${n} seconds` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Privacy">
      <SettingRow title="Load remote images automatically" description="Remote images can reveal when and where you open a message. When off, you can still load them per message.">
        <Switch :model-value="settings.settings.showRemoteImages" @update:model-value="(v: boolean) => settings.update({ showRemoteImages: v })" />
      </SettingRow>
    </SettingSection>
  </div>
</template>
