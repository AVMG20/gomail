<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, DownloadIcon, PlusIcon, SparklesIcon, Trash2Icon, UploadIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { LABEL_COLORS } from '@/lib/color'
import type { Label, Settings } from '@/lib/types'
import { useAiStore } from '@/stores/ai'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const ai = useAiStore()
const s = computed(() => settings.settings!)
const userLabels = computed(() => settings.labels.filter((l) => !l.source || l.source === 'user'))
const providerLabels = computed(() => settings.labels.filter((l) => l.source && l.source !== 'user'))
const justAdded = ref<string | null>(null)

function setLabels(next: Label[]) {
  void settings.update({ labels: next }, 500)
}
function patch(id: string, p: Partial<Label>) {
  setLabels(settings.labels.map((l) => (l.id === id ? { ...l, ...p } : l)))
}
function add() {
  const id = `lbl_${Date.now().toString(36)}`
  const color = LABEL_COLORS[userLabels.value.length % LABEL_COLORS.length]
  justAdded.value = id
  setLabels([...settings.labels, { id, name: 'New label', description: '', color, source: 'user' }])
}
function remove(id: string) {
  setLabels(settings.labels.filter((l) => l.id !== id))
}
function move(id: string, delta: number) {
  const list = [...settings.labels]
  const users = list.filter((l) => !l.source || l.source === 'user')
  const i = users.findIndex((l) => l.id === id)
  const j = i + delta
  if (j < 0 || j >= users.length) return
  ;[users[i], users[j]] = [users[j], users[i]]
  setLabels([...users, ...list.filter((l) => l.source && l.source !== 'user')])
}
// ---- export / import (share Jev label setups)
const fileInput = ref<HTMLInputElement | null>(null)
interface LabelExport { app: 'gomail'; kind: 'labels'; version: 1; labels: { name: string; description: string; color: string }[]; classifyInstructions?: string; grammarSystemPrompt?: string }

async function exportLabels() {
  const data: LabelExport = {
    app: 'gomail', kind: 'labels', version: 1,
    labels: userLabels.value.map(({ name, description, color }) => ({ name, description, color })),
    classifyInstructions: s.value.classifyInstructions || undefined,
    grammarSystemPrompt: s.value.grammarSystemPrompt || undefined,
  }
  const json = JSON.stringify(data, null, 2)
  await api.clipboardSet(json)
  // Also offer a file where the webview supports downloads (browsers; harmless elsewhere).
  try {
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([json], { type: 'application/json' }))
    a.download = 'gomail-labels.json'
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 1000)
  } catch {
    /* clipboard copy is enough */
  }
  toast.success(`Exported ${data.labels.length} labels`, { description: 'Copied to the clipboard as JSON.' })
}

function importLabels(text: string) {
  let data: Partial<LabelExport>
  try {
    data = JSON.parse(text) as Partial<LabelExport>
  } catch {
    toast.error('That file isn’t valid JSON')
    return
  }
  if (!Array.isArray(data.labels)) {
    toast.error('No labels found in that file')
    return
  }
  // Merge by name (case-insensitive): update description/color, add the rest.
  const next = [...settings.labels]
  let added = 0
  let updated = 0
  for (const l of data.labels) {
    if (!l?.name || typeof l.name !== 'string') continue
    const i = next.findIndex((x) => x.name.trim().toLowerCase() === l.name.trim().toLowerCase())
    const color = /^#[0-9a-f]{3,8}$/i.test(l.color ?? '') ? l.color : LABEL_COLORS[next.length % LABEL_COLORS.length]
    if (i >= 0) {
      next[i] = { ...next[i], description: l.description ?? next[i].description, color }
      updated++
    } else {
      next.push({ id: `lbl_${Date.now().toString(36)}${added}`, name: l.name.trim(), description: l.description ?? '', color, source: 'user' })
      added++
    }
  }
  const patch: Partial<Settings> = { labels: next }
  if (typeof data.classifyInstructions === 'string') patch.classifyInstructions = data.classifyInstructions
  if (typeof data.grammarSystemPrompt === 'string') patch.grammarSystemPrompt = data.grammarSystemPrompt
  void settings.update(patch, 0)
  toast.success(`Imported labels: ${added} added, ${updated} updated`)
}

async function onImportFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (file) importLabels(await file.text())
  if (fileInput.value) fileInput.value.value = ''
}

function labelAll() {
  ai.requestBulk({ query: { accountId: '' }, ids: [], title: 'All mail in all accounts' })
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Your labels" description="Tag mail across all accounts. The description tells the AI when a label applies.">
      <div v-for="(l, i) in userLabels" :key="l.id" class="group flex items-start gap-3 py-3">
        <Popover>
          <PopoverTrigger as-child>
            <button type="button" class="border-border mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md border" aria-label="Label color">
              <span class="size-3.5 rounded-full" :style="{ background: l.color }" />
            </button>
          </PopoverTrigger>
          <PopoverContent class="w-auto p-2" align="start">
            <div class="grid grid-cols-5 gap-1.5">
              <button v-for="c in LABEL_COLORS" :key="c" type="button" class="size-7 rounded-full" :style="{ background: c, boxShadow: l.color === c ? `0 0 0 2px var(--popover), 0 0 0 4px ${c}` : undefined }" :aria-label="c" @click="patch(l.id, { color: c })" />
            </div>
          </PopoverContent>
        </Popover>
        <div class="min-w-0 flex-1 space-y-1.5">
          <div class="flex items-center gap-2">
            <Input :model-value="l.name" class="h-8 max-w-56 text-[13px] font-medium" aria-label="Label name" :autofocus="justAdded === l.id" @update:model-value="(v) => patch(l.id, { name: String(v) })" />
          </div>
          <Textarea :model-value="l.description" rows="1" :aria-label="`Description for ${l.name}`" placeholder="When should this label apply? e.g. “Invoices, receipts and bank statements”" class="min-h-8 resize-none py-1.5 text-xs" @update:model-value="(v) => patch(l.id, { description: String(v) })" />
        </div>
        <div class="flex items-center gap-0.5 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-xs" aria-label="Move up" :disabled="i === 0" @click="move(l.id, -1)"><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon-xs" aria-label="Move down" :disabled="i === userLabels.length - 1" @click="move(l.id, 1)"><ArrowDownIcon /></Button>
          <Button variant="ghost" size="icon-xs" class="hover:text-destructive" aria-label="Delete label" @click="remove(l.id)"><Trash2Icon /></Button>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2 py-3">
        <Button variant="outline" size="sm" @click="add"><PlusIcon /> New label</Button>
        <Button variant="ghost" size="sm" @click="exportLabels"><DownloadIcon /> Export</Button>
        <Button variant="ghost" size="sm" @click="fileInput?.click()"><UploadIcon /> Import</Button>
        <input ref="fileInput" type="file" accept="application/json,.json" class="hidden" @change="onImportFile" />
        <Button v-if="settings.aiReady" variant="ghost" size="sm" @click="labelAll"><SparklesIcon class="text-violet-500" /> Label all mail with AI…</Button>
      </div>
    </SettingSection>

    <SettingSection title="Gmail & Outlook">
      <SettingRow title="Import labels from Gmail/Outlook" description="Gmail labels (including Junk) and Outlook categories appear as labels.">
        <Switch :model-value="s.importProviderLabels" @update:model-value="(v: boolean) => settings.update({ importProviderLabels: v })" />
      </SettingRow>
      <div v-for="l in providerLabels" :key="l.id" class="flex items-center gap-3 py-2.5">
        <Popover>
          <PopoverTrigger as-child>
            <button type="button" class="border-border flex size-8 shrink-0 items-center justify-center rounded-md border" aria-label="Label color">
              <span class="size-3.5 rounded-full" :style="{ background: l.color }" />
            </button>
          </PopoverTrigger>
          <PopoverContent class="w-auto p-2" align="start">
            <div class="grid grid-cols-5 gap-1.5">
              <button v-for="c in LABEL_COLORS" :key="c" type="button" class="size-7 rounded-full" :style="{ background: c }" :aria-label="c" @click="patch(l.id, { color: c })" />
            </div>
          </PopoverContent>
        </Popover>
        <span class="flex-1 text-[13px] font-medium">{{ l.name }}</span>
        <span class="text-muted-foreground inline-flex items-center gap-1.5 text-xs">
          <ProviderIcon v-if="l.provider" :provider="l.provider" class="size-3.5" />
          {{ l.source === 'system' ? 'Built-in' : l.provider ? `From ${l.provider === 'outlook' ? 'Outlook' : 'Gmail'}` : 'Imported from Gmail/Outlook' }}
        </span>
      </div>
      <p v-if="s.importProviderLabels && !providerLabels.length" class="text-muted-foreground py-3 text-xs">No labels imported yet.</p>
    </SettingSection>
  </div>
</template>
