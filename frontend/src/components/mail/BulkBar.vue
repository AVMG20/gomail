<script setup lang="ts">
import { computed } from 'vue'
import { ArchiveIcon, MailIcon, MailOpenIcon, OctagonAlertIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon, XIcon, TagsIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import Tip from '@/components/common/Tip.vue'
import LabelMenuItems from './LabelMenuItems.vue'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'

const mail = useMailStore()
const ai = useAiStore()

// "Select all in this view" covers rows that never loaded: go by the view's unread count.
const anyUnread = computed(() => (mail.selectAllQuery ? mail.viewUnreadCount > 0 : [...mail.selection].some((id) => mail.byId.get(id)?.unread)))
// Unloaded rows' stars are unknown: with "all in this view" offer Star, except in Starred itself.
const allStarred = computed(() => (mail.selectAllQuery ? mail.mailbox.kind === 'starred' : mail.selection.size > 0 && [...mail.selection].every((id) => mail.byId.get(id)?.starred)))


// run() clears the selection before fn runs, so toggles capture anyUnread/allStarred at click time.
async function run(fn: (ids: string[]) => Promise<void> | void) {
  const ids = await mail.targetIds()
  mail.clearSelection()
  await fn(ids)
}

/** "All in this view" + read goes through markAllRead, which also covers mail only on the server. */
function toggleRead(read: boolean) {
  if (read && mail.selectAllQuery) {
    mail.clearSelection()
    return mail.markAllRead()
  }
  return run((ids) => mail.setRead(ids, read))
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
    <!-- Narrow list: just the number, so every action (and Clear) still fits. -->
    <span class="mr-1 shrink-0 pl-1 text-sm font-medium whitespace-nowrap tabular-nums @[480px]:mr-2">{{ mail.selectionCount.toLocaleString() }}<span class="sr-only @[480px]:not-sr-only"> selected</span></span>
    <Tip label="Archive" shortcut="e"><Button variant="ghost" size="icon-sm" aria-label="Archive" @click="run(mail.archive)"><ArchiveIcon /></Button></Tip>
    <Tip label="Delete" shortcut="#"><Button variant="ghost" size="icon-sm" aria-label="Delete" @click="run(mail.trash)"><Trash2Icon /></Button></Tip>
    <Tip v-if="mail.mailbox.kind !== 'spam'" label="Move to Junk"><Button variant="ghost" size="icon-sm" aria-label="Move to Junk" @click="run(mail.junk)"><OctagonAlertIcon /></Button></Tip>
    <Tip :label="anyUnread ? 'Mark as read' : 'Mark as unread'">
      <Button variant="ghost" size="icon-sm" :aria-label="anyUnread ? 'Mark as read' : 'Mark as unread'" @click="toggleRead(anyUnread)">
        <MailOpenIcon v-if="anyUnread" /><MailIcon v-else />
      </Button>
    </Tip>
    <Tip :label="allStarred ? 'Unstar' : 'Star'"><Button variant="ghost" size="icon-sm" aria-label="Star" @click="((star) => run((ids) => mail.setStarred(ids, star)))(!allStarred)"><StarIcon /></Button></Tip>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Tip label="Add label"><Button variant="ghost" size="icon-sm" aria-label="Add label"><TagIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Add label</DropdownMenuLabel>
        <LabelMenuItems @pick="(id) => run((ids) => mail.addLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Tip label="Remove label"><Button variant="ghost" size="icon-sm" aria-label="Remove label"><TagsIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Remove label</DropdownMenuLabel>
        <LabelMenuItems @pick="(id) => run((ids) => mail.removeLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <Tip label="Label with AI">
      <Button variant="ghost" size="sm" class="gap-1.5 px-2" aria-label="Label with AI" @click="labelWithAi">
        <SparklesIcon class="text-violet-500" /> <span class="hidden text-xs @[460px]:inline">Label with AI</span>
      </Button>
    </Tip>
    <div class="flex-1" />
    <Tip label="Clear selection" shortcut="esc"><Button variant="ghost" size="icon-sm" aria-label="Clear selection" @click="mail.clearSelection()"><XIcon /></Button></Tip>
  </div>
</template>
