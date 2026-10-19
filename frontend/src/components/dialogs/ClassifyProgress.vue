<script setup lang="ts">
import { computed, watch } from 'vue'
import { CheckCircle2Icon, SparklesIcon, XIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { formatUSD } from '@/lib/format'
import { useAiStore } from '@/stores/ai'

const ai = useAiStore()
const job = computed(() => ai.job)
const pct = computed(() => (job.value && job.value.total ? Math.round((job.value.done / job.value.total) * 100) : 0))

// Auto-dismiss a finished job after a while.
watch(() => job.value?.finished, (done) => {
  if (done) setTimeout(() => { if (ai.job?.finished) ai.dismissJob() }, 9000)
})
</script>

<template>
  <Transition
    enter-from-class="opacity-0 translate-y-2" enter-active-class="transition duration-200"
    leave-to-class="opacity-0 translate-y-2" leave-active-class="transition duration-150"
  >
    <div v-if="job" class="bg-popover text-popover-foreground border-border fixed bottom-4 left-[72px] z-40 w-72 rounded-xl border p-3.5 shadow-xl" role="status" aria-live="polite">
      <div class="flex items-center gap-2">
        <CheckCircle2Icon v-if="job.finished && !job.cancelled" class="size-4 text-emerald-500" />
        <SparklesIcon v-else class="size-4 text-violet-500" :class="!job.finished && 'animate-pulse'" />
        <span class="flex-1 text-[13px] font-medium">
          {{ job.finished ? (job.cancelled ? 'Labeling cancelled' : job.error ? 'Labeling failed' : 'Labeling complete') : 'Labeling with AI…' }}
        </span>
        <button type="button" class="text-muted-foreground hover:text-foreground rounded p-0.5" aria-label="Dismiss" @click="job.finished ? ai.dismissJob() : ai.cancelJob()">
          <XIcon class="size-3.5" />
        </button>
      </div>
      <div class="bg-muted mt-3 h-1.5 overflow-hidden rounded-full">
        <div class="h-full rounded-full bg-gradient-to-r from-violet-500 to-[var(--primary)] transition-[width] duration-300" :style="{ width: `${job.finished ? 100 : pct}%` }" />
      </div>
      <div class="text-muted-foreground mt-2 flex items-center justify-between text-xs tabular-nums">
        <span>{{ job.done.toLocaleString() }} / {{ job.total ? job.total.toLocaleString() : '…' }}</span>
        <span>{{ formatUSD(job.cost) }}</span>
      </div>
      <div class="text-muted-foreground mt-1 text-xs tabular-nums">
        {{ job.labeled }} labeled · +{{ job.added ?? 0 }} added · −{{ job.removed ?? 0 }} removed · {{ job.skipped }} skipped
      </div>
      <p v-if="job.error" class="text-destructive mt-1 text-xs">{{ job.error }}</p>
      <div v-if="!job.finished" class="mt-2.5 flex justify-end">
        <Button variant="outline" size="xs" :disabled="job.cancelled" @click="ai.cancelJob()">{{ job.cancelled ? 'Cancelling…' : 'Cancel' }}</Button>
      </div>
    </div>
  </Transition>
</template>
