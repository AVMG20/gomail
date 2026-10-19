<script setup lang="ts">
import { onMounted } from 'vue'
import { CopyIcon, MailIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { api } from '@/lib/api'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
onMounted(() => settings.loadAppInfo())

async function copy(text: string) {
  await api.clipboardSet(text)
  toast('Copied to clipboard')
}
</script>

<template>
  <div>
    <div class="mb-6 flex items-center gap-4">
      <div class="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm"><MailIcon class="size-6" /></div>
      <div>
        <div class="flex items-center gap-2 text-lg font-semibold">gomail <Badge v-if="settings.appInfo?.demo" variant="secondary">Demo data</Badge></div>
        <div class="text-muted-foreground text-xs">A calm desktop mail client · Version {{ settings.appInfo?.version ?? '—' }}</div>
      </div>
    </div>
    <SettingSection title="Diagnostics">
      <SettingRow title="Platform" :description="settings.appInfo?.platform || '—'" />
      <SettingRow v-for="row in [{ k: 'Data folder', v: settings.appInfo?.dataDir }, { k: 'Log file', v: settings.appInfo?.logFile }]" :key="row.k" :title="row.k">
        <template #description><span class="selectable font-mono">{{ row.v || '—' }}</span></template>
        <button v-if="row.v" type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted rounded-md p-1.5" :aria-label="`Copy ${row.k}`" @click="copy(row.v!)"><CopyIcon class="size-3.5" /></button>
      </SettingRow>
    </SettingSection>
  </div>
</template>
