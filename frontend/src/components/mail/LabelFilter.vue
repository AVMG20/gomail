<script setup lang="ts">
import { computed, ref } from 'vue'
import { CheckIcon, EyeOffIcon, ListFilterIcon, TagIcon } from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import CmdRoot from '@/components/common/cmd/CmdRoot.vue'
import CmdInput from '@/components/common/cmd/CmdInput.vue'
import CmdList from '@/components/common/cmd/CmdList.vue'
import CmdItem from '@/components/common/cmd/CmdItem.vue'
import Tip from '@/components/common/Tip.vue'
import { fuzzyFilter } from '@/lib/fuzzy'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const mail = useMailStore()
const settings = useSettingsStore()
const accounts = useAccountsStore()
const ui = useUiStore()
const open = ref(false)
const q = ref('')
/** What picking a label does: show only mail with it, or hide mail with it. */
const mode = ref<'show' | 'hide'>('show')

function onOpen(v: boolean) {
  if (v) mode.value = mail.labelFilter.hidden.length && !mail.labelFilter.labels.length && !mail.labelFilter.unlabeled ? 'hide' : 'show'
}
function pick(id: string) {
  if (mode.value === 'hide') mail.toggleHiddenLabel(id)
  else mail.toggleLabelFilter(id)
}

const items = computed(() => fuzzyFilter(q.value, settings.labels, (l) => l.name, 100))
const activeCount = computed(() => mail.labelFilter.labels.length + mail.labelFilter.hidden.length + (mail.labelFilter.unlabeled ? 1 : 0))
</script>

<template>
  <Popover v-model:open="open" @update:open="onOpen">
    <PopoverTrigger as-child>
      <Tip label="Filter by label">
        <Button variant="ghost" size="sm" :class="['text-muted-foreground gap-1.5 px-2', activeCount && 'text-foreground bg-muted']">
          <ListFilterIcon class="size-4" />
          <span class="sr-only">Filter by</span>
          <span class="sr-only text-xs font-medium @[440px]:not-sr-only">Labels</span>
          <span v-if="activeCount" class="sr-only">, {{ activeCount }} active</span>
          <span v-if="activeCount" aria-hidden="true" class="bg-primary text-primary-foreground min-w-4 rounded-full px-1 text-[10px] leading-4 font-semibold">{{ activeCount }}</span>
        </Button>
      </Tip>
    </PopoverTrigger>
    <PopoverContent align="end" class="w-64 gap-0 p-0">
      <CmdRoot>
        <CmdInput v-model="q" placeholder="Filter labels…" auto-focus />
        <div class="px-2 pt-2" role="radiogroup" aria-label="Picking a label">
          <div class="bg-muted grid grid-cols-2 rounded-md p-0.5">
            <button
              v-for="o in [{ v: 'show', l: 'Show only' }, { v: 'hide', l: 'Hide' }] as const"
              :key="o.v"
              type="button"
              role="radio"
              :aria-checked="mode === o.v"
              :class="['rounded px-2 py-1 text-xs font-medium transition-colors', mode === o.v ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']"
              @click="mode = o.v"
            >{{ o.l }}</button>
          </div>
        </div>
        <CmdList class="max-h-72">
          <div v-if="items.length" class="text-muted-foreground flex justify-between px-2 pt-1.5 pb-1 text-[10.5px] font-medium tracking-wide uppercase"><span>Labels</span><span>Messages</span></div>
          <CmdItem v-for="l in items" :key="l.id" :value="l.id" @select="pick(l.id)">
            <span class="size-2.5 rounded-full" :style="{ background: l.color }" />
            <span class="flex-1 truncate">{{ l.name }}</span>
            <span class="text-muted-foreground text-xs tabular-nums" :aria-label="`${accounts.labelCounts.get(l.id)?.total ?? 0} messages`">{{ accounts.labelCounts.get(l.id)?.total ?? '' }}</span>
            <EyeOffIcon v-if="mail.labelFilter.hidden.includes(l.id)" class="text-muted-foreground size-4" aria-label="Hidden" />
            <CheckIcon v-else class="size-4" :class="mail.labelFilter.labels.includes(l.id) ? 'opacity-100' : 'opacity-0'" />
          </CmdItem>
          <CmdItem v-if="mode === 'show' && (!q || 'no label'.includes(q.toLowerCase()))" value="__unlabeled" @select="mail.toggleUnlabeledFilter()">
            <span class="border-muted-foreground/60 size-2.5 rounded-full border border-dashed" />
            <span class="flex-1">No label</span>
            <CheckIcon class="size-4" :class="mail.labelFilter.unlabeled ? 'opacity-100' : 'opacity-0'" />
          </CmdItem>
          <div v-if="!items.length && q" class="text-muted-foreground px-2 py-6 text-center text-sm">No labels match “{{ q }}”</div>
        </CmdList>
        <div class="border-border flex items-center justify-between border-t px-2 py-1.5">
          <Button variant="ghost" size="xs" class="text-muted-foreground" @click="open = false; ui.openSettings('labels')"><TagIcon /> Manage</Button>
          <Button variant="ghost" size="xs" :disabled="!activeCount" @click="mail.clearLabelFilter()">Clear</Button>
        </div>
      </CmdRoot>
    </PopoverContent>
  </Popover>
</template>
