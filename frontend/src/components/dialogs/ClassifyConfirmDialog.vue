<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { LoaderIcon, SparklesIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { formatUSD } from '@/lib/format'
import type { ClassifyEstimate } from '@/lib/types'
import { useAiStore } from '@/stores/ai'
import { useSettingsStore } from '@/stores/settings'

const ai = useAiStore()
const settings = useSettingsStore()

const open = computed({
  get: () => !!ai.bulkRequest,
  set: (v) => { if (!v) ai.bulkRequest = null },
})
const onlyUnlabeled = ref(true)
const includeTrashDrafts = ref(false)
const estimate = ref<ClassifyEstimate | null>(null)
const estimating = ref(false)

async function refreshEstimate() {
  const req = ai.bulkRequest
  if (!req) return
  estimating.value = true
  try {
    if (req.query) estimate.value = await api.estimateClassify(req.query, onlyUnlabeled.value, includeTrashDrafts.value)
    else estimate.value = { count: req.ids.length, estimatedCostUsd: req.ids.length * 0.00011, priceKnown: false }
  } catch {
    estimate.value = null
  } finally {
    estimating.value = false
  }
}

watch(() => ai.bulkRequest, (r) => {
  if (r) {
    onlyUnlabeled.value = !!r.query
    includeTrashDrafts.value = false
    void refreshEstimate()
  }
}, { immediate: true })
watch([onlyUnlabeled, includeTrashDrafts], () => void refreshEstimate())

function start() {
  const req = ai.bulkRequest
  if (req) void ai.startBulk(req, onlyUnlabeled.value, includeTrashDrafts.value)
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-[440px]">
      <DialogHeader>
        <DialogTitle class="flex items-center gap-2"><SparklesIcon class="size-4 text-violet-500" /> Label with AI</DialogTitle>
        <DialogDescription>{{ ai.bulkRequest?.title }}</DialogDescription>
      </DialogHeader>

      <div class="bg-muted/50 grid grid-cols-2 gap-3 rounded-lg p-4">
        <div>
          <div class="text-muted-foreground text-xs">Messages</div>
          <div class="mt-0.5 text-xl font-semibold tabular-nums">
            <LoaderIcon v-if="estimating && !estimate" class="size-4 animate-spin" />
            <template v-else>{{ (estimate?.count ?? 0).toLocaleString() }}</template>
          </div>
        </div>
        <div>
          <div class="text-muted-foreground text-xs">Estimated cost</div>
          <div class="mt-0.5 text-xl font-semibold tabular-nums"><template v-if="estimate && !estimate.priceKnown">—</template><template v-else>~{{ formatUSD(estimate?.estimatedCostUsd) }}</template></div>
        </div>
        <div v-if="estimate?.requests" class="text-muted-foreground col-span-2 -mt-1 text-xs">{{ estimate.requests.toLocaleString() }} classifier requests<template v-if="estimate.estimatedTokens"> · ~{{ Math.round(estimate.estimatedTokens / 1000).toLocaleString() }}K tokens</template><template v-if="!estimate.priceKnown"> · price unknown for this model</template></div>
        <div v-if="estimate?.unchanged" class="text-muted-foreground col-span-2 text-xs">{{ estimate.unchanged.toLocaleString() }} already up to date (skipped, no cost)</div>
        <div class="text-muted-foreground col-span-2 text-xs">Model: <span class="text-foreground font-mono">{{ settings.settings?.classifierModel }}</span></div>
      </div>
      <p v-if="estimate?.exceedsBudget" class="rounded-md bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-300" role="status">
        This goes over your daily AI budget<template v-if="settings.settings?.aiDailyBudgetUsd"> of {{ formatUSD(settings.settings.aiDailyBudgetUsd) }}</template><template v-if="typeof estimate.todayUsd === 'number'"> ({{ formatUSD(estimate.todayUsd) }} spent today)</template>. You can still start it; automatic labeling will pause for the rest of the day.
      </p>

      <div class="grid gap-3">
        <Label v-if="ai.bulkRequest?.query" class="flex items-center gap-2.5 font-normal">
          <Checkbox v-model="onlyUnlabeled" /> Only unlabeled mail
        </Label>
        <Label v-if="ai.bulkRequest?.query" class="flex items-center gap-2.5 font-normal">
          <Checkbox v-model="includeTrashDrafts" /> Include Trash &amp; Drafts
        </Label>
      </div>

      <DialogFooter>
        <Button variant="ghost" @click="open = false">Cancel</Button>
        <Button :disabled="!estimate?.count" @click="start"><SparklesIcon /> Start labeling</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
