<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, DownloadIcon, PlusIcon, SparklesIcon, Trash2Icon, UploadIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { LABEL_COLORS } from '@/lib/color'
import { buildLabelExport, MAX_IMPORT_BYTES, mergeLabelImport } from '@/lib/labelsIO'
import { labelAccounts, newLabelAccounts } from '@/lib/labelScope'
import type { Label, Settings } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import LabelMailboxPicker from './LabelMailboxPicker.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'
import RulesSection from './RulesSection.vue'

const settings = useSettingsStore()
const ai = useAiStore()
const accounts = useAccountsStore()
const mail = useMailStore()
const accountIds = computed(() => accounts.accounts.map((a) => a.id))
/** Where an imported label is used, e.g. "alex@example.com" or "2 mailboxes". */
function mailboxesText(l: Label): string {
  if (accountIds.value.length < 2 || l.source === 'system') return ''
  const ids = labelAccounts(l, accountIds.value)
  if (ids.length >= accountIds.value.length) return 'All mailboxes'
  return ids.length === 1 ? accounts.accountMap.get(ids[0])?.email ?? '' : `${ids.length} mailboxes`
}
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
  // A color no label uses yet, when there is one.
  const used = new Set(settings.labels.map((l) => l.color.toLowerCase()))
  const color = LABEL_COLORS.find((c) => !used.has(c.toLowerCase())) ?? LABEL_COLORS[userLabels.value.length % LABEL_COLORS.length]
  justAdded.value = id
  // The mailbox being viewed (every mailbox in the unified view).
  const ids = newLabelAccounts(mail.mailbox.accountId, accountIds.value)
  setLabels([...settings.labels, { id, name: 'New label', description: '', color, source: 'user', accountIds: ids }])
  void nextTick(() => {
    const el = document.querySelector<HTMLInputElement>(`[data-label-name="${id}"]`)
    el?.focus()
    el?.select()
  })
}

/** Names must not be empty, and two labels with the same name would be indistinguishable. */
function onNameBlur(l: Label) {
  const typed = l.name.trim()
  const r = settings.renameLabel(l.id, l.name)
  if (r.deduped) toast(`You already have a label called “${typed}”`, { description: `This one was renamed to “${r.name}”.` })
}

// Deleting a label removes it from every message: confirm, with how many it affects.
const confirmDelete = ref(false)
const deleting = ref<{ label: Label; count: number | null } | null>(null)
let deleteId = ''
async function askRemove(l: Label) {
  deleteId = l.id
  deleting.value = { label: l, count: null }
  confirmDelete.value = true
  try {
    const counts = await api.getLabelCounts('')
    if (deleting.value?.label.id === l.id) deleting.value = { label: l, count: counts.find((c) => c.labelId === l.id)?.total ?? 0 }
  } catch {
    /* the dialog works without a count */
  }
}
function remove() {
  // Read the id kept aside: the dialog's own close clears the reactive state first.
  const id = deleteId
  deleteId = ''
  if (id) setLabels(settings.labels.filter((l) => l.id !== id))
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

async function exportLabels() {
  const data = buildLabelExport(userLabels.value, s.value)
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
  const stamp = Date.now().toString(36)
  const r = mergeLabelImport(text, settings.labels, (i) => `lbl_${stamp}${i}`)
  if (!r.ok) {
    toast.error(r.error)
    return
  }
  // The file may also carry AI prompts: say so, and offer to keep the current ones.
  const cur = settings.settings
  const prev: Partial<Settings> = {}
  const replaced: string[] = []
  if (cur && r.patch.classifyInstructions !== undefined && r.patch.classifyInstructions !== cur.classifyInstructions) {
    prev.classifyInstructions = cur.classifyInstructions
    replaced.push('labeling instructions')
  }
  if (cur && r.patch.grammarSystemPrompt !== undefined && r.patch.grammarSystemPrompt !== cur.grammarSystemPrompt) {
    prev.grammarSystemPrompt = cur.grammarSystemPrompt
    replaced.push('grammar prompt')
  }
  void settings.update(r.patch, 0)
  const notes = [r.skipped ? `${r.skipped} skipped (empty or duplicate names).` : '', replaced.length ? `It also replaced your ${replaced.join(' and ')}.` : ''].filter(Boolean)
  toast.success(`Imported labels: ${r.added} added, ${r.updated} updated`, {
    description: notes.join(' ') || undefined,
    duration: replaced.length ? 15000 : undefined,
    action: replaced.length ? { label: 'Keep mine', onClick: () => void settings.update(prev, 0) } : undefined,
  })
}

async function onImportFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (file && file.size > MAX_IMPORT_BYTES) toast.error('That file is too large to be a label export')
  else if (file) importLabels(await file.text())
  if (fileInput.value) fileInput.value.value = ''
}

function labelAll() {
  ai.requestBulk({ query: { accountId: '' }, ids: [], title: 'All mail in all accounts' })
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Your labels" description="Tag mail in your mailboxes. Each label belongs to the mailboxes you pick; the description tells the AI when it applies.">
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
            <Input :model-value="l.name" class="h-8 max-w-56 text-[13px] font-medium" aria-label="Label name" :data-label-name="l.id" @update:model-value="(v) => patch(l.id, { name: String(v) })" @blur="onNameBlur(l)" />
            <LabelMailboxPicker v-if="accountIds.length > 1" :label="l" @change="(ids) => patch(l.id, { accountIds: ids })" />
          </div>
          <Textarea :model-value="l.description" rows="1" :aria-label="`Description for ${l.name}`" placeholder="When should this label apply? e.g. “Invoices, receipts and bank statements”" class="min-h-8 resize-none py-1.5 text-xs" @update:model-value="(v) => patch(l.id, { description: String(v) })" />
        </div>
        <div class="flex items-center gap-0.5 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-xs" aria-label="Move up" :disabled="i === 0" @click="move(l.id, -1)"><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon-xs" aria-label="Move down" :disabled="i === userLabels.length - 1" @click="move(l.id, 1)"><ArrowDownIcon /></Button>
          <Button variant="ghost" size="icon-xs" class="hover:text-destructive" aria-label="Delete label" @click="askRemove(l)"><Trash2Icon /></Button>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2 py-3">
        <Button variant="outline" size="sm" @click="add"><PlusIcon /> New label</Button>
        <Button variant="ghost" size="sm" @click="exportLabels"><DownloadIcon /> Export</Button>
        <Button variant="ghost" size="sm" @click="fileInput?.click()"><UploadIcon /> Import</Button>
        <input ref="fileInput" type="file" accept="application/json,.json" class="hidden" @change="onImportFile" />
        <Button variant="ghost" size="sm" @click="labelAll"><SparklesIcon class="text-violet-500" /> Label all mail with AI…</Button>
      </div>
    </SettingSection>

    <RulesSection />

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
          <template v-if="mailboxesText(l)"> · {{ mailboxesText(l) }}</template>
        </span>
      </div>
      <p v-if="s.importProviderLabels && !providerLabels.length" class="text-muted-foreground py-3 text-xs">No labels imported yet.</p>
    </SettingSection>

    <AlertDialog v-model:open="confirmDelete">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete the label “{{ deleting?.label.name }}”?</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="deleting?.count">It’s removed from {{ deleting.count.toLocaleString() }} message{{ deleting.count === 1 ? '' : 's' }}. </template>
            <template v-else-if="deleting?.count === 0">No messages have this label. </template>
            This can’t be undone. Labels in Gmail or Outlook aren’t changed.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="remove">Delete label</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
