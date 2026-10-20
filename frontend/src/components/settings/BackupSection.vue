<script setup lang="ts">
import { ref, useId } from 'vue'
import { DownloadIcon, LoaderIcon, TriangleAlertIcon, UploadIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { backupResultText, restorePendingText, restoreSummaryText } from '@/lib/backup'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const accounts = useAccountsStore()
const includeSecrets = ref(false)
const busy = ref<'export' | 'import' | null>(null)
const secretsId = useId()

async function exportBackup() {
  busy.value = 'export'
  try {
    const r = await api.exportBackup(includeSecrets.value)
    if (!r.cancelled) toast.success('Backup saved', { description: backupResultText(r) })
  } catch (e) {
    toast.error('Couldn’t save the backup', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function importBackup() {
  busy.value = 'import'
  try {
    const s = await api.importBackup()
    if (s.cancelled) return
    await Promise.all([settings.load(), accounts.load()])
    const description = [restoreSummaryText(s), restorePendingText(s)].filter(Boolean).join(' ')
    toast.success('Backup imported', { description, duration: 12000 })
  } catch (e) {
    toast.error('Couldn’t import the backup', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <SettingSection
    v-if="hasMethod('ExportBackup')"
    title="Backup & restore"
    description="Saves your settings, accounts, labels and rules to a file, for a new computer or a reinstall. Your mail isn’t in it: it downloads again when you sign in."
  >
    <SettingRow title="Export a backup" description="Saves a .gomailbackup file. Sign-ins are never included, so restored accounts ask you to sign in again.">
      <Button size="sm" variant="outline" :disabled="!!busy" @click="exportBackup">
        <LoaderIcon v-if="busy === 'export'" class="animate-spin" /><DownloadIcon v-else /> Export…
      </Button>
    </SettingRow>
    <div class="py-3.5">
      <div class="flex items-start gap-2.5">
        <Checkbox :id="secretsId" v-model="includeSecrets" class="mt-0.5" />
        <label :for="secretsId" class="text-[13px] leading-snug font-medium">Include API keys and OAuth app credentials</label>
      </div>
      <p v-if="includeSecrets" class="mt-2 ml-6.5 flex items-start gap-1.5 text-xs leading-relaxed text-amber-700 dark:text-amber-400" role="note">
        <TriangleAlertIcon class="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
        The file will hold your OpenRouter key and Google/Microsoft client secrets in plain text. Keep it somewhere safe.
      </p>
      <p v-else class="text-muted-foreground mt-1 ml-6.5 text-xs">When off, you enter your OpenRouter key again after restoring.</p>
    </div>
    <SettingRow title="Restore a backup" description="Adds a backup to this install. Nothing is deleted, and accounts you already have stay as they are.">
      <Button size="sm" variant="outline" :disabled="!!busy" @click="importBackup">
        <LoaderIcon v-if="busy === 'import'" class="animate-spin" /><UploadIcon v-else /> Import…
      </Button>
    </SettingRow>
  </SettingSection>
</template>
