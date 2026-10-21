<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { ArchiveIcon, InboxIcon, MailIcon, MailOpenIcon, OctagonAlertIcon, ShieldBanIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon, XIcon, TagsIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import Tip from '@/components/common/Tip.vue'
import LabelMenuItems from './LabelMenuItems.vue'
import { groupByName } from '@/lib/labelScope'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import { useSenderRulesStore } from '@/stores/senderRules'

const mail = useMailStore()
const ai = useAiStore()
const ui = useUiStore()
const settings = useSettingsStore()
const senderRules = useSenderRulesStore()

// "Select all in this view" covers rows that never loaded: go by the view's unread count.
const anyUnread = computed(() => (mail.selectAllQuery ? mail.viewUnreadCount > 0 : [...mail.selection].some((id) => mail.byId.get(id)?.unread)))
// Star unless every selected row is starred (see mail.shouldStar).
const star = computed(() => mail.shouldStar([...mail.selection], mail.selectAllQuery))
// The "Add label" menu's open state lives in the ui store (shortcut "l"): don't reopen it with the next selection.
onBeforeUnmount(() => (ui.bulkLabelOpen = false))
/** "Remove label" offers the labels the selected mail has (every label for "all in this view"). */
const presentLabels = computed(() => {
  if (mail.selectAllQuery) return null
  const have = new Set([...mail.selection].flatMap((id) => mail.byId.get(id)?.labels ?? []))
  return groupByName(settings.labels.filter((l) => have.has(l.id))) // one per name: each mailbox's own goes
})

// run() clears the selection before fn runs, so toggles capture anyUnread/star at click time.
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

</script>

<template>
  <div class="flex h-full items-center gap-1">
    <!-- Narrow list: just the number, so every action (and Clear) still fits. -->
    <span class="mr-1 shrink-0 pl-1 text-sm font-medium whitespace-nowrap tabular-nums @[480px]:mr-2">{{ mail.selectionCount.toLocaleString() }}<span class="sr-only @[480px]:not-sr-only"> selected</span></span>
    <Tip label="Archive" shortcut="e"><Button variant="ghost" size="icon-sm" aria-label="Archive" @click="run(mail.archive)"><ArchiveIcon /></Button></Tip>
    <Tip label="Delete" shortcut="#"><Button variant="ghost" size="icon-sm" aria-label="Delete" @click="run(mail.trash)"><Trash2Icon /></Button></Tip>
    <Tip v-if="mail.mailbox.kind !== 'spam'" label="Move to Junk" shortcut="!"><Button variant="ghost" size="icon-sm" aria-label="Move to Junk" @click="run(mail.junk)"><OctagonAlertIcon /></Button></Tip>
    <Tip v-else label="Not junk" shortcut="!"><Button variant="ghost" size="icon-sm" aria-label="Not junk" @click="run(mail.notJunk)"><InboxIcon /></Button></Tip>
    <!-- "All in this view" can't name senders of rows that never loaded: only for picked rows. -->
    <Tip v-if="senderRules.available() && mail.mailbox.kind !== 'spam' && !mail.selectAllQuery" label="Always send senders to Junk">
      <Button variant="ghost" size="icon-sm" aria-label="Always send senders to Junk" @click="run(senderRules.junkSender)"><ShieldBanIcon /></Button>
    </Tip>
    <Tip :label="anyUnread ? 'Mark as read' : 'Mark as unread'" :shortcut="anyUnread ? 'shift+i' : 'u'">
      <Button variant="ghost" size="icon-sm" :aria-label="anyUnread ? 'Mark as read' : 'Mark as unread'" @click="toggleRead(anyUnread)">
        <MailOpenIcon v-if="anyUnread" /><MailIcon v-else />
      </Button>
    </Tip>
    <Tip :label="star ? 'Star' : 'Unstar'" shortcut="s"><Button variant="ghost" size="icon-sm" :aria-label="star ? 'Star' : 'Unstar'" @click="((on) => run((ids) => mail.setStarred(ids, on)))(star)"><StarIcon :class="!star && 'fill-amber-400 text-amber-400'" /></Button></Tip>
    <DropdownMenu v-model:open="ui.bulkLabelOpen">
      <DropdownMenuTrigger as-child>
        <Tip label="Add label" shortcut="l"><Button variant="ghost" size="icon-sm" aria-label="Add label"><TagIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Add label</DropdownMenuLabel>
        <LabelMenuItems :account-ids="mail.selectionAccountIds" @pick="(id) => run((ids) => mail.addLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Tip label="Remove label"><Button variant="ghost" size="icon-sm" aria-label="Remove label"><TagsIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="w-52">
        <DropdownMenuLabel class="text-muted-foreground text-xs">Remove label</DropdownMenuLabel>
        <LabelMenuItems :account-ids="mail.selectionAccountIds" :only="presentLabels ?? undefined" empty-text="No labels on this mail" @pick="(id) => run((ids) => mail.removeLabel(ids, id))" />
      </DropdownMenuContent>
    </DropdownMenu>
    <Tip label="Label with AI">
      <Button variant="ghost" size="icon-sm" aria-label="Label with AI" @click="ai.labelSelection()"><SparklesIcon class="text-violet-500" /></Button>
    </Tip>
    <div class="flex-1" />
    <Tip label="Clear selection" shortcut="esc"><Button variant="ghost" size="icon-sm" aria-label="Clear selection" @click="mail.clearSelection()"><XIcon /></Button></Tip>
  </div>
</template>
