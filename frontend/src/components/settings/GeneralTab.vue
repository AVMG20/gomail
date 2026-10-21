<script setup lang="ts">
import { onMounted } from 'vue'
import { ExternalLinkIcon, LoaderIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { api, hasMethod } from '@/lib/api'
import { useSettingsStore } from '@/stores/settings'
import BackupSection from './BackupSection.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const intervals = [1, 5, 15, 30, 60]
onMounted(() => {
  if (!settings.appInfo) void settings.loadAppInfo()
})

async function checkNow() {
  try {
    await settings.checkForUpdate(true)
    if (settings.updateInfo && !settings.updateInfo.available) toast('gomail is up to date')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : String(e))
  }
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Startup">
      <SettingRow title="Open gomail when you log in" description="Starts in the background, so new mail arrives right away.">
        <Switch :model-value="!!settings.settings.launchAtLogin" @update:model-value="(v: boolean) => settings.update({ launchAtLogin: v })" />
      </SettingRow>
      <SettingRow title="Show in menu bar / system tray" description="Shows how much mail is unread.">
        <Switch :model-value="settings.settings.showInTray ?? true" @update:model-value="(v: boolean) => settings.update({ showInTray: v })" />
      </SettingRow>
      <SettingRow v-if="settings.settings.showInTray ?? true" title="Keep running when the window is closed" description="Closing the window hides gomail, so new mail still arrives.">
        <Switch :model-value="settings.settings.closeToTray ?? false" @update:model-value="(v: boolean) => settings.update({ closeToTray: v })" />
      </SettingRow>
    </SettingSection>

    <SettingSection title="Sync & storage" all-mailboxes>
      <SettingRow title="Check for new mail" description="How often gomail looks for new mail in the background.">
        <Select :model-value="String(settings.settings.syncIntervalMinutes)" @update:model-value="(v) => settings.update({ syncIntervalMinutes: Number(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="i in intervals" :key="i" :value="String(i)">Every {{ i === 1 ? 'minute' : i === 60 ? 'hour' : `${i} minutes` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Download mail from" description="gomail keeps this much of your mail on this computer. Anything older stays on the server: load it at the end of a folder.">
        <Select :model-value="String(settings.settings.syncDepthDays ?? 90)" @update:model-value="(v) => settings.update({ syncDepthDays: Number(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="d in [30, 90, 365, 0]" :key="d" :value="String(d)">{{ d === 0 ? 'All time' : d === 365 ? 'The last year' : `The last ${d} days` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Pause downloading older mail" description="Stops the background download of the period above. New mail still arrives.">
        <Switch :model-value="!!settings.settings.backfillPaused" @update:model-value="(v: boolean) => settings.update({ backfillPaused: v })" />
      </SettingRow>
    </SettingSection>

    <SettingSection title="Updates">
      <SettingRow title="Check for updates automatically" description="Checks GitHub once a day. Nothing installs without you.">
        <Switch :model-value="settings.settings.checkUpdates !== false" @update:model-value="(v: boolean) => settings.update({ checkUpdates: v })" />
      </SettingRow>
      <SettingRow
        v-if="hasMethod('CheckForUpdate')"
        :title="settings.updateInfo?.available ? `Version ${settings.updateInfo.latest} is available` : `Version ${settings.updateInfo?.current ?? settings.appInfo?.version ?? '—'}`"
        :description="settings.updateInfo?.available ? settings.updateInfo.notes || 'See the release notes on GitHub.' : settings.updateInfo ? 'You’re up to date.' : 'The version you have now.'"
      >
        <Button v-if="settings.updateInfo?.available" size="sm" @click="api.openExternal(settings.updateInfo.url)">Download <ExternalLinkIcon /></Button>
        <Button v-else size="sm" variant="outline" :disabled="settings.checkingUpdate" @click="checkNow"><LoaderIcon v-if="settings.checkingUpdate" class="animate-spin" /> Check now</Button>
      </SettingRow>
    </SettingSection>

    <BackupSection />
  </div>
</template>
