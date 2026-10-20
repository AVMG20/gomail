<script setup lang="ts">
// One mailbox's labels and rules (its Settings page → Labels & rules). Every mailbox has its own
// labels: nothing here changes another mailbox. Its Gmail labels / Outlook categories are listed
// apart: their names follow Gmail/Outlook, the rest (color, description, action) is ours.
import { computed, nextTick, ref } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, CopyIcon, DownloadIcon, EyeOffIcon, PlayIcon, PlusIcon, Trash2Icon, UploadIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { api, hasMethod } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger } from '@/components/ui/select'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { LABEL_COLORS } from '@/lib/color'
import { buildLabelExport, MAX_IMPORT_BYTES, mergeLabelImport } from '@/lib/labelsIO'
import { copyLabelsTo, nameTaken, newLabelId } from '@/lib/labelScope'
import type { Label, Settings } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'
import JunkLabelChips from './JunkLabelChips.vue'
import RulesSection from './RulesSection.vue'
import { useMailboxSettings } from './mailboxSettings'

const props = defineProps<{ accountId: string }>()
const settings = useSettingsStore()
const accounts = useAccountsStore()

const account = computed(() => accounts.accountMap.get(props.accountId))
const providerName = computed(() => (account.value?.provider === 'outlook' ? 'Outlook' : 'Gmail'))
const s = computed(() => settings.settings!)
const mine = (l: Label) => !!l.accountIds?.includes(props.accountId)
const userLabels = computed(() => settings.labels.filter((l) => mine(l) && (!l.source || l.source === 'user')))
const importedLabels = computed(() => settings.labels.filter((l) => mine(l) && l.source === 'provider'))

// ---- what happens to mail that gets the label
const THEN: { value: string; label: string; chip: string }[] = [
  { value: 'keep', label: 'Don’t move', chip: '' },
  { value: 'archive', label: 'Archive', chip: '→ Archive' },
  { value: 'junk', label: 'Move to Junk', chip: '→ Junk' },
  // Jev never trashes: a wrong guess would delete real mail, so its picks go to Junk instead.
  { value: 'trash', label: 'Move to Trash (Junk when Jev adds it)', chip: '→ Trash' },
]
/** Who makes a label's choice happen: hand labels too with "Move mail when you add a label yourself". */
const whenHeader = computed(() => (s.value.manualLabelActions ? 'When the label is added' : 'When Jev or a rule adds it (not you)'))
const whenHeaderImported = computed(() => (s.value.manualLabelActions ? 'When mail arrives with it or gets it' : 'When mail arrives with it, or Jev or a rule adds it'))
const thenOf = (l: Label) => THEN.find((t) => t.value === l.then) ?? THEN[0]

const mb = useMailboxSettings()
/** Choosing what a label does offers to do it for the mail that already has it. */
const setThen = (l: Label, v: string) => mb.setThen(l, v === 'keep' ? '' : v)

function setLabels(next: Label[]) {
  void settings.update({ labels: next }, 500)
}
function patch(id: string, p: Partial<Label>) {
  setLabels(settings.labels.map((l) => (l.id === id ? { ...l, ...p } : l)))
}

// ---- copy this mailbox's labels to another one (labels are per mailbox, so set them up once)
const otherMailboxes = computed(() => accounts.accounts.filter((a) => a.id !== props.accountId))
function copyTo(toId: string) {
  const to = accounts.accountMap.get(toId)
  const r = copyLabelsTo(settings.labels, props.accountId, toId)
  if (!r.copied) return toast(`${to?.displayName || to?.email} already has these labels`)
  setLabels(r.labels)
  toast(`Copied ${r.copied} label${r.copied === 1 ? '' : 's'} to ${to?.displayName || to?.email}`)
}

const justAdded = ref<string | null>(null)
function add() {
  let name = 'New label'
  for (let n = 2; nameTaken(settings.labels, props.accountId, name); n++) name = `New label ${n}`
  const id = newLabelId(settings.labels, name)
  // A color none of this mailbox's labels uses yet, when there is one.
  const used = new Set(userLabels.value.map((l) => l.color.toLowerCase()))
  const color = LABEL_COLORS.find((c) => !used.has(c.toLowerCase())) ?? LABEL_COLORS[userLabels.value.length % LABEL_COLORS.length]
  justAdded.value = id
  setLabels([...settings.labels, { id, name, description: '', color, source: 'user', accountIds: [props.accountId], then: '' }])
  void nextTick(() => {
    const el = document.querySelector<HTMLInputElement>(`[data-label-name="${id}"]`)
    el?.focus()
    el?.select()
  })
}

/** Names must not be empty, and two labels of this mailbox with the same name would be indistinguishable. */
function onNameBlur(l: Label) {
  const typed = l.name.trim()
  const r = settings.renameLabel(l.id, l.name)
  if (r.deduped) toast(`This mailbox already has a label called “${typed}”`, { description: `This one was renamed to “${r.name}”.` })
}

/** Swap a label with its neighbour among this mailbox's labels (other labels keep their place). */
function move(id: string, delta: number) {
  const list = [...settings.labels]
  const own = userLabels.value
  const i = own.findIndex((l) => l.id === id)
  const j = i + delta
  if (i < 0 || j < 0 || j >= own.length) return
  const a = list.findIndex((l) => l.id === own[i].id)
  const b = list.findIndex((l) => l.id === own[j].id)
  ;[list[a], list[b]] = [list[b], list[a]]
  setLabels(list)
}

// ---- delete (user labels) / hide (imported ones): confirm, with how many messages it affects
const confirmDelete = ref(false)
const deleting = ref<{ label: Label; count: number | null } | null>(null)
let deleteId = ''
async function askRemove(l: Label) {
  deleteId = l.id
  deleting.value = { label: l, count: null }
  confirmDelete.value = true
  try {
    const counts = await api.getLabelCounts(props.accountId)
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
  const r = mergeLabelImport(text, settings.labels, (i) => `lbl_${stamp}${i}`, props.accountId)
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

</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Labels" description="This mailbox’s labels; other mailboxes have their own. Jev uses each description to decide when a label applies.">
      <SettingRow v-if="account" title="Labels that go to Junk" description="Mail that gets one of these labels moves to Junk, marked read. Same as “Move to Junk” on a label below." stacked>
        <JunkLabelChips :account="account" />
      </SettingRow>
      <div v-for="(l, i) in userLabels" :key="l.id" class="group flex items-start gap-3 py-3">
        <Popover>
          <PopoverTrigger as-child>
            <button type="button" class="border-border mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md border" :aria-label="`Color of ${l.name}`">
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
            <Select :model-value="thenOf(l).value" @update:model-value="(v) => setThen(l, String(v))">
              <SelectTrigger
                size="sm"
                :class="['h-6 shrink-0 gap-1 rounded-full px-2 text-xs data-[size=sm]:h-6', thenOf(l).chip ? 'bg-muted text-foreground border-transparent' : 'text-muted-foreground border-dashed']"
                :aria-label="`When “${l.name}” is added`"
              >
                {{ thenOf(l).chip || 'Move mail…' }}
              </SelectTrigger>
              <SelectContent>
                <div class="text-muted-foreground px-2 py-1.5 text-xs">{{ whenHeader }}</div>
                <SelectItem v-for="t in THEN" :key="t.value" :value="t.value">{{ t.label }}</SelectItem>
              </SelectContent>
            </Select>
            <Button v-if="l.then && hasMethod('ApplyLabelAction')" variant="ghost" size="icon-xs" class="text-muted-foreground" :disabled="mb.isApplying(l.id)" :title="`Move mail that already has “${l.name}” to ${mb.placeOf(l.then)}`" :aria-label="`Move mail that already has ${l.name} to ${mb.placeOf(l.then)}`" @click="mb.applyThen(l)"><PlayIcon /></Button>
          </div>
          <Textarea :model-value="l.description" rows="1" :aria-label="`Description for ${l.name}`" placeholder="When should this label apply? e.g. “Invoices, receipts and bank statements”" class="min-h-8 resize-none py-1.5 text-xs" @update:model-value="(v) => patch(l.id, { description: String(v) })" />
        </div>
        <div class="flex items-center gap-0.5 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-xs" aria-label="Move up" :disabled="i === 0" @click="move(l.id, -1)"><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon-xs" aria-label="Move down" :disabled="i === userLabels.length - 1" @click="move(l.id, 1)"><ArrowDownIcon /></Button>
          <Button variant="ghost" size="icon-xs" class="hover:text-destructive" :aria-label="`Delete ${l.name}`" @click="askRemove(l)"><Trash2Icon /></Button>
        </div>
      </div>
      <p v-if="!userLabels.length" class="text-muted-foreground py-3 text-xs">This mailbox has no labels yet.</p>
      <div class="flex flex-wrap items-center gap-2 py-3">
        <Button variant="outline" size="sm" @click="add"><PlusIcon /> New label</Button>
        <DropdownMenu v-if="otherMailboxes.length">
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="sm" :disabled="!userLabels.length"><CopyIcon /> Copy to…</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" class="w-56">
            <DropdownMenuLabel class="text-muted-foreground text-xs font-normal">Copy these labels to</DropdownMenuLabel>
            <DropdownMenuItem v-for="o in otherMailboxes" :key="o.id" @click="copyTo(o.id)">{{ o.displayName || o.email }}</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button variant="ghost" size="sm" :disabled="!userLabels.length" @click="exportLabels"><DownloadIcon /> Export</Button>
        <Button variant="ghost" size="sm" @click="fileInput?.click()"><UploadIcon /> Import</Button>
        <input ref="fileInput" type="file" accept="application/json,.json" class="hidden" @change="onImportFile" />
      </div>
    </SettingSection>

    <RulesSection :account-id="accountId" />

    <SettingSection :title="account?.provider === 'outlook' ? 'Outlook categories' : 'Gmail labels'">
      <template #actions>
        <span class="text-muted-foreground inline-flex items-center gap-1 text-[11px]"><ProviderIcon v-if="account" :provider="account.provider" class="size-3" /> Imported</span>
      </template>
      <SettingRow :title="`Show ${account?.provider === 'outlook' ? 'Outlook categories' : 'Gmail labels'}`" description="For every mailbox. When off, they stay in sync but are hidden.">
        <Switch :model-value="s.importProviderLabels" @update:model-value="(v: boolean) => settings.update({ importProviderLabels: v })" />
      </SettingRow>
      <template v-if="s.importProviderLabels">
        <div v-for="l in importedLabels" :key="l.id" class="group flex items-start gap-3 py-3">
          <Popover>
            <PopoverTrigger as-child>
              <button type="button" class="border-border mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md border" :aria-label="`Color of ${l.name}`">
                <span class="size-3.5 rounded-full" :style="{ background: l.color }" />
              </button>
            </PopoverTrigger>
            <PopoverContent class="w-auto p-2" align="start">
              <div class="grid grid-cols-5 gap-1.5">
                <button v-for="c in LABEL_COLORS" :key="c" type="button" class="size-7 rounded-full" :style="{ background: c }" :aria-label="c" @click="patch(l.id, { color: c })" />
              </div>
            </PopoverContent>
          </Popover>
          <div class="min-w-0 flex-1 space-y-1.5">
            <div class="flex h-8 items-center gap-2">
              <span class="truncate text-[13px] font-medium" :title="`The name comes from ${providerName}`">{{ l.name }}</span>
              <span class="bg-muted text-muted-foreground shrink-0 rounded-full px-1.5 py-0.5 text-[10.5px]">From {{ providerName }}</span>
              <Select :model-value="thenOf(l).value" @update:model-value="(v) => setThen(l, String(v))">
                <SelectTrigger
                  size="sm"
                  :class="['h-6 shrink-0 gap-1 rounded-full px-2 text-xs data-[size=sm]:h-6', thenOf(l).chip ? 'bg-muted text-foreground border-transparent' : 'text-muted-foreground border-dashed']"
                  :aria-label="`When “${l.name}” is added`"
                >
                  {{ thenOf(l).chip || 'Move mail…' }}
                </SelectTrigger>
                <SelectContent>
                  <div class="text-muted-foreground px-2 py-1.5 text-xs">{{ whenHeaderImported }}</div>
                  <SelectItem v-for="t in THEN" :key="t.value" :value="t.value">{{ t.label }}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Textarea :model-value="l.description" rows="1" :aria-label="`Description for ${l.name}`" placeholder="Add a description so Jev can use this label" class="min-h-8 resize-none py-1.5 text-xs" @update:model-value="(v) => patch(l.id, { description: String(v) })" />
          </div>
          <Button variant="ghost" size="icon-xs" class="mt-1.5 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100" :aria-label="`Hide ${l.name}`" :title="`Hide ${l.name}`" @click="askRemove(l)"><EyeOffIcon /></Button>
        </div>
        <p v-if="!importedLabels.length" class="text-muted-foreground py-3 text-xs">No {{ account?.provider === 'outlook' ? 'categories' : 'labels' }} imported from this mailbox yet.</p>
      </template>
    </SettingSection>

    <AlertDialog v-model:open="confirmDelete">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ deleting?.label.source === 'provider' ? 'Hide' : 'Delete the label' }} “{{ deleting?.label.name }}”?</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="deleting?.count">It’s removed from {{ deleting.count.toLocaleString() }} message{{ deleting.count === 1 ? '' : 's' }} in this mailbox. </template>
            <template v-else-if="deleting?.count === 0">No messages in this mailbox have it. </template>
            <template v-if="deleting?.label.source === 'provider'">It won’t be imported again for this mailbox. {{ providerName }} keeps it.</template>
            <template v-else>This can’t be undone. Other mailboxes keep their own labels.</template>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="remove">{{ deleting?.label.source === 'provider' ? 'Hide' : 'Delete label' }}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
