<script setup lang="ts">
import { computed } from 'vue'
import { ArchiveIcon, MailIcon, MailOpenIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon, XIcon, TagsIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import Tip from '@/components/common/Tip.vue'
import LabelMenuItems from './LabelMenuItems.vue'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'

const mail = useMailStore()
const ai = useAiStore()

const allLoaded = computed(() => mail.messages.length > 0 && mail.selection.size === mail.messages.length)
const checkState = computed<boolean | 'indeterminate'>(() => (mail.selectAllQuery || allLoaded.value ? true : mail.selection.size ? 'indeterminate' : false))
const anyUnread = computed(() => [...mail.selection].some((id) => mail.byId.get(id)?.unread))
const allStarred = computed(() => mail.selection.size > 0 && [...mail.selection].every((id) => mail.byId.get(id)?.starred))

function toggleAll() {
  if (checkState.value === true) mail.clearSelection()
  else mail.selectAllLoaded()
}

async function run(fn: (ids: string[]) => Promise<void> | void) {
  const ids = await mail.targetIds()
  mail.clearSelection()
  await fn(ids)
}

async function labelWithAi() {
  if (mail.selectAllQuery) {
    ai.requestBulk({ query: { ...mail.query, cursor: '' }, ids: [], title: `${mail.total.toLocaleString()} messages in ${mail.mailboxName}` })
  } else {
    const ids = [...mail.selection]
    if (ids.length <= 3) {
      mail.clearSelection()
      await ai.labelMessages(ids)
    } else ai.requestBulk({ query: null, ids, title: `${ids.length} selected messages` })
  }
}
</script>

<template>
  <div class="flex h-full items-center gap-1">
    <Checkbox :model-value="checkState" class="mr-2 ml-1" aria-label="Select all" @update:model-value="toggleAll" />
    <span class="mr-2 text-sm font-medium tabular-nums">{{ mail.selectionCount.toLocaleString() }} selected</span>
    <Tip label="Archive" shortcut="e"><Button variant="ghost" size="icon-sm" aria-label="Archive" @click="run(mail.archive)"><ArchiveIcon /></Button></Tip>
    <Tip label="Delete" shortcut="#"><Button variant="ghost" size="icon-sm" aria-label="Delete" @click="run(mail.trash)"><Trash2Icon /></Button></Tip>
    <Tip :label="anyUnread ? 'Mark as read' : 'Mark as unread'">
      <Button variant="ghost" size="icon-sm" :aria-label="anyUnread ? 'Mark as read' : 'Mark as unread'" @click="run((ids) => mail.setRead(ids, anyUnread))">
        <MailOpenIcon v-if="anyUnread" /><MailIcon v-else />
      </Button>
    </Tip>
    <Tip :label="allStarred ? 'Unstar' : 'Star'"><Button variant="ghost" size="icon-sm" aria-label="Star" @click="run((ids) => mail.setStarred(ids, !allStarred))"><StarIcon /></Button></Tip>
    <DropdownMenu>
      <Tip label="Add label">
        <DropdownMenuTrigger as-child><Button variant="ghost" size="icon-sm" aria-label="Add label"><TagIcon /></Button></DropdownMenuTrigger>
      </Tip>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Add label</DropdownMenuLabel>
        <LabelMenuItems @pick="(id) => run((ids) => mail.addLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <DropdownMenu>
      <Tip label="Remove label">
        <DropdownMenuTrigger as-child><Button variant="ghost" size="icon-sm" aria-label="Remove label"><TagsIcon /></Button></DropdownMenuTrigger>
      </Tip>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Remove label</DropdownMenuLabel>
        <LabelMenuItems @pick="(id) => run((ids) => mail.removeLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <Tip label="Label with AI">
      <Button variant="ghost" size="sm" class="gap-1.5 px-2" @click="labelWithAi">
        <SparklesIcon class="text-violet-500" /> <span class="text-xs">Label with AI</span>
      </Button>
    </Tip>
    <div class="flex-1" />
    <Tip label="Clear selection" shortcut="esc"><Button variant="ghost" size="icon-sm" aria-label="Clear selection" @click="mail.clearSelection()"><XIcon /></Button></Tip>
  </div>
</template>
