<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckIcon, ChevronsUpDownIcon, PencilIcon, RefreshCwIcon } from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import CmdRoot from '@/components/common/cmd/CmdRoot.vue'
import CmdInput from '@/components/common/cmd/CmdInput.vue'
import CmdList from '@/components/common/cmd/CmdList.vue'
import CmdGroup from '@/components/common/cmd/CmdGroup.vue'
import CmdItem from '@/components/common/cmd/CmdItem.vue'
import { fuzzyFilter } from '@/lib/fuzzy'
import { formatContext } from '@/lib/format'
import type { AIModel } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'

const props = withDefaults(defineProps<{ recommended?: AIModel[]; recommendedHeading?: string; recommendedKind?: string; placeholder?: string; kind?: string }>(), {
  recommended: () => [], recommendedHeading: 'Recommended', recommendedKind: '', placeholder: 'Choose a model', kind: '',
})
const model = defineModel<string>({ required: true })
const settings = useSettingsStore()
const open = ref(false)
const q = ref('')
const CAP = 80

watch(open, (o) => {
  if (o) {
    q.value = ''
    void settings.loadModels()
  }
})

const all = computed(() => (props.kind ? settings.models.filter((m) => !m.kind || m.kind === props.kind) : settings.models))
const filtered = computed(() => fuzzyFilter(q.value, all.value, (m) => `${m.name} ${m.id}`, 100000))
const shown = computed(() => filtered.value.slice(0, CAP))
// Recommended = pinned models plus any listed models of the same kind (e.g. "decision" for Jev).
const recAll = computed(() => {
  if (!props.recommended.length) return []
  const ids = new Set(props.recommended.map((m) => m.id))
  const extra = all.value.filter((m) => props.recommendedKind && m.kind === props.recommendedKind && !ids.has(m.id))
  return [...props.recommended.map((r) => all.value.find((m) => m.id === r.id) ?? r), ...extra]
})
const rec = computed(() => (q.value ? fuzzyFilter(q.value, recAll.value, (m) => `${m.name} ${m.id}`) : recAll.value))
const selected = computed<AIModel | null>(() => settings.models.find((m) => m.id === model.value) ?? props.recommended.find((m) => m.id === model.value) ?? null)
const showCustom = computed(() => {
  const s = q.value.trim()
  return s.length > 2 && /^[~\w.\-/:]+$/.test(s) && !all.value.some((m) => m.id === s) && !props.recommended.some((m) => m.id === s)
})

function price(n: number) {
  return n < 0.01 && n > 0 ? `$${n.toFixed(4)}` : `$${n.toFixed(2)}`
}
function priceLine(m: AIModel): string {
  if (m.priceUnknown) return 'Price unknown'
  if (!m.promptPrice && !m.completionPrice) return props.recommended.some((r) => r.id === m.id) ? 'Priced per decision' : 'Free'
  return `${price(m.promptPrice)} / 1M in · ${price(m.completionPrice)} / 1M out`
}

function pick(id: string) {
  model.value = id
  open.value = false
}
</script>

<template>
  <div class="space-y-1.5">
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button variant="outline" role="combobox" :aria-expanded="open" class="h-auto min-h-9 w-full justify-between py-1.5 font-normal">
          <span v-if="model" class="min-w-0 text-left">
            <span class="block truncate text-[13px] font-medium">{{ selected?.name ?? model }}</span>
            <span v-if="selected" class="text-muted-foreground block truncate font-mono text-[11px]">{{ model }}</span>
          </span>
          <span v-else class="text-muted-foreground">{{ placeholder }}</span>
          <ChevronsUpDownIcon class="text-muted-foreground size-4 shrink-0" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" class="w-(--reka-popover-trigger-width) min-w-[420px] gap-0 p-0">
        <CmdRoot>
          <CmdInput v-model="q" placeholder="Search by name or id…" auto-focus>
            <template #end>
              <button type="button" class="text-muted-foreground hover:text-foreground rounded p-1" title="Refresh model list" aria-label="Refresh model list" @click="settings.loadModels(true)">
                <RefreshCwIcon class="size-3.5" :class="settings.modelsLoading && 'animate-spin'" />
              </button>
            </template>
          </CmdInput>
          <CmdList class="max-h-80">
            <CmdGroup v-if="rec.length" :heading="recommendedHeading">
              <CmdItem v-for="m in rec" :key="m.id" :value="m.id" @select="pick(m.id)">
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-[13px] font-medium">{{ m.name }}</span>
                  <span class="text-muted-foreground block truncate font-mono text-[11px]">{{ m.id }}</span>
                </span>
                <span class="text-muted-foreground text-[11px]">{{ m.description }}</span>
                <CheckIcon class="size-4" :class="m.id === model ? 'opacity-100' : 'opacity-0'" />
              </CmdItem>
            </CmdGroup>
            <CmdGroup :heading="rec.length ? 'All models' : undefined">
              <CmdItem v-for="m in shown" :key="m.id" :value="m.id" @select="pick(m.id)">
                <span class="min-w-0 flex-1">
                  <span class="flex items-center gap-2">
                    <span class="truncate text-[13px] font-medium">{{ m.name }}</span>
                    <span v-if="m.contextLength" class="bg-muted text-muted-foreground shrink-0 rounded px-1 text-[10px] font-medium">{{ formatContext(m.contextLength) }}</span>
                  </span>
                  <span class="text-muted-foreground flex items-center gap-2 text-[11px]">
                    <span class="truncate font-mono">{{ m.id }}</span>
                    <span class="ml-auto shrink-0 tabular-nums">{{ priceLine(m) }}</span>
                  </span>
                </span>
                <CheckIcon class="size-4" :class="m.id === model ? 'opacity-100' : 'opacity-0'" />
              </CmdItem>
              <div v-if="settings.modelsLoading && !all.length" class="text-muted-foreground px-3 py-6 text-center text-xs">Loading models…</div>
              <div v-else-if="settings.modelsError && !all.length" class="text-destructive px-3 py-6 text-center text-xs">{{ settings.modelsError }}</div>
              <div v-else-if="filtered.length > CAP" class="text-muted-foreground px-3 py-2 text-center text-[11px]">Showing {{ CAP }} of {{ filtered.length }} — refine your search</div>
            </CmdGroup>
            <CmdItem v-if="showCustom" :value="`custom:${q}`" @select="pick(q.trim())">
              <PencilIcon class="text-muted-foreground size-4" />
              <span class="truncate">Use custom model <span class="font-mono">{{ q.trim() }}</span></span>
            </CmdItem>
            <div v-if="!filtered.length && !rec.length && !showCustom && all.length" class="text-muted-foreground px-3 py-6 text-center text-xs">No models match</div>
          </CmdList>
        </CmdRoot>
      </PopoverContent>
    </Popover>
    <p v-if="selected" class="text-muted-foreground text-xs">{{ priceLine(selected) }}<template v-if="selected.contextLength"> · {{ formatContext(selected.contextLength) }} context</template></p>
  </div>
</template>
