<script setup lang="ts">
import { computed, ref } from 'vue'
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
import type { MessageSummary } from '@/lib/types'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{ message: MessageSummary }>()
const mail = useMailStore()
const settings = useSettingsStore()
const ai = useAiStore()
const ui = useUiStore()
const q = ref('')

const items = computed(() => fuzzyFilter(q.value, settings.labels, (l) => l.name, 100))
const details = computed(() => new Map((props.message.labelDetails ?? []).map((d) => [d.labelId, d])))
const scores = computed(() => ai.scores[props.message.id] ?? {})

function scoreOf(id: string): number | null {
  if (scores.value[id] !== undefined) return scores.value[id]
  const d = details.value.get(id)
  return d?.source === 'ai' ? d.confidence : null
}

function toggle(id: string) {
  if (props.message.labels.includes(id)) void mail.removeLabel([props.message.id], id, true)
  else void mail.addLabel([props.message.id], id, true)
}

const canCreate = computed(() => q.value.trim() && !settings.labels.some((l) => l.name.toLowerCase() === q.value.trim().toLowerCase()))
async function create() {
  const name = q.value.trim()
  if (!name || !settings.settings) return
  const id = `lbl_${Date.now().toString(36)}`
  const color = LABEL_COLORS[settings.labels.length % LABEL_COLORS.length]
  await settings.update({ labels: [...settings.labels, { id, name, description: '', color, source: 'user' }] }, 0)
  q.value = ''
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
            <span class="flex size-4 items-center justify-center rounded border" :class="message.labels.includes(l.id) ? 'border-transparent' : 'border-border'" :style="message.labels.includes(l.id) ? { background: l.color } : {}">
              <CheckIcon v-if="message.labels.includes(l.id)" class="size-3 text-white" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="flex items-center gap-1.5">
                <span class="truncate">{{ l.name }}</span>
                <SparklesIcon v-if="details.get(l.id)?.source === 'ai'" class="size-3 text-violet-500" />
                <span v-if="l.source && l.source !== 'user'" class="text-muted-foreground text-[10px]">{{ l.provider === 'outlook' ? 'Outlook' : 'Gmail' }}</span>
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
            <span>Create “{{ q.trim() }}”</span>
          </CmdItem>
          <div v-if="!settings.labels.length && !q" class="text-muted-foreground px-3 py-6 text-center text-xs">Type to create your first label.</div>
        </CmdList>
        <div class="border-border flex items-center justify-between border-t px-2 py-1.5">
          <Button variant="ghost" size="xs" class="text-muted-foreground" @click="ui.labelPopoverOpen = false; ui.openSettings('labels')">Manage labels</Button>
          <Button v-if="settings.aiReady" variant="ghost" size="xs" :disabled="ai.labeling.has(message.id)" @click="ai.labelMessages([message.id])">
            <SparklesIcon class="text-violet-500" /> Label with AI
          </Button>
        </div>
      </CmdRoot>
    </PopoverContent>
  </Popover>
</template>
