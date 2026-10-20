<script setup lang="ts">
// Lazily loaded content of the list's right-click menu (the ContextMenu root and
// trigger live in MessageList so real contextmenu events open it; WebKitGTK
// ignores synthetic ones).
import { computed } from 'vue'
import {
  ArchiveIcon, FolderInputIcon, CornerUpLeftIcon, ForwardIcon, MailIcon, MailOpenIcon, ReplyAllIcon, SparklesIcon, StarIcon, TagIcon, Trash2Icon,
} from '@lucide/vue'
import {
  ContextMenuCheckboxItem, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut,
  ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger,
} from '@/components/ui/context-menu'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useAccountsStore } from '@/stores/accounts'
import { FOLDER_ICONS } from './folderIcons'

const props = defineProps<{ messageId: string }>()
const mail = useMailStore()
const settings = useSettingsStore()
const compose = useComposeStore()
const ai = useAiStore()

const ctxMsg = computed(() => mail.byId.get(props.messageId) ?? null)
const ctxIds = computed(() => (mail.selection.has(props.messageId) ? [...mail.selection] : [props.messageId]))

/** Bulk actions on a selected row follow the selection, including "all N in this view". */
async function run(fn: (ids: string[]) => unknown, keepSelection = false) {
  const inSelection = mail.selection.has(props.messageId)
  const ids = inSelection && mail.selectAllQuery ? await mail.targetIds() : ctxIds.value
  if (inSelection && !keepSelection) mail.clearSelection()
  await fn(ids)
}


async function respond(mode: 'reply' | 'replyAll' | 'forward') {
  const thread = await mail.loadThread(props.messageId)
  const m = thread.find((x) => x.id === props.messageId) ?? thread[thread.length - 1]
  if (m) void compose.respond(mode, m)
}

const accounts = useAccountsStore()
const moveTargets = computed(() => {
  const m = ctxMsg.value
  if (!m) return []
  return (accounts.folders[m.accountId] ?? []).filter((f) => !['starred', 'drafts', 'sent'].includes(f.kind) && !m.folderIds.includes(f.id))
})
function moveTo(folderId: string) {
  void run((ids) => mail.moveToFolder(ids, folderId))
}

// Like the bulk bar: the toggles follow the whole selection, not just the clicked row.
// "All N in this view" covers rows that never loaded: go by the view's unread count.
const anyUnread = computed(() => (mail.selectAllQuery && mail.selection.has(props.messageId) ? mail.viewUnreadCount > 0 : ctxIds.value.some((id) => mail.byId.get(id)?.unread)))
const allStarred = computed(() => (mail.selectAllQuery && mail.selection.has(props.messageId) ? mail.mailbox.kind === 'starred' : ctxIds.value.every((id) => mail.byId.get(id)?.starred)))
const labelState = (labelId: string) => ctxIds.value.every((id) => mail.byId.get(id)?.labels.includes(labelId))

function toggleCtxLabel(labelId: string) {
  const remove = labelState(labelId)
  // The label submenu stays open for more toggles: keep the selection they apply to.
  void run((ids) => (remove ? mail.removeLabel(ids, labelId) : mail.addLabel(ids, labelId)), true)
}
</script>

<template>
    <ContextMenuContent v-if="ctxMsg" class="w-56" @close-auto-focus="(e: Event) => e.preventDefault()">
    <ContextMenuItem @click="mail.open(messageId)"><MailOpenIcon /> Open <ContextMenuShortcut>↵</ContextMenuShortcut></ContextMenuItem>
    <ContextMenuSeparator />
      <ContextMenuItem @click="respond('reply')"><CornerUpLeftIcon /> Reply <ContextMenuShortcut>R</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="respond('replyAll')"><ReplyAllIcon /> Reply all <ContextMenuShortcut>A</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="respond('forward')"><ForwardIcon /> Forward <ContextMenuShortcut>F</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem @click="run(mail.archive)"><ArchiveIcon /> Archive <ContextMenuShortcut>E</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="run(mail.trash)"><Trash2Icon /> Delete <ContextMenuShortcut>#</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuItem @click="((read) => run((ids) => mail.setRead(ids, read)))(anyUnread)">
        <component :is="anyUnread ? MailOpenIcon : MailIcon" /> {{ anyUnread ? 'Mark as read' : 'Mark as unread' }} <ContextMenuShortcut>U</ContextMenuShortcut>
      </ContextMenuItem>
      <ContextMenuItem @click="((star) => run((ids) => mail.setStarred(ids, star)))(!allStarred)"><StarIcon /> {{ allStarred ? 'Unstar' : 'Star' }} <ContextMenuShortcut>S</ContextMenuShortcut></ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuSub>
        <ContextMenuSubTrigger class="gap-2"><TagIcon class="size-4" /> Labels</ContextMenuSubTrigger>
        <ContextMenuSubContent class="w-52">
          <ContextMenuCheckboxItem
            v-for="l in settings.labels"
            :key="l.id"
            :model-value="labelState(l.id)"
            @select="(e: Event) => { e.preventDefault(); toggleCtxLabel(l.id) }"
          >
            <span class="size-2.5 rounded-full" :style="{ background: l.color }" /> {{ l.name }}
          </ContextMenuCheckboxItem>
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
    <ContextMenuItem @click="ai.labelMessages(ctxIds)"><SparklesIcon class="text-violet-500" /> Label with AI</ContextMenuItem>
    </ContextMenuContent>
</template>
