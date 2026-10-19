<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, SparklesIcon, Trash2Icon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { LABEL_COLORS } from '@/lib/color'
import type { Label } from '@/lib/types'
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
          <Textarea :model-value="l.description" rows="1" placeholder="When should this label apply? e.g. “Invoices, receipts and bank statements”" class="min-h-8 resize-none py-1.5 text-xs" @update:model-value="(v) => patch(l.id, { description: String(v) })" />
        </div>
        <div class="flex items-center gap-0.5 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-xs" aria-label="Move up" :disabled="i === 0" @click="move(l.id, -1)"><ArrowUpIcon /></Button>
          <Button variant="ghost" size="icon-xs" aria-label="Move down" :disabled="i === userLabels.length - 1" @click="move(l.id, 1)"><ArrowDownIcon /></Button>
          <Button variant="ghost" size="icon-xs" class="hover:text-destructive" aria-label="Delete label" @click="remove(l.id)"><Trash2Icon /></Button>
        </div>
      </div>
      <div class="flex items-center gap-2 py-3">
        <Button variant="outline" size="sm" @click="add"><PlusIcon /> New label</Button>
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
          <ProviderIcon :provider="l.provider ?? 'gmail'" class="size-3.5" />
          {{ l.source === 'system' ? 'Built-in' : 'From' }} {{ l.provider === 'outlook' ? 'Outlook' : 'Gmail' }}
        </span>
      </div>
      <p v-if="s.importProviderLabels && !providerLabels.length" class="text-muted-foreground py-3 text-xs">No labels imported yet.</p>
    </SettingSection>
  </div>
</template>
