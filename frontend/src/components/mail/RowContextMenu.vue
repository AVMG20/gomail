<script setup lang="ts">
// Lazily loaded content of the list's right-click menu (the ContextMenu root and
// trigger live in MessageList so real contextmenu events open it; WebKitGTK
// ignores synthetic ones).
import { computed } from 'vue'
import {
  ArchiveIcon, FolderInputIcon, InboxIcon, CornerUpLeftIcon, ForwardIcon, MailIcon, MailOpenIcon, OctagonAlertIcon, PanelRightOpenIcon, ReplyAllIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon,
} from '@lucide/vue'
import {
  ContextMenuCheckboxItem, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuShortcut,
  ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger,
} from '@/components/ui/context-menu'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useAccountsStore } from '@/stores/accounts'
import { shortcutLabel } from '@/lib/platform'
import { FOLDER_ICONS } from './folderIcons'

const props = defineProps<{ messageId: string }>()
const mail = useMailStore()
const settings = useSettingsStore()
const compose = useComposeStore()
const ai = useAiStore()

const ctxMsg = computed(() => mail.byId.get(props.messageId) ?? null)
const ctxIds = computed(() => (mail.selection.has(props.messageId) ? [...mail.selection] : [props.messageId]))
/** Right-clicked inside a selection of several: the menu acts on all of them. */
const multi = computed(() => mail.selection.has(props.messageId) && (mail.selectAllQuery || mail.selection.size > 1))
const multiCount = computed(() => (mail.selectAllQuery ? mail.total : mail.selection.size))

/** Bulk actions on a selected row follow the selection, including "all N in this view". */
async function run(fn: (ids: string[]) => unknown, keepSelection = false) {
  const inSelection = mail.selection.has(props.messageId)
  const ids = inSelection && mail.selectAllQuery ? await mail.targetIds() : ctxIds.value
  if (inSelection && !keepSelection) mail.clearSelection()
  await fn(ids)
}


function respond(mode: 'reply' | 'replyAll' | 'forward') {
  void compose.respondToThread(mode, props.messageId)
}

const accounts = useAccountsStore()
const moveTargets = computed(() => {
  const m = ctxMsg.value
  if (!m) return []
  // Folders belong to one mailbox: a selection spanning accounts has no common folder.
  if (multi.value && mail.selectionAccountIds.length !== 1) return []
  return (accounts.folders[m.accountId] ?? []).filter((f) => !['starred', 'drafts', 'sent', 'spam'].includes(f.kind) && !m.folderIds.includes(f.id))
})
function moveTo(folderId: string) {
  void run((ids) => mail.moveToFolder(ids, folderId))
}

// Like the bulk bar: the toggles follow the whole selection, not just the clicked row.
// "All N in this view" covers rows that never loaded: go by the view's unread count.
const anyUnread = computed(() => (mail.selectAllQuery && mail.selection.has(props.messageId) ? mail.viewUnreadCount > 0 : ctxIds.value.some((id) => mail.byId.get(id)?.unread)))
const star = computed(() => mail.shouldStar(ctxIds.value, mail.selectAllQuery && mail.selection.has(props.messageId)))

/** Label with AI follows the selection too (a big one goes through the bulk confirm). */
function labelWithAi() {
  if (mail.selection.has(props.messageId)) void ai.labelSelection()
  else void ai.labelMessages([props.messageId])
}
// The labels of the mailboxes of the mail it applies to (a mixed selection: of any of them), plus
// labels the mail already has so they can be taken off.
const ctxLabels = computed(() => {
  const inSelection = mail.selection.has(props.messageId)
  const accts = inSelection ? mail.selectionAccountIds : [ctxMsg.value?.accountId ?? '']
  const scoped = new Set(settings.labelsForAccounts(accts).map((l) => l.id))
  const present = new Set(ctxIds.value.flatMap((id) => mail.byId.get(id)?.labels ?? []))
  return settings.labels.filter((l) => scoped.has(l.id) || present.has(l.id))
})
const labelState = (labelId: string) => ctxIds.value.every((id) => mail.byId.get(id)?.labels.includes(labelId))

function toggleCtxLabel(labelId: string) {
  const remove = labelState(labelId)
  // The label submenu stays open for more toggles: keep the selection they apply to.
  void run((ids) => (remove ? mail.removeLabel(ids, labelId) : mail.addLabel(ids, labelId)), true)
}
</script>

<template>
    <ContextMenuContent v-if="ctxMsg" class="w-56" @close-auto-focus="(e: Event) => e.preventDefault()">
    <!-- Several selected: say what the actions apply to; Open/Reply/Forward are for one message. -->
    <ContextMenuLabel v-if="multi" class="text-muted-foreground text-xs font-normal">{{ multiCount.toLocaleString() }} selected</ContextMenuLabel>
    <template v-else>
      <ContextMenuItem @click="mail.open(messageId)"><PanelRightOpenIcon /> Open <ContextMenuShortcut>↵</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem @click="respond('reply')"><CornerUpLeftIcon /> Reply <ContextMenuShortcut>R</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="respond('replyAll')"><ReplyAllIcon /> Reply all <ContextMenuShortcut>A</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="respond('forward')"><ForwardIcon /> Forward <ContextMenuShortcut>F</ContextMenuShortcut></ContextMenuItem>
    </template>
      <ContextMenuSeparator />
      <ContextMenuItem @click="run(mail.archive)"><ArchiveIcon /> Archive <ContextMenuShortcut>E</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="run(mail.trash)"><Trash2Icon /> Delete <ContextMenuShortcut>#</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem v-if="mail.mailbox.kind !== 'spam'" @click="run(mail.junk)"><OctagonAlertIcon /> Move to Junk <ContextMenuShortcut>!</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem v-else @click="run(mail.notJunk)"><InboxIcon /> Not junk <ContextMenuShortcut>!</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="((read) => read && mail.selectAllQuery && mail.selection.has(messageId) ? (mail.clearSelection(), mail.markAllRead()) : run((ids) => mail.setRead(ids, read)))(anyUnread)">
        <component :is="anyUnread ? MailOpenIcon : MailIcon" /> {{ anyUnread ? 'Mark as read' : 'Mark as unread' }} <ContextMenuShortcut>{{ shortcutLabel(anyUnread ? 'shift+i' : 'u') }}</ContextMenuShortcut>
      </ContextMenuItem>
      <ContextMenuItem @click="((on) => run((ids) => mail.setStarred(ids, on)))(star)"><StarIcon /> {{ star ? 'Star' : 'Unstar' }} <ContextMenuShortcut>S</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuSub>
        <ContextMenuSubTrigger class="gap-2"><TagIcon class="size-4" /> Labels</ContextMenuSubTrigger>
        <ContextMenuSubContent class="w-52">
          <ContextMenuCheckboxItem
            v-for="l in ctxLabels"
            :key="l.id"
            :model-value="labelState(l.id)"
            @select="(e: Event) => { e.preventDefault(); toggleCtxLabel(l.id) }"
          >
            <span class="size-2.5 rounded-full" :style="{ background: l.color }" /> {{ l.name }}
          </ContextMenuCheckboxItem>
          <ContextMenuItem v-if="!ctxLabels.length" disabled>No labels yet</ContextMenuItem>
        </ContextMenuSubContent>
      </ContextMenuSub>
      <ContextMenuSub v-if="moveTargets.length">
      <ContextMenuSubTrigger class="gap-2"><FolderInputIcon class="size-4" /> Move to</ContextMenuSubTrigger>
      <ContextMenuSubContent class="w-48">
        <ContextMenuItem v-for="f in moveTargets" :key="f.id" @click="moveTo(f.id)">
          <component :is="FOLDER_ICONS[f.kind] ?? FOLDER_ICONS.custom" /> {{ f.kind === 'spam' ? 'Junk' : f.name }}
        </ContextMenuItem>
      </ContextMenuSubContent>
    </ContextMenuSub>
    <ContextMenuItem @click="labelWithAi"><SparklesIcon class="text-violet-500" /> Label with AI</ContextMenuItem>
    </ContextMenuContent>
</template>
