<script setup lang="ts">
import { computed, ref } from 'vue'
import { useEventListener } from '@vueuse/core'
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

const mail = useMailStore()
const accounts = useAccountsStore()
const ai = useAiStore()
const confirmEmpty = ref(false)
const busy = ref(false)

const kind = computed(() => mail.mailbox.kind)
const folderName = computed(() => (kind.value === 'spam' ? 'Junk' : 'Trash'))

/** The concrete folder ids behind the current view (one per account in the unified view). */
const folderIds = computed(() => mail.viewFolderIds)
// Gmail's Trash can't be emptied with gomail's API scope (it empties itself after 30 days).
const emptyTargets = computed(() => folderIds.value.filter((id) => {
  const f = accounts.folderMap.get(id)
  const provider = f ? accounts.accountMap.get(f.accountId)?.provider : ''
  return !(kind.value === 'trash' && provider === 'gmail')
}))
/** Gmail's Trash is left out of "Empty Trash" in the unified view. */
const skippedFolders = computed(() => folderIds.value.length - emptyTargets.value.length)
/** What emptying deletes: the emptiable folders only (the view may also show Gmail's Trash). */
const emptyCount = computed(() => (skippedFolders.value ? emptyTargets.value.reduce((n, id) => n + (accounts.folderMap.get(id)?.total ?? 0), 0) : mail.total))
const emptiable = computed(() => (kind.value === 'trash' || kind.value === 'spam') && hasMethod('EmptyFolder') && emptyTargets.value.length > 0)
const unread = computed(() => folderIds.value.reduce((n, id) => n + (accounts.folderMap.get(id)?.unread ?? 0), 0))

async function emptyFolder() {
  busy.value = true
  try {
    const targets = new Set(emptyTargets.value)
    await Promise.all(emptyTargets.value.map((id) => api.emptyFolder(id)))
    mail.removeLocal(mail.messages.filter((m) => m.folderIds.some((f) => targets.has(f))).map((m) => m.id))
    toast(`${folderName.value} emptied`)
    accounts.refreshCounts()
    void mail.softRefresh()
  } catch (e) {
    toast.error(`Couldn’t empty ${folderName.value}`, { description: errorMessage(e) })
  } finally {
    busy.value = false
  }
}

function markAllRead() {
  void mail.markAllRead()
}

// Command palette entry points ("Mark all as read", "Empty Trash/Junk", "Label this view with AI").
useEventListener(window, 'gomail:folder-action', (e: Event) => {
  const what = (e as CustomEvent<string>).detail
  if (what === 'markAllRead') void markAllRead()
  else if (what === 'empty' && emptiable.value && mail.total > 0) confirmEmpty.value = true
  else if (what === 'labelAi') labelFolder()
})

function labelFolder() {
  ai.requestBulk({ query: { ...mail.query, cursor: '' }, ids: [], title: `${mail.mailboxName}${mail.mailboxAccount ? ` · ${mail.mailboxAccount.displayName}` : ''}` })
}
</script>

<template>
  <div class="flex items-center">
    <!-- Gmail's Trash can't be emptied from here: say why instead of showing nothing. -->
    <span v-if="kind === 'trash' && skippedFolders && !emptiable && mail.total > 0" class="text-muted-foreground mr-1 hidden text-[11px] @[480px]:inline">Gmail empties Trash after 30 days</span>
    <Button v-if="emptiable && mail.total > 0" variant="ghost" size="sm" class="text-destructive hover:text-destructive h-8 gap-1.5 px-2 text-xs" :disabled="busy" @click="confirmEmpty = true">
      <Trash2Icon class="size-3.5" /> Empty {{ folderName }}
    </Button>
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Tip label="More"><Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="More folder actions"><MoreHorizontalIcon /></Button></Tip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="w-56">
        <DropdownMenuItem :disabled="!unread && !mail.messages.some((m) => m.unread)" @click="markAllRead"><MailCheckIcon /> Mark all as read</DropdownMenuItem>
        <DropdownMenuItem @click="labelFolder"><SparklesIcon class="text-violet-500" /> Label this view with AI…</DropdownMenuItem>
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
          <AlertDialogDescription>
            {{ emptyCount.toLocaleString() }} {{ skippedFolders ? 'message' : 'conversation' }}{{ emptyCount === 1 ? '' : 's' }} will be permanently deleted{{ emptyTargets.length > 1 ? ' from all accounts' : '' }}. This can’t be undone.
            <template v-if="skippedFolders"> Gmail’s Trash isn’t included: Gmail empties it after 30 days.</template>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive hover:bg-destructive/90 text-white" @click="emptyFolder">Delete forever</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
