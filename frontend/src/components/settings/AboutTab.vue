<script setup lang="ts">
import { onMounted } from 'vue'
import { CopyIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const ui = useUiStore()
onMounted(() => settings.loadAppInfo())

async function copy(text: string) {
  await api.clipboardSet(text)
  toast('Copied to clipboard')
}
</script>

<template>
  <div>
    <div class="mb-6 flex items-center gap-4">
      <img src="/appicon.svg" alt="" class="size-14 drop-shadow-sm" width="56" height="56" />
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2 text-lg font-semibold">gomail <Badge v-if="settings.appInfo?.demo" variant="secondary">Demo data</Badge></div>
        <div class="text-muted-foreground text-xs">A calm desktop mail client · Version {{ settings.appInfo?.version ?? '—' }}</div>
      </div>
      <Button size="sm" variant="outline" @click="ui.settingsTab = 'general'">{{ settings.updateInfo?.available ? `Get version ${settings.updateInfo.latest}` : 'Updates' }}</Button>
    </div>
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
