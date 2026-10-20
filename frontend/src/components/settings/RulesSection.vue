<script setup lang="ts">
// Settings → Labels → Rules. Each rule reads as a sentence:
//   When [Sender] contains [text]
//   in [All mailboxes] → [Add “GitLab”]
// Rules run on every new mail, without AI. "Apply to existing mail" adds labels to what's downloaded
// (Junk rules only act on new mail, so mail you took out of Junk stays out).
import { computed, ref } from 'vue'
import { CheckIcon, OctagonAlertIcon, PlayIcon, PlusIcon, Trash2Icon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import type { LabelRule } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const accounts = useAccountsStore()
const rules = computed(() => settings.settings?.labelRules ?? [])
// Labels a rule can add (the built-in Junk label is the "Move to Junk" action instead).
const labelOptions = computed(() => settings.labels.filter((l) => l.id !== 'junk'))
// Select items can't use "" as a value: these stand in for "every mailbox" and "Junk".
const ALL = '*'
const JUNK = '__junk'

function save(next: LabelRule[], delay = 500) {
  void settings.update({ labelRules: next }, delay)
}
function patch(id: string, p: Partial<LabelRule>) {
  status.value = ''
  save(rules.value.map((r) => (r.id === id ? { ...r, ...p } : r)))
}
function add() {
  const firstLabel = labelOptions.value.find((l) => l.source === 'user' || !l.source) ?? labelOptions.value[0]
  save([...rules.value, {
    id: `rule_${Date.now().toString(36)}`, accountId: '', field: 'from', contains: '',
    action: firstLabel ? 'label' : 'junk', labelId: firstLabel?.id ?? '',
  }], 0)
}
function remove(id: string) {
  status.value = ''
  save(rules.value.filter((r) => r.id !== id), 0)
}
function setAction(r: LabelRule, v: string) {
  patch(r.id, v === JUNK ? { action: 'junk', labelId: '' } : { action: 'label', labelId: v })
}

/** The action select's value; undefined shows "Pick a label" (none chosen yet, or the label was deleted). */
function actionValue(r: LabelRule): string | undefined {
  if (r.action === 'junk') return JUNK
  return settings.labelMap.has(r.labelId) ? r.labelId : undefined
}
/** A rule scoped to a mailbox that was removed keeps showing what it was. */
function knownAccount(r: LabelRule): boolean {
  return !r.accountId || accounts.accounts.some((a) => a.id === r.accountId)
}
function complete(r: LabelRule): boolean {
  return !!r.contains.trim() && (r.action === 'junk' || settings.labelMap.has(r.labelId))
}
const canApply = computed(() => rules.value.some((r) => r.action === 'label' && complete(r)))
const hasJunkRule = computed(() => rules.value.some((r) => r.action === 'junk' && complete(r)))

const applying = ref(false)
const status = ref('')
async function applyNow() {
  applying.value = true
  status.value = ''
  try {
    await settings.flush?.()
    const n = await api.applyLabelRules()
    status.value = n
      ? `Labeled ${n.toLocaleString()} message${n === 1 ? '' : 's'}.`
      : 'Nothing to change: matching mail already has its labels.'
    toast.success(n ? status.value : 'Rules applied', { description: n ? undefined : status.value })
  } catch (e) {
    status.value = ''
    toast.error('Couldn’t apply the rules', { description: errorMessage(e) })
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <SettingSection title="Rules" description="Label new mail by sender or subject, or move it to Junk. Rules run on every new mail, no AI needed.">
    <div
      v-for="(r, i) in rules"
      :key="r.id"
      role="group"
      :aria-label="`Rule ${i + 1}`"
      class="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 py-2.5 text-[13px]"
    >
      <div class="min-w-0 space-y-1.5">
        <div class="flex items-center gap-1.5">
          <span class="text-muted-foreground w-9 shrink-0">When</span>
          <Select :model-value="r.field" @update:model-value="(v) => patch(r.id, { field: v as LabelRule['field'] })">
            <SelectTrigger size="sm" class="w-28 shrink-0" :aria-label="`Rule ${i + 1}: look at`"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="from">Sender</SelectItem>
              <SelectItem value="subject">Subject</SelectItem>
            </SelectContent>
          </Select>
          <span class="text-muted-foreground shrink-0">contains</span>
          <Input
            :model-value="r.contains"
            class="h-8 min-w-0 flex-1"
            :placeholder="r.field === 'from' ? 'e.g. gitlab.com' : 'e.g. invoice'"
            :aria-label="`Rule ${i + 1}: ${r.field === 'from' ? 'sender' : 'subject'} contains`"
            :aria-invalid="!r.contains.trim() || undefined"
            @update:model-value="(v) => patch(r.id, { contains: String(v) })"
          />
        </div>
        <div class="flex items-center gap-1.5">
          <span class="text-muted-foreground w-9 shrink-0">in</span>
          <Select :model-value="r.accountId || ALL" @update:model-value="(v) => patch(r.id, { accountId: v === ALL ? '' : String(v) })">
            <SelectTrigger size="sm" class="w-48 min-w-0" :aria-label="`Rule ${i + 1}: mailbox`"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem :value="ALL">All mailboxes</SelectItem>
              <SelectItem v-for="a in accounts.accounts" :key="a.id" :value="a.id">{{ a.email }}</SelectItem>
              <SelectItem v-if="!knownAccount(r)" :value="r.accountId" disabled>Removed mailbox</SelectItem>
            </SelectContent>
          </Select>
          <span class="text-muted-foreground shrink-0" aria-hidden="true">→</span>
          <Select :model-value="actionValue(r)" @update:model-value="(v) => setAction(r, String(v))">
            <SelectTrigger
              size="sm"
              class="w-48 min-w-0"
              :aria-label="`Rule ${i + 1}: then`"
              :aria-invalid="actionValue(r) === undefined || undefined"
            >
              <SelectValue placeholder="Pick a label" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="l in labelOptions" :key="l.id" :value="l.id">
                <span class="size-2 shrink-0 rounded-full" :style="{ background: l.color }" aria-hidden="true" /> Add “{{ l.name }}”
              </SelectItem>
              <SelectSeparator v-if="labelOptions.length" />
              <SelectItem :value="JUNK"><OctagonAlertIcon class="size-3.5" aria-hidden="true" /> Move to Junk</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <Button
        variant="ghost"
        size="icon-sm"
        class="hover:text-destructive focus-visible:opacity-100 opacity-60 group-focus-within:opacity-100 group-hover:opacity-100"
        :aria-label="`Delete rule ${i + 1}`"
        @click="remove(r.id)"
      >
        <Trash2Icon />
      </Button>
    </div>
    <p v-if="!rules.length" class="text-muted-foreground py-2.5 text-xs">For example: when Sender contains “gitlab.com” → Add “GitLab”.</p>
    <div class="flex flex-wrap items-center gap-2 py-2.5">
      <Button variant="outline" size="sm" class="gap-1.5" @click="add"><PlusIcon /> Add rule</Button>
      <template v-if="rules.length && hasMethod('ApplyLabelRules')">
        <Button
          variant="ghost"
          size="sm"
          class="gap-1.5"
          :disabled="applying || !canApply"
          :aria-busy="applying || undefined"
          :title="canApply ? 'Add the labels of your rules to mail already downloaded' : 'Finish a label rule first'"
          @click="applyNow"
        >
          <Spinner v-if="applying" class="size-3.5" />
          <PlayIcon v-else />
          {{ applying ? 'Applying…' : 'Apply to existing mail' }}
        </Button>
        <span class="text-muted-foreground flex items-center gap-1 text-xs" aria-live="polite">
          <template v-if="status"><CheckIcon class="size-3.5 text-emerald-600" aria-hidden="true" /> {{ status }}</template>
          <template v-else-if="hasJunkRule">Junk rules only act on new mail.</template>
        </span>
      </template>
    </div>
  </SettingSection>
</template>
