<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckCircle2Icon, KeyRoundIcon, LoaderIcon, RotateCcwIcon, SparklesIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { api, errorMessage, hasMethod } from '@/lib/api'
import type { AIUsage } from '@/lib/types'
import { formatUSD } from '@/lib/format'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { JEV_MODELS, useSettingsStore } from '@/stores/settings'
import ModelPicker from './ModelPicker.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const settings = useSettingsStore()
const mail = useMailStore()
const ai = useAiStore()
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
async function removeKey() {
  await settings.setOpenRouterKey('')
  testOk.value = null
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

function labelCurrent() {
  ai.requestBulk({ query: { ...mail.query, cursor: '' }, ids: [], title: `${mail.mailboxName}${mail.mailboxAccount ? ` · ${mail.mailboxAccount.displayName}` : ''}` })
}
const pct = (v: number) => `${Math.round(v * 100)}%`
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="OpenRouter" description="AI features are optional and use your own OpenRouter key. Mail is sent to the model only when labeling or improving text.">
      <SettingRow title="API key" stacked>
        <template #description>
          <span v-if="s.openRouterKeySet" class="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400"><CheckCircle2Icon class="size-3.5" /> A key is set. It is stored in your system keychain and never shown again.</span>
          <span v-else>Get a key at openrouter.ai/keys.</span>
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
          <Button v-if="s.openRouterKeySet" variant="ghost" size="sm" class="text-muted-foreground" @click="removeKey">Remove</Button>
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
      <SettingRow title="Daily budget" description="Auto-labeling pauses for the day when AI spend reaches this. 0 = no limit.">
        <div class="flex items-center gap-1.5">
          <span class="text-muted-foreground text-sm">$</span>
          <Input type="number" min="0" step="0.25" class="h-8 w-24 text-right tabular-nums" :model-value="s.aiDailyBudgetUsd ?? 1" aria-label="Daily AI budget in US dollars" @update:model-value="onBudget" />
        </div>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Labeling (Jev)" description="Jev is a decision model that picks labels from the ones you define in Settings → Labels.">
      <SettingRow title="Classifier model" stacked>
        <ModelPicker v-model="classifierModel" :recommended="JEV_MODELS" recommended-kind="decision" recommended-heading="Recommended for labeling" class="max-w-md" />
      </SettingRow>
      <SettingRow title="Add label at" stacked>
        <template #description>Labels scoring below this confidence are left unlabeled.</template>
        <div class="flex max-w-md items-center gap-3">
          <Slider :model-value="[s.classifyThreshold]" :min="0.05" :max="0.95" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ classifyThreshold: v[0] })" />
          <span class="w-10 text-right text-xs tabular-nums">{{ pct(s.classifyThreshold) }}</span>
        </div>
      </SettingRow>
      <SettingRow title="Let AI correct Gmail/Outlook labels" description="Jev may remove imported labels it is confident don’t apply. Labels you set yourself are never removed.">
        <Switch :model-value="s.aiCorrectsProviderLabels" @update:model-value="(v: boolean) => settings.update({ aiCorrectsProviderLabels: v })" />
      </SettingRow>
      <SettingRow v-if="s.aiCorrectsProviderLabels" title="Remove label below" stacked>
        <template #description>Imported labels scoring below this are removed.</template>
        <div class="flex max-w-md items-center gap-3">
          <Slider :model-value="[s.classifyRemoveThreshold]" :min="0" :max="0.5" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ classifyRemoveThreshold: v[0] })" />
          <span class="w-10 text-right text-xs tabular-nums">{{ pct(s.classifyRemoveThreshold) }}</span>
        </div>
      </SettingRow>
      <SettingRow title="Auto-label new mail" description="Run Jev on incoming mail after each sync.">
        <Switch :model-value="s.autoClassify" @update:model-value="(v: boolean) => settings.update({ autoClassify: v })" />
      </SettingRow>
      <SettingRow title="Classification instructions" stacked>
        <template #description>
          <span class="flex items-center justify-between gap-2">
            General guidance for Jev; each label's description is added automatically.
            <button type="button" class="text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1" :disabled="!settings.defaults.classify" @click="settings.update({ classifyInstructions: settings.defaults.classify })"><RotateCcwIcon class="size-3" /> Reset to default</button>
          </span>
        </template>
        <Textarea :model-value="s.classifyInstructions || settings.defaults.classify" rows="3" class="text-xs" @update:model-value="(v) => settings.update({ classifyInstructions: String(v) }, 600)" />
      </SettingRow>
      <div class="flex items-center gap-3 py-3.5">
        <Button variant="outline" size="sm" :disabled="!s.openRouterKeySet || (!!ai.job && !ai.job.finished)" @click="labelCurrent"><SparklesIcon class="text-violet-500" /> Label current folder…</Button>
        <span v-if="ai.job" class="text-muted-foreground text-xs tabular-nums">
          {{ ai.job.finished ? 'Done' : 'Running' }} · {{ ai.job.done }}/{{ ai.job.total || '…' }} · {{ formatUSD(ai.job.cost) }}
        </span>
      </div>
    </SettingSection>

    <SettingSection title="Grammar" description="Right-click the subject or message body while composing and choose “Improve grammar”.">
      <SettingRow title="Text model" stacked>
        <ModelPicker v-model="chatModel" kind="chat" class="max-w-md" />
      </SettingRow>
      <SettingRow title="Grammar system prompt" stacked>
        <template #description>
          <span class="flex items-center justify-between gap-2">
            Instructions sent with the text to improve.
            <button type="button" class="text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1" :disabled="!settings.defaults.grammar" @click="settings.update({ grammarSystemPrompt: settings.defaults.grammar })"><RotateCcwIcon class="size-3" /> Reset to default</button>
          </span>
        </template>
        <Textarea :model-value="s.grammarSystemPrompt || settings.defaults.grammar" rows="4" class="text-xs" @update:model-value="(v) => settings.update({ grammarSystemPrompt: String(v) }, 600)" />
      </SettingRow>
      <Badge v-if="!s.openRouterKeySet" variant="secondary" class="my-3">Add an API key above to enable grammar help</Badge>
    </SettingSection>
  </div>
</template>
