<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckCircle2Icon, KeyRoundIcon, LoaderIcon, RotateCcwIcon, SparklesIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { api, errorMessage, hasMethod } from '@/lib/api'
import type { AIUsage } from '@/lib/types'
import { formatUSD } from '@/lib/format'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { JEV_MODELS, useSettingsStore } from '@/stores/settings'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useMailboxSettings } from './mailboxSettings'
import ModelPicker from './ModelPicker.vue'
import ReasoningSelect from './ReasoningSelect.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const mail = useMailStore()
const ai = useAiStore()
const accounts = useAccountsStore()
const mb = useMailboxSettings()
const s = computed(() => settings.settings!)

const keyInput = ref('')
const savingKey = ref(false)
const testing = ref(false)
const testOk = ref<boolean | null>(null)

const usage = ref<AIUsage | null>(null)
onMounted(async () => {
  void settings.loadDefaults()
  if (settings.aiReady) void settings.loadModels()
  if (hasMethod('GetAIUsage')) usage.value = await api.getAIUsage().catch(() => null)
})

/** An emptied (or half-typed) field is not "0 = no limit": only real numbers are saved. */
function onBudget(v: string | number) {
  if (typeof v === 'string' && v.trim() === '') return
  const n = Number(v)
  if (!Number.isFinite(n)) return
  void settings.update({ aiDailyBudgetUsd: Math.max(0, n) }, 600)
}

async function saveKey() {
  savingKey.value = true
  try {
    await settings.setOpenRouterKey(keyInput.value.trim())
    keyInput.value = ''
    testOk.value = null
    toast.success('API key saved')
    void settings.loadModels(true)
  } catch (e) {
    toast.error('Could not save key', { description: errorMessage(e) })
  } finally {
    savingKey.value = false
  }
}
const confirmRemoveKey = ref(false)
async function removeKey() {
  await settings.setOpenRouterKey('')
  testOk.value = null
  toast('OpenRouter key removed', { description: 'AI labeling and grammar help are off until you add a key again.' })
}
async function test() {
  testing.value = true
  testOk.value = null
  try {
    await api.testOpenRouter()
    testOk.value = true
  } catch (e) {
    testOk.value = false
    toast.error('Connection failed', { description: errorMessage(e) })
  } finally {
    testing.value = false
  }
}

const classifierModel = computed({ get: () => s.value.classifierModel, set: (v: string) => settings.update({ classifierModel: v }) })
const chatModel = computed({ get: () => s.value.openRouterModel, set: (v: string) => settings.update({ openRouterModel: v }) })
const textReasoning = computed({ get: () => s.value.textAIReasoning || 'default', set: (v: string) => settings.update({ textAIReasoning: v }) })
const eventModel = computed({ get: () => s.value.eventAIModel ?? '', set: (v: string) => settings.update({ eventAIModel: v }) })
const eventReasoning = computed({ get: () => s.value.eventAIReasoning || 'low', set: (v: string) => settings.update({ eventAIReasoning: v }) })
const REASONING_HINT = 'How long a reasoning model thinks first: higher is slower and costs more. Models without reasoning ignore it.'

function labelCurrent() {
  ai.requestBulk({ query: { ...mail.query, cursor: '' }, ids: [], title: `${mail.mailboxName}${mail.mailboxAccount ? ` · ${mail.mailboxAccount.displayName}` : ''}` })
}
const pct = (v: number) => `${Math.round(v * 100)}%`
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="OpenRouter key" description="Your own key turns on the AI features. Mail is only sent to the AI to label it or to help you write.">
      <SettingRow title="API key" stacked>
        <template #description>
          <span v-if="s.openRouterKeySet" class="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400"><CheckCircle2Icon class="size-3.5" aria-hidden="true" /> A key is set. It’s kept in your system keychain and never shown again.</span>
          <span v-else>Get a key at <button type="button" class="text-foreground underline-offset-2 hover:underline" @click="api.openExternal('https://openrouter.ai/keys')">openrouter.ai/keys</button>.</span>
        </template>
        <div class="flex items-center gap-2">
          <div class="relative max-w-sm flex-1">
            <KeyRoundIcon class="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
            <Input v-model="keyInput" type="password" aria-label="OpenRouter API key" autocomplete="off" :placeholder="s.openRouterKeySet ? 'Enter a new key to replace it' : 'sk-or-v1-…'" class="pl-8 text-xs" @keydown.enter="keyInput && saveKey()" />
          </div>
          <Button size="sm" :disabled="!keyInput.trim() || savingKey" @click="saveKey">Save</Button>
          <Button v-if="s.openRouterKeySet" variant="outline" size="sm" :disabled="testing" @click="test">
            <LoaderIcon v-if="testing" class="animate-spin" /><CheckCircle2Icon v-else-if="testOk" class="text-emerald-500" />
            Test
          </Button>
          <Button v-if="s.openRouterKeySet" variant="ghost" size="sm" class="text-muted-foreground" @click="confirmRemoveKey = true">Remove</Button>
        </div>
      </SettingRow>
    </SettingSection>

    <SettingSection v-if="s.openRouterKeySet" title="Usage & budget">
      <div v-if="usage" class="grid grid-cols-2 gap-3 py-3.5 sm:grid-cols-4">
        <div v-for="c in [{ k: 'Today', v: formatUSD(usage.todayUsd) }, { k: 'This month', v: formatUSD(usage.monthUsd) }, { k: 'Requests today', v: usage.requestsToday.toLocaleString() }, { k: 'Requests this month', v: usage.requestsMonth.toLocaleString() }]" :key="c.k" class="bg-muted/50 rounded-lg px-3 py-2.5">
          <div class="text-muted-foreground text-[11px]">{{ c.k }}</div>
          <div class="mt-0.5 text-base font-semibold tabular-nums">{{ c.v }}</div>
        </div>
      </div>
      <p v-if="usage?.budgetReached" class="mb-1 rounded-md bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-300">Today’s budget is used up, so automatic labeling is paused until tomorrow. Manual labeling still works.</p>
      <SettingRow title="Daily budget" description="Automatic labeling stops for the day once AI costs reach this. 0 means no limit.">
        <div class="flex items-center gap-1.5">
          <span class="text-muted-foreground text-sm">$</span>
          <Input type="number" min="0" step="0.25" class="h-8 w-24 text-right tabular-nums" placeholder="0" :model-value="s.aiDailyBudgetUsd ?? 1" aria-label="Daily AI budget in US dollars" @update:model-value="onBudget" />
        </div>
      </SettingRow>
    </SettingSection>

    <SettingSection title="AI labeling" description="Jev is the AI that sorts your mail into labels.">
      <p v-if="!s.openRouterKeySet" class="text-muted-foreground py-2 text-xs">Add an OpenRouter key above to use AI labeling.</p>
      <SettingRow title="Label new mail automatically in" description="Jev labels new mail in these mailboxes as it arrives." stacked>
        <p v-if="!accounts.accounts.length" class="text-muted-foreground text-xs">Add a mailbox first.</p>
        <ul v-else class="space-y-1.5" aria-label="Auto-labeling per mailbox">
          <li v-for="a in accounts.accounts" :key="a.id" class="flex items-center gap-3">
            <Switch :model-value="mb.autoLabels(a.id)" :aria-label="`Auto-label new mail in ${a.email}`" @update:model-value="(v: boolean) => mb.setAutoLabel(a.id, v)" />
            <AccountAvatar :account="a" :size="18" :glyph="false" />
            <span class="min-w-0 truncate text-[13px]" :class="!mb.autoLabels(a.id) && 'text-muted-foreground'" :title="a.email">{{ a.email }}</span>
          </li>
        </ul>
      </SettingRow>
    </SettingSection>

    <SettingSection title="How Jev labels" all-mailboxes>
      <SettingRow title="Labeling model" stacked>
        <ModelPicker v-model="classifierModel" kind="decision" :recommended="JEV_MODELS" recommended-kind="decision" recommended-heading="Recommended for labeling" class="max-w-md" />
      </SettingRow>
      <SettingRow title="How sure Jev must be to add a label" stacked>
        <template #description>Higher means fewer labels, but fewer mistakes.</template>
        <div class="flex max-w-md items-center gap-3">
          <Slider :model-value="[s.classifyThreshold]" :min="0.05" :max="0.95" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ classifyThreshold: v[0] })" />
          <span class="w-10 text-right text-xs tabular-nums">{{ pct(s.classifyThreshold) }}</span>
        </div>
      </SettingRow>
      <SettingRow title="Let Jev remove wrong Gmail/Outlook labels" description="Only imported labels it’s sure are wrong, never your own. Each mailbox can turn this off.">
        <Switch :model-value="s.aiCorrectsProviderLabels" @update:model-value="(v: boolean) => settings.update({ aiCorrectsProviderLabels: v })" />
      </SettingRow>
      <SettingRow v-if="s.aiCorrectsProviderLabels" title="Remove an imported label when Jev is below" stacked>
        <template #description>Lower means Jev removes fewer labels.</template>
        <div class="flex max-w-md items-center gap-3">
          <Slider :model-value="[s.classifyRemoveThreshold]" :min="0" :max="0.5" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ classifyRemoveThreshold: v[0] })" />
          <span class="w-10 text-right text-xs tabular-nums">{{ pct(s.classifyRemoveThreshold) }}</span>
        </div>
      </SettingRow>
      <SettingRow title="Labeling instructions" stacked>
        <template #description>
          <span class="flex items-center justify-between gap-2">
            Extra guidance for Jev. Each label’s description is added for you.
            <button type="button" class="text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1" :disabled="!settings.defaults.classify" @click="settings.update({ classifyInstructions: settings.defaults.classify })"><RotateCcwIcon class="size-3" aria-hidden="true" /> Reset to default</button>
          </span>
        </template>
        <Textarea :model-value="s.classifyInstructions || settings.defaults.classify" rows="3" class="text-xs" placeholder="Leave empty to use the built-in instructions" @update:model-value="(v) => settings.update({ classifyInstructions: String(v) }, 600)" />
      </SettingRow>
      <div class="flex items-center gap-3 py-3.5">
        <Button variant="outline" size="sm" :disabled="!s.openRouterKeySet || (!!ai.job && !ai.job.finished)" :title="!s.openRouterKeySet ? 'Add an OpenRouter key first' : ai.job && !ai.job.finished ? 'A labeling run is in progress' : undefined" @click="labelCurrent"><SparklesIcon class="text-violet-500" /> Label current folder…</Button>
        <span v-if="ai.job" class="text-muted-foreground text-xs tabular-nums" aria-live="polite">
          {{ ai.job.finished ? 'Done' : 'Running' }} · {{ ai.job.done }}/{{ ai.job.total || '…' }} · {{ formatUSD(ai.job.cost) }}
        </span>
      </div>
    </SettingSection>

    <SettingSection title="Writing & summaries" description="To fix grammar while writing, right-click the text and choose “Improve grammar”." all-mailboxes>
      <p v-if="!s.openRouterKeySet" class="text-muted-foreground py-2 text-xs">Add an OpenRouter key above to use writing help.</p>
      <SettingRow title="Text model" description="Fixes grammar and summarizes mail." stacked>
        <ModelPicker v-model="chatModel" kind="chat" class="max-w-md" />
      </SettingRow>
      <SettingRow title="Reasoning" :description="REASONING_HINT">
        <ReasoningSelect v-model="textReasoning" label="Text model reasoning" />
      </SettingRow>
      <SettingRow title="Grammar instructions" stacked>
        <template #description>
          <span class="flex items-center justify-between gap-2">
            Sent along with the text you want improved.
            <button type="button" class="text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1" :disabled="!settings.defaults.grammar" @click="settings.update({ grammarSystemPrompt: settings.defaults.grammar })"><RotateCcwIcon class="size-3" aria-hidden="true" /> Reset to default</button>
          </span>
        </template>
        <Textarea :model-value="s.grammarSystemPrompt || settings.defaults.grammar" rows="4" class="text-xs" @update:model-value="(v) => settings.update({ grammarSystemPrompt: String(v) }, 600)" />
      </SettingRow>
    </SettingSection>

    <SettingSection v-if="hasMethod('ParseEventText')" title="Calendar events" description="“Fill in” on a new event turns a sentence like “lunch with Sam friday 12:30” into its date, time, place and guests." all-mailboxes>
      <SettingRow title="Event model" stacked>
        <template #description>
          <span class="flex items-center justify-between gap-2">
            {{ eventModel ? 'Used only to fill in events.' : 'Uses the text model unless you pick another one.' }}
            <button v-if="eventModel" type="button" class="text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1" @click="eventModel = ''"><RotateCcwIcon class="size-3" aria-hidden="true" /> Use the text model</button>
          </span>
        </template>
        <ModelPicker v-model="eventModel" kind="chat" placeholder="Same as the text model" class="max-w-md" />
      </SettingRow>
      <SettingRow title="Reasoning" :description="`${REASONING_HINT} Low keeps filling in quick.`">
        <ReasoningSelect v-model="eventReasoning" label="Event model reasoning" />
      </SettingRow>
    </SettingSection>

    <AlertDialog v-model:open="confirmRemoveKey">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove your OpenRouter key?</AlertDialogTitle>
          <AlertDialogDescription>AI labeling, automatic labeling and grammar help stop working until you add a key again. Labels already applied stay.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="removeKey">Remove key</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
