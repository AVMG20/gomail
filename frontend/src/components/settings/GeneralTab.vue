<script setup lang="ts">
import { Switch } from '@/components/ui/switch'
import { Slider } from '@/components/ui/slider'
import { shortcutLabel } from '@/lib/platform'
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
      <SettingRow title="Mark as read" description="When an opened message counts as read.">
        <Select :model-value="settings.settings.markReadMode ?? 'immediate'" @update:model-value="(v) => settings.update({ markReadMode: v as 'immediate' | 'delay' | 'manual' })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="immediate">Immediately</SelectItem>
            <SelectItem value="delay">After 2 seconds</SelectItem>
            <SelectItem value="manual">Manually</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Reading pane" description="Off shows a full-width list; messages open in place with a back button.">
        <div class="bg-muted inline-flex rounded-lg p-0.5">
          <button v-for="o in [{ v: 'right', l: 'Right' }, { v: 'off', l: 'Off' }]" :key="o.v" type="button" :class="['rounded-md px-3 py-1 text-xs font-medium transition-colors', (settings.settings.readingPane ?? 'right') === o.v ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']" @click="settings.update({ readingPane: o.v as 'right' | 'off' })">{{ o.l }}</button>
        </div>
      </SettingRow>
      <SettingRow title="Text size" :description="`${Math.round((settings.settings.zoom ?? 1) * 100)}% · ${shortcutLabel('mod+=')} / ${shortcutLabel('mod+-')} / ${shortcutLabel('mod+0')}`">
        <div class="flex w-56 items-center gap-3">
          <Slider :model-value="[settings.settings.zoom ?? 1]" :min="0.9" :max="1.25" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ zoom: Math.round(v[0] * 100) / 100 })" />
          <button type="button" class="text-muted-foreground hover:text-foreground text-xs" @click="settings.update({ zoom: 1 })">Reset</button>
        </div>
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
