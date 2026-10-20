<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckIcon, PlusIcon, SparklesIcon, TagIcon } from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import CmdRoot from '@/components/common/cmd/CmdRoot.vue'
import CmdInput from '@/components/common/cmd/CmdInput.vue'
import CmdList from '@/components/common/cmd/CmdList.vue'
import CmdItem from '@/components/common/cmd/CmdItem.vue'
import Tip from '@/components/common/Tip.vue'
import { fuzzyFilter } from '@/lib/fuzzy'
import { LABEL_COLORS } from '@/lib/color'
import { labelInAccount, newLabelAccounts } from '@/lib/labelScope'
import type { MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{ message: MessageSummary }>()
const mail = useMailStore()
const settings = useSettingsStore()
const ai = useAiStore()
const ui = useUiStore()
const accounts = useAccountsStore()
const q = ref('')

// The labels of the message's mailbox, plus any it already has (so they can be taken off).
const offered = computed(() => settings.labels.filter((l) => labelInAccount(l, props.message.accountId) || props.message.labels.includes(l.id)))
const items = computed(() => fuzzyFilter(q.value, offered.value, (l) => l.name, 100))
// The message's labels as changed here: the row may leave the list (a label filter) and the
// thread copy isn't refreshed, so the props alone would go stale.
const local = ref<string[] | null>(null)
watch(() => props.message.id, () => (local.value = null))
watch(() => props.message.labels, () => (local.value = null))
const labels = computed(() => local.value ?? props.message.labels)
const details = computed(() => new Map((props.message.labelDetails ?? []).map((d) => [d.labelId, d])))
const scores = computed(() => ai.scores[props.message.id] ?? {})

function scoreOf(id: string): number | null {
  if (scores.value[id] !== undefined) return scores.value[id]
  const d = details.value.get(id)
  return d?.source === 'ai' ? d.confidence : null
}

function toggle(id: string) {
  const has = labels.value.includes(id)
  local.value = has ? labels.value.filter((l) => l !== id) : [...labels.value, id]
  if (has) void mail.removeLabel([props.message.id], id, true)
  else void mail.addLabel([props.message.id], id, true)
}

// A label of another mailbox with the typed name: "create" adds this mailbox to it (names are unique).
const elsewhere = computed(() => {
  const n = q.value.trim().toLowerCase()
  return settings.labels.find((l) => l.name.toLowerCase() === n && !offered.value.includes(l) && l.source === 'user') ?? null
})
const canCreate = computed(() => q.value.trim() && (!!elsewhere.value || !settings.labels.some((l) => l.name.toLowerCase() === q.value.trim().toLowerCase())))
async function create() {
  const name = q.value.trim()
  if (!name || !settings.settings) return
  const acct = props.message.accountId
  const other = elsewhere.value
  let id: string
  if (other) {
    id = other.id
    await settings.update({ labels: settings.labels.map((l) => (l.id === id ? { ...l, accountIds: [...(l.accountIds ?? []), acct] } : l)) }, 0)
  } else {
    id = `lbl_${Date.now().toString(36)}`
    const color = LABEL_COLORS[settings.labels.length % LABEL_COLORS.length]
    // New labels belong to the mailbox being viewed (all of them in the unified view), and always to this message's.
    const all = accounts.accounts.map((a) => a.id)
    const accountIds = [...new Set([...newLabelAccounts(mail.mailbox.accountId, all), acct].filter(Boolean))]
    await settings.update({ labels: [...settings.labels, { id, name, description: '', color, source: 'user', accountIds }] }, 0)
  }
  q.value = ''
  local.value = [...labels.value, id]
  void mail.addLabel([props.message.id], id, true)
}
</script>

<template>
  <Popover v-model:open="ui.labelPopoverOpen">
    <PopoverTrigger as-child>
      <Tip label="Labels" shortcut="l">
        <Button variant="ghost" size="icon-sm" aria-label="Labels"><TagIcon /></Button>
      </Tip>
    </PopoverTrigger>
    <PopoverContent align="start" class="w-72 gap-0 p-0">
      <CmdRoot>
        <CmdInput v-model="q" placeholder="Add or find a label…" auto-focus @keydown.enter="canCreate && !items.length && create()" />
        <CmdList class="max-h-80">
          <CmdItem v-for="l in items" :key="l.id" :value="l.id" class="py-2" @select="toggle(l.id)">
            <span class="flex size-4 items-center justify-center rounded border" :class="labels.includes(l.id) ? 'border-transparent' : 'border-border'" :style="labels.includes(l.id) ? { background: l.color } : {}">
              <CheckIcon v-if="labels.includes(l.id)" class="size-3 text-white" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="flex items-center gap-1.5">
                <span class="truncate">{{ l.name }}</span>
                <span v-if="details.get(l.id)?.source === 'ai'" title="Added by AI" class="inline-flex"><SparklesIcon class="size-3 text-violet-500" aria-label="Added by AI" /></span>
                <span v-if="l.source && l.source !== 'user'" class="text-muted-foreground text-[10px]">{{ l.source === 'system' ? 'built-in' : l.provider === 'outlook' ? 'Outlook' : l.provider === 'gmail' ? 'Gmail' : 'imported' }}</span>
              </span>
              <span v-if="scoreOf(l.id) !== null" class="mt-1 flex items-center gap-1.5" :title="`AI score ${Math.round(scoreOf(l.id)! * 100)}%`">
                <span class="bg-muted h-1 flex-1 overflow-hidden rounded-full">
                  <span class="block h-full rounded-full" :style="{ width: `${Math.round(scoreOf(l.id)! * 100)}%`, background: l.color }" />
                </span>
                <span class="text-muted-foreground w-8 text-right text-[10px] tabular-nums">{{ Math.round(scoreOf(l.id)! * 100) }}%</span>
              </span>
            </span>
          </CmdItem>
          <CmdItem v-if="canCreate" value="__create" @select="create">
            <PlusIcon class="text-muted-foreground size-4" />
            <span>{{ elsewhere ? `Use “${elsewhere.name}” in this mailbox` : `Create “${q.trim()}”` }}</span>
          </CmdItem>
          <div v-if="!offered.length && !q" class="text-muted-foreground px-3 py-6 text-center text-xs">Type to create your first label.</div>
        </CmdList>
        <div class="border-border flex items-center justify-between border-t px-2 py-1.5">
          <Button variant="ghost" size="xs" class="text-muted-foreground" @click="ui.labelPopoverOpen = false; ui.openSettings('labels')">Manage labels</Button>
          <Button variant="ghost" size="xs" :disabled="ai.labeling.has(message.id)" @click="ai.labelMessages([message.id])">
            <SparklesIcon class="text-violet-500" /> Label with AI
          </Button>
        </div>
      </CmdRoot>
    </PopoverContent>
  </Popover>
</template>
