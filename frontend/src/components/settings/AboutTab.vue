<script setup lang="ts">
import { onMounted } from 'vue'
import { CopyIcon, ExternalLinkIcon, LoaderIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api, hasMethod } from '@/lib/api'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import BackupSection from './BackupSection.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const ui = useUiStore()
onMounted(() => settings.loadAppInfo())

async function checkNow() {
  try {
    await settings.checkForUpdate(true)
    if (settings.updateInfo && !settings.updateInfo.available) toast('gomail is up to date')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : String(e))
  }
}

async function copy(text: string) {
  await api.clipboardSet(text)
  toast('Copied to clipboard')
}
</script>

<template>
  <div>
    <div class="mb-6 flex items-center gap-4">
      <img src="/appicon.svg" alt="" class="size-14 drop-shadow-sm" width="56" height="56" />
      <div>
        <div class="flex items-center gap-2 text-lg font-semibold">gomail <Badge v-if="settings.appInfo?.demo" variant="secondary">Demo data</Badge></div>
        <div class="text-muted-foreground text-xs">A calm desktop mail client · Version {{ settings.appInfo?.version ?? '—' }}</div>
      </div>
    </div>
    <SettingSection v-if="hasMethod('CheckForUpdate')" title="Updates">
      <SettingRow
        :title="settings.updateInfo?.available ? `Version ${settings.updateInfo.latest} is available` : 'Check for updates'"
        :description="settings.updateInfo?.available ? settings.updateInfo.notes || 'See the release notes on GitHub.' : settings.updateInfo ? `You’re on the latest version (${settings.updateInfo.current}).` : 'Looks for a newer release on GitHub.'"
      >
        <Button v-if="settings.updateInfo?.available" size="sm" @click="api.openExternal(settings.updateInfo.url)">Download <ExternalLinkIcon /></Button>
        <Button v-else size="sm" variant="outline" :disabled="settings.checkingUpdate" @click="checkNow"><LoaderIcon v-if="settings.checkingUpdate" class="animate-spin" /> Check now</Button>
      </SettingRow>
    </SettingSection>
    <BackupSection />
    <SettingSection title="Help">
      <SettingRow title="Welcome guide" description="Walk through connecting mail, AI labeling and starter labels again.">
        <Button size="sm" variant="outline" @click="ui.welcomeTick++">Show again</Button>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Diagnostics">
      <SettingRow title="Platform" :description="settings.appInfo?.os ? `${settings.appInfo.os}/${settings.appInfo.arch}` : settings.appInfo?.platform || '—'" />
      <SettingRow v-for="row in [{ k: 'Data folder', v: settings.appInfo?.dataDir }, { k: 'Log file', v: settings.appInfo?.logFile }]" :key="row.k" :title="row.k">
        <template #description><span class="selectable font-mono">{{ row.v || '—' }}</span></template>
        <button v-if="row.v" type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted rounded-md p-1.5" :aria-label="`Copy ${row.k}`" @click="copy(row.v!)"><CopyIcon class="size-3.5" /></button>
      </SettingRow>
    </SettingSection>
  </div>
</template>
