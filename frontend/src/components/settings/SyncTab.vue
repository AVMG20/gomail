<script setup lang="ts">
// Settings → Sync: keep settings, labels and sign-ins the same on every computer through a
// gomail-sync server. A 24-word sync code, made by the app, links the computers; everything is
// encrypted on the computer with a key from it.
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { CheckCircle2Icon, ChevronRightIcon, CopyIcon, LoaderIcon, RefreshCwIcon, TriangleAlertIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { api, DEVICE_SYNC_INTERVALS, errorMessage, normSyncStatus, syncCodeWords } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import type { DeviceSyncStatus } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const status = ref<DeviceSyncStatus | null>(null)
const server = ref('')
const typedCode = ref('')
/** The sync code on screen (after turning sync on, or "Show sync code"). */
const shownCode = ref('')
const busy = ref<'create' | 'join' | 'sync' | 'secrets' | 'code' | 'disconnect' | null>(null)
const confirmOff = ref(false)

let off: (() => void) | null = null
onMounted(async () => {
  off = api.on('devicesync:status', (p) => (status.value = normSyncStatus(p)))
  try {
    status.value = await api.getDeviceSyncStatus()
  } catch (e) {
    toast.error('Couldn’t read the sync status', { description: errorMessage(e) })
  }
  if (status.value && !server.value) server.value = status.value.defaultServerUrl
})
onUnmounted(() => off?.())

const codeWords = computed(() => syncCodeWords(shownCode.value))

async function turnOn() {
  busy.value = 'create'
  try {
    status.value = await api.connectDeviceSync({ serverUrl: server.value.trim(), create: true, code: '', includeSecrets: true })
    shownCode.value = await api.getDeviceSyncCode()
  } catch (e) {
    toast.error('Couldn’t turn on sync', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function join() {
  busy.value = 'join'
  try {
    status.value = await api.connectDeviceSync({ serverUrl: server.value.trim(), create: false, code: typedCode.value, includeSecrets: true })
    typedCode.value = ''
    toast.success('Sync is on', { description: 'This computer is getting your settings now.' })
  } catch (e) {
    toast.error('Couldn’t use this code', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function toggleCode() {
  if (shownCode.value) {
    shownCode.value = ''
    return
  }
  busy.value = 'code'
  try {
    shownCode.value = await api.getDeviceSyncCode()
  } catch (e) {
    toast.error('Couldn’t read the sync code', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function copyCode() {
  await api.clipboardSet(shownCode.value)
  toast('Sync code copied')
}

async function syncNow() {
  busy.value = 'sync'
  try {
    status.value = await api.syncDevicesNow()
  } catch (e) {
    toast.error('Couldn’t sync', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function setSecrets(on: boolean) {
  busy.value = 'secrets'
  try {
    await api.setDeviceSyncSecrets(on)
    if (status.value) status.value = { ...status.value, includeSecrets: on }
  } catch (e) {
    toast.error('Couldn’t change it', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

async function turnOff() {
  busy.value = 'disconnect'
  try {
    await api.disconnectDeviceSync()
    status.value = await api.getDeviceSyncStatus()
    shownCode.value = ''
    toast('Sync is off on this computer', { description: 'Its settings and mail stay as they are.' })
  } catch (e) {
    toast.error('Couldn’t turn off sync', { description: errorMessage(e) })
  } finally {
    busy.value = null
  }
}

const statusLine = computed(() => {
  const s = status.value
  if (!s) return ''
  if (s.syncing) return 'Syncing…'
  return s.lastSyncAt ? `Last synced ${relativeTime(s.lastSyncAt)}` : 'Not synced yet'
})
</script>

<template>
  <div v-if="status">
    <p v-if="!status.available" class="text-muted-foreground py-3.5 text-[13px]">Sync between computers isn’t available with demo data.</p>

    <template v-else-if="status.connected">
      <SettingSection title="Sync between computers" description="Settings, mailboxes, labels and rules stay the same on every computer that uses your sync code.">
        <SettingRow title="Status">
          <template #description>
            <span v-if="status.error && !status.syncing" class="text-destructive inline-flex items-start gap-1" role="alert" data-testid="sync-error">
              <TriangleAlertIcon class="mt-0.5 size-3.5 shrink-0" aria-hidden="true" /> {{ status.error }}
            </span>
            <span v-else class="inline-flex items-center gap-1" data-testid="sync-status">
              <CheckCircle2Icon v-if="!status.syncing" class="size-3.5 text-emerald-600 dark:text-emerald-400" aria-hidden="true" /> {{ statusLine }}
            </span>
          </template>
          <Button size="sm" variant="outline" :disabled="!!busy || status.syncing" @click="syncNow">
            <LoaderIcon v-if="busy === 'sync' || status.syncing" class="animate-spin" /><RefreshCwIcon v-else /> Sync now
          </Button>
        </SettingRow>
        <SettingRow title="Sync automatically" description="And when gomail starts. Set for this computer only.">
          <Select :model-value="String(settings.settings?.deviceSyncInterval ?? 1)" @update:model-value="(v) => settings.update({ deviceSyncInterval: Number(v) })">
            <SelectTrigger class="w-44" data-testid="sync-interval"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="o in DEVICE_SYNC_INTERVALS" :key="o.value" :value="String(o.value)">{{ o.label }}</SelectItem>
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow title="Sync sign-ins and API keys" description="Another computer gets your mailboxes signed in, your OpenRouter key and OAuth app. They’re encrypted like everything else. Off removes them from the server.">
          <Switch :model-value="status.includeSecrets" :disabled="!!busy" @update:model-value="(v: boolean) => setSecrets(v)" />
        </SettingRow>
        <SettingRow title="Sync code" description="Enter it on another computer to sync it too.">
          <Button size="sm" variant="outline" :disabled="busy === 'code'" data-testid="sync-show-code" @click="toggleCode">{{ shownCode ? 'Hide sync code' : 'Show sync code' }}</Button>
        </SettingRow>
        <div v-if="shownCode" class="pb-3.5" data-testid="sync-code">
          <ol class="bg-muted/60 grid grid-cols-3 gap-x-4 gap-y-1.5 rounded-lg p-3 font-mono text-xs sm:grid-cols-4">
            <li v-for="(w, i) in codeWords" :key="i" class="selectable flex gap-1.5"><span class="text-muted-foreground w-5 text-right tabular-nums">{{ i + 1 }}</span>{{ w }}</li>
          </ol>
          <div class="mt-2 flex items-start gap-3">
            <p class="text-muted-foreground flex-1 text-xs leading-relaxed">
              Enter this code on your other devices. Keep it somewhere safe: without it, synced data can’t be recovered.
            </p>
            <Button size="sm" variant="outline" @click="copyCode"><CopyIcon /> Copy</Button>
          </div>
        </div>
        <SettingRow title="Turn off sync on this computer" :description="`Stops syncing with ${status.serverUrl}. Settings and mail here stay; other computers keep syncing.`">
          <Button size="sm" variant="outline" :disabled="!!busy" @click="confirmOff = true">Turn off…</Button>
        </SettingRow>
      </SettingSection>
    </template>

    <template v-else>
      <SettingSection
        title="Sync between computers"
        description="Keep settings, mailboxes, labels and rules the same on your computers. Everything is encrypted on this computer before it’s sent: the server can’t read it."
      >
        <SettingRow title="Turn on sync" description="For your first computer. gomail makes a sync code to enter on your others.">
          <Button size="sm" :disabled="!!busy" data-testid="sync-turn-on" @click="turnOn">
            <LoaderIcon v-if="busy === 'create'" class="animate-spin" /> Turn on sync
          </Button>
        </SettingRow>
        <SettingRow title="Use a code from another device" description="Paste or type its 24 words. This computer takes the synced settings; mailboxes and labels only it has are added." stacked>
          <div class="flex max-w-lg flex-col items-start gap-2">
            <Textarea v-model="typedCode" rows="3" class="font-mono text-xs" spellcheck="false" autocomplete="off" aria-label="Sync code" placeholder="24 words, in order" data-testid="sync-code-input" />
            <Button size="sm" variant="outline" :disabled="!typedCode.trim() || !!busy" data-testid="sync-join" @click="join">
              <LoaderIcon v-if="busy === 'join'" class="animate-spin" /> Connect
            </Button>
          </div>
        </SettingRow>
        <details class="group py-3.5">
          <summary class="text-muted-foreground hover:text-foreground flex cursor-pointer list-none items-center gap-1 text-xs select-none">
            <ChevronRightIcon class="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" /> Server
          </summary>
          <div class="mt-2 ml-4.5 space-y-1">
            <Input v-model="server" class="max-w-sm text-xs" autocomplete="off" spellcheck="false" aria-label="Sync server" :placeholder="status.defaultServerUrl" data-testid="sync-server" />
            <p class="text-muted-foreground text-xs">Your own gomail-sync server, or the default. Every computer must use the same one.</p>
          </div>
        </details>
      </SettingSection>
    </template>

    <SettingSection v-if="status.available" title="What syncs">
      <SettingRow title="Synced" description="Settings, mailboxes and their order, signatures, labels, rules, Not-junk senders, labels you and Jev put on mail, and which calendars you show." />
      <SettingRow title="Through Gmail and Outlook" description="Your mail, folders, archive, junk, read and starred state: they sync through your mail provider as always." />
      <SettingRow title="Only on this computer" description="Zoom, startup and tray choices, how much mail is kept here, how often it’s checked and synced." />
    </SettingSection>

    <AlertDialog v-model:open="confirmOff">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Turn off sync on this computer?</AlertDialogTitle>
          <AlertDialogDescription>Settings and mail on this computer stay as they are, and your other computers keep syncing. To sync again, use the code from one of them.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction @click="turnOff">Turn off</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
