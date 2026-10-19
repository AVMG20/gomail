<script setup lang="ts">
import { computed, ref } from 'vue'
import { MailCheckIcon, MoreHorizontalIcon, SparklesIcon, Trash2Icon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import Tip from '@/components/common/Tip.vue'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ai = useAiStore()
const confirmEmpty = ref(false)
const busy = ref(false)

const kind = computed(() => mail.mailbox.kind)
const folderName = computed(() => (kind.value === 'spam' ? 'Junk' : 'Trash'))

/** The concrete folder ids behind the current view (one per account in the unified view). */
const folderIds = computed(() => {
  if (mail.mailbox.folderId) return [mail.mailbox.folderId]
  return accounts.accounts.map((a) => accounts.folderOf(a.id, kind.value)?.id).filter((x): x is string => !!x)
})
// Gmail's Trash can't be emptied with gomail's API scope (it empties itself after 30 days).
const emptyTargets = computed(() => folderIds.value.filter((id) => {
  const f = accounts.folderMap.get(id)
  const provider = f ? accounts.accountMap.get(f.accountId)?.provider : ''
  return !(kind.value === 'trash' && provider === 'gmail')
}))
const emptiable = computed(() => (kind.value === 'trash' || kind.value === 'spam') && hasMethod('EmptyFolder') && emptyTargets.value.length > 0)
const unread = computed(() => folderIds.value.reduce((n, id) => n + (accounts.folderMap.get(id)?.unread ?? 0), 0))

async function emptyFolder() {
  busy.value = true
  try {
    await Promise.all(emptyTargets.value.map((id) => api.emptyFolder(id)))
    mail.messages = []
    mail.total = 0
    toast(`${folderName.value} emptied`)
    accounts.refreshCounts()
    void mail.softRefresh()
  } catch (e) {
    toast.error(`Couldn’t empty ${folderName.value}`, { description: errorMessage(e) })
  } finally {
    busy.value = false
  }
}

async function markAllRead() {
  const ids = mail.messages.filter((m) => m.unread).map((m) => m.id)
  mail.patchLocal(ids, { unread: false })
  try {
    if (hasMethod('MarkFolderRead') && !mail.search && !mail.hasLabelFilter) await Promise.all(folderIds.value.map((id) => api.markFolderRead(id)))
    else await api.markRead(await mail.targetIdsForQuery(), true)
    toast('Marked all as read')
  } catch (e) {
    mail.patchLocal(ids, { unread: true })
    toast.error('Couldn’t mark as read', { description: errorMessage(e) })
  }
  accounts.refreshCounts()
}

function labelFolder() {
  ai.requestBulk({ query: { ...mail.query, cursor: '' }, ids: [], title: `${mail.mailboxName}${mail.mailboxAccount ? ` · ${mail.mailboxAccount.displayName}` : ''}` })
}
</script>

<template>
  <div class="flex items-center">
    <Button v-if="emptiable && mail.total > 0" variant="ghost" size="sm" class="text-destructive hover:text-destructive h-8 gap-1.5 px-2 text-xs" :disabled="busy" @click="confirmEmpty = true">
      <Trash2Icon class="size-3.5" /> Empty {{ folderName }}
    </Button>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Tip label="More"><Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="More folder actions"><MoreHorizontalIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="w-56">
        <DropdownMenuItem :disabled="!unread && !mail.messages.some((m) => m.unread)" @click="markAllRead"><MailCheckIcon /> Mark all as read</DropdownMenuItem>
        <DropdownMenuItem v-if="settings.aiReady" @click="labelFolder"><SparklesIcon class="text-violet-500" /> Label this view with AI…</DropdownMenuItem>
        <template v-if="emptiable">
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" :disabled="!mail.total" @click="confirmEmpty = true"><Trash2Icon /> Empty {{ folderName }}…</DropdownMenuItem>
        </template>
      </DropdownMenuContent>
    </DropdownMenu>

    <AlertDialog v-model:open="confirmEmpty">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Empty {{ folderName }}?</AlertDialogTitle>
          <AlertDialogDescription>{{ mail.total.toLocaleString() }} conversation{{ mail.total === 1 ? '' : 's' }} will be permanently deleted{{ folderIds.length > 1 ? ' from all accounts' : '' }}. This can’t be undone.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive hover:bg-destructive/90 text-white" @click="emptyFolder">Delete forever</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
