<script setup lang="ts">
// Right-click menu for a folder in the sidebar: bulk actions on the whole folder (also mail that
// isn't downloaded yet where the server can do it, e.g. "Mark all as read"). Anything that moves
// or deletes a lot asks first and says how many messages it affects; moves can be undone.
import { computed, ref } from 'vue'
import { ArchiveIcon, CalendarClockIcon, EyeOffIcon, InboxIcon, MailCheckIcon, MailOpenIcon, OctagonAlertIcon, RefreshCwIcon, SparklesIcon, Trash2Icon, UsersIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { toYmd } from '@/lib/calendar'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useCleanupStore } from '@/stores/cleanup'
import { MAX_RESOLVE, inChunks, useMailStore } from '@/stores/mail'
import { useMailboxSettings } from '@/components/settings/mailboxSettings'

const props = defineProps<{ kind: string; folderId: string; name: string; unread: number }>()
const accounts = useAccountsStore()
const mail = useMailStore()
const ai = useAiStore()
const cleanup = useCleanupStore()
const mb = useMailboxSettings()

/** The concrete folders behind this row: one, or one per account in the unified view. */
const targets = computed(() => {
  if (props.folderId) {
    const f = accounts.folderMap.get(props.folderId)
    return f ? [{ folderId: f.id, accountId: f.accountId }] : []
  }
  return accounts.accounts
    .map((a) => ({ folderId: accounts.folderOf(a.id, props.kind)?.id ?? '', accountId: a.id }))
    .filter((t) => t.folderId)
})
const providerOf = (accountId: string) => accounts.accountMap.get(accountId)?.provider ?? ''
// Gmail's Trash can't be emptied with gomail's access (Gmail empties it after 30 days).
const emptiable = computed(() => targets.value.filter((t) => !(props.kind === 'trash' && providerOf(t.accountId) === 'gmail')))
const canMarkRead = computed(() => !['sent', 'drafts'].includes(props.kind) && hasMethod('MarkFolderRead'))
const canLabel = computed(() => !['sent', 'drafts', 'trash', 'spam'].includes(props.kind))

function refresh() {
  accounts.refreshCounts()
  void mail.softRefresh()
}

async function markAllRead() {
  try {
    await Promise.all(targets.value.map((t) => api.markFolderRead(t.folderId)))
    toast(`${props.name}: all marked as read`)
  } catch (e) {
    toast.error('Couldn’t mark as read', { description: errorMessage(e) })
  }
  refresh()
}

// ---- confirm + bulk move/delete
interface Pending { title: string; text: string; action: string; destructive?: boolean; run: () => Promise<void> }
const pending = ref<Pending | null>(null)
const open = ref(false)
const busy = ref(false)
function ask(p: Pending) {
  pending.value = p
  open.value = true
}
async function confirm() {
  const p = pending.value
  if (!p) return
  busy.value = true
  try {
    await p.run()
  } finally {
    busy.value = false
    open.value = false
  }
}

/** Ids in each target folder matching a search ("" = everything). */
async function resolve(search: string) {
  const out: { folderId: string; accountId: string; ids: string[] }[] = []
  for (const t of targets.value) {
    const ids = await api.resolveQueryIds({ accountId: t.accountId, folderId: t.folderId, search }, MAX_RESOLVE)
    if (ids.length) out.push({ ...t, ids })
  }
  return out
}

const plural = (n: number) => `${n.toLocaleString()} message${n === 1 ? '' : 's'}`

async function archive(search: string, what: string) {
  let groups: Awaited<ReturnType<typeof resolve>>
  try {
    groups = await resolve(search)
  } catch (e) {
    return toast.error('Couldn’t look up the messages', { description: errorMessage(e) })
  }
  const n = groups.reduce((s, g) => s + g.ids.length, 0)
  if (!n) return toast(`No ${what} in ${props.name}`)
  ask({
    title: `Archive ${plural(n)}?`,
    text: `${what[0].toUpperCase()}${what.slice(1)} in ${props.name} moves to the archive. You can undo this right after.`,
    action: 'Archive',
    run: async () => {
      try {
        for (const g of groups) await inChunks(g.ids, api.archive)
        toast(`Archived ${plural(n)}`, {
          action: {
            label: 'Undo',
            onClick: async () => {
              for (const g of groups) await inChunks(g.ids, (c) => api.moveToFolder(c, g.folderId))
              refresh()
            },
          },
        })
      } catch (e) {
        toast.error('Archive failed', { description: errorMessage(e) })
      }
      refresh()
    },
  })
}

/** Other folders: move everything in it to the Junk folder or the Inbox (undo moves it back). */
async function moveAll(to: 'spam' | 'inbox') {
  let groups: Awaited<ReturnType<typeof resolve>>
  try {
    groups = await resolve('')
  } catch (e) {
    return toast.error('Couldn’t look up the messages', { description: errorMessage(e) })
  }
  const n = groups.reduce((s, g) => s + g.ids.length, 0)
  if (!n) return toast(`${props.name} is empty`)
  const dest = to === 'spam' ? 'Junk' : 'the Inbox'
  ask({
    title: `Move ${plural(n)} to ${dest}?`,
    text: to === 'spam'
      ? `Everything in ${props.name} moves to Junk. Tip: right-click a message → Rules for the sender → Always send to Junk, so their new mail skips the inbox.`
      : `Everything in ${props.name} moves to the Inbox of its mailbox.`,
    action: `Move to ${to === 'spam' ? 'Junk' : 'Inbox'}`,
    run: async () => {
      try {
        for (const g of groups) {
          if (to === 'spam') await inChunks(g.ids, api.moveToJunk)
          else {
            const inbox = accounts.folderOf(g.accountId, 'inbox')?.id
            if (inbox) await inChunks(g.ids, (c) => api.moveToFolder(c, inbox))
          }
        }
        toast(`Moved ${plural(n)} to ${dest}`, {
          action: {
            label: 'Undo',
            onClick: async () => {
              for (const g of groups) await inChunks(g.ids, (c) => api.moveToFolder(c, g.folderId))
              refresh()
            },
          },
        })
      } catch (e) {
        toast.error('Move failed', { description: errorMessage(e) })
      }
      refresh()
    },
  })
}

/** Other folders: delete it on the server (Outlook); its mail goes to Trash first. */
function del() {
  const f = accounts.folderMap.get(props.folderId)
  if (!f) return
  const where = accounts.accountMap.get(f.accountId)?.provider === 'outlook' ? 'Outlook' : 'the server'
  ask({
    title: `Delete the folder “${props.name}”?`,
    text: `${f.total ? `Its ${plural(f.total)} move to Trash first. ` : ''}The folder is deleted in ${where} too, for good.`,
    action: 'Delete folder',
    destructive: true,
    run: async () => {
      // Closes the dialog right away: the progress toast follows the delete.
      void accounts.deleteFolder(f.id).then((ok) => { if (ok && mail.mailbox.folderId === f.id) mail.goToKind('inbox') })
    },
  })
}

function hide() {
  if (!props.folderId) return
  mb.setFolderShown(props.folderId, false)
  if (mail.mailbox.folderId === props.folderId) mail.goToKind('inbox')
  toast(`${props.name} is hidden`, {
    description: 'Show it again in this mailbox’s settings.',
    action: { label: 'Undo', onClick: () => mb.setFolderShown(props.folderId, true) },
  })
}

async function junkToInbox() {
  let groups: Awaited<ReturnType<typeof resolve>>
  try {
    groups = await resolve('')
  } catch (e) {
    return toast.error('Couldn’t look up the messages', { description: errorMessage(e) })
  }
  const n = groups.reduce((s, g) => s + g.ids.length, 0)
  if (!n) return toast(`${props.name} is empty`)
  ask({
    title: `Move ${plural(n)} to the Inbox?`,
    text: 'Everything in Junk goes back to the Inbox of its mailbox and is no longer treated as junk.',
    action: 'Move to Inbox',
    run: async () => {
      try {
        for (const g of groups) {
          const inbox = accounts.folderOf(g.accountId, 'inbox')?.id
          if (inbox) await inChunks(g.ids, (c) => api.moveToFolder(c, inbox))
        }
        toast(`Moved ${plural(n)} to the Inbox`)
      } catch (e) {
        toast.error('Move failed', { description: errorMessage(e) })
      }
      refresh()
    },
  })
}

function empty() {
  const total = emptiable.value.reduce((s, t) => s + (accounts.folderMap.get(t.folderId)?.total ?? 0), 0)
  ask({
    title: `Empty ${props.name}?`,
    text: `${total ? `${plural(total)} will be deleted for good. ` : ''}This can’t be undone.`,
    action: `Empty ${props.name}`,
    destructive: true,
    run: async () => {
      try {
        await Promise.all(emptiable.value.map((t) => api.emptyFolder(t.folderId)))
        toast(`${props.name} emptied`)
      } catch (e) {
        toast.error(`Couldn’t empty ${props.name}`, { description: errorMessage(e) })
      }
      refresh()
    },
  })
}

function labelWithAi() {
  const accountId = props.folderId ? (targets.value[0]?.accountId ?? '') : ''
  ai.requestBulk({ query: { accountId, folderId: props.folderId, folderKind: props.folderId ? '' : props.kind }, ids: [], title: props.name })
}

function sync() {
  if (props.folderId) void accounts.sync(targets.value[0]?.accountId ?? '')
  else void accounts.sync('')
}

const monthAgo = () => toYmd(new Date(Date.now() - 30 * 86_400_000))
</script>

<template>
  <ContextMenu>
    <ContextMenuTrigger as-child>
      <slot />
    </ContextMenuTrigger>
    <ContextMenuContent class="w-60">
      <ContextMenuLabel class="text-muted-foreground truncate text-xs font-normal">{{ name }}</ContextMenuLabel>
      <ContextMenuItem v-if="canMarkRead" :disabled="!unread && kind !== 'archive' && kind !== 'custom' && kind !== 'starred'" @select="markAllRead">
        <MailCheckIcon /> Mark all as read
      </ContextMenuItem>

      <template v-if="kind === 'inbox'">
        <ContextMenuSeparator />
        <ContextMenuItem v-if="hasMethod('TopSenders')" @select="cleanup.show(folderId ? (targets[0]?.accountId ?? '') : '')"><UsersIcon /> Clean up by sender…</ContextMenuItem>
        <ContextMenuItem @select="archive('is:read', 'read mail')"><MailOpenIcon /> Archive read mail…</ContextMenuItem>
        <ContextMenuItem @select="archive(`before:${monthAgo()}`, 'mail older than 30 days')"><CalendarClockIcon /> Archive older than 30 days…</ContextMenuItem>
        <ContextMenuItem @select="archive('', 'all mail')"><ArchiveIcon /> Archive everything…</ContextMenuItem>
      </template>

      <template v-if="kind === 'custom'">
        <ContextMenuSeparator />
        <ContextMenuItem @select="moveAll('spam')"><OctagonAlertIcon /> Move everything to Junk…</ContextMenuItem>
        <ContextMenuItem @select="moveAll('inbox')"><InboxIcon /> Move everything to Inbox…</ContextMenuItem>
        <ContextMenuItem @select="hide"><EyeOffIcon /> Hide folder</ContextMenuItem>
        <ContextMenuItem v-if="folderId && hasMethod('DeleteFolder')" variant="destructive" @select="del"><Trash2Icon /> Delete folder…</ContextMenuItem>
      </template>

      <template v-if="kind === 'spam'">
        <ContextMenuSeparator />
        <ContextMenuItem @select="junkToInbox"><InboxIcon /> Move all to Inbox…</ContextMenuItem>
      </template>

      <template v-if="(kind === 'spam' || kind === 'trash') && hasMethod('EmptyFolder')">
        <ContextMenuItem v-if="emptiable.length" variant="destructive" @select="empty"><Trash2Icon /> Empty {{ name }}…</ContextMenuItem>
        <ContextMenuItem v-else disabled><Trash2Icon /> Gmail empties Trash after 30 days</ContextMenuItem>
      </template>

      <ContextMenuSeparator />
      <ContextMenuItem v-if="canLabel" @select="labelWithAi"><SparklesIcon class="text-violet-500" /> Label with AI…</ContextMenuItem>
      <ContextMenuItem @select="sync"><RefreshCwIcon /> Sync now</ContextMenuItem>
    </ContextMenuContent>
  </ContextMenu>

  <AlertDialog v-model:open="open">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>{{ pending?.title }}</AlertDialogTitle>
        <AlertDialogDescription>{{ pending?.text }}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel :disabled="busy">Cancel</AlertDialogCancel>
        <AlertDialogAction
          :disabled="busy"
          :class="pending?.destructive && 'bg-destructive text-white hover:bg-destructive/90'"
          @click.prevent="confirm"
        >{{ busy ? 'Working…' : pending?.action }}</AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
