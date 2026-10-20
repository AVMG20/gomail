<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import {
  ArchiveIcon, CornerUpLeftIcon, ForwardIcon, MailIcon, MoreHorizontalIcon, ReplyAllIcon, SparklesIcon, StarIcon,
  Trash2Icon, FolderInputIcon, TextQuoteIcon, LoaderIcon, MailOpenIcon, XIcon, ArrowLeftIcon, PencilIcon,
} from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent,
  DropdownMenuSubTrigger, DropdownMenuTrigger, DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import LabelPill from '@/components/common/LabelPill.vue'
import Tip from '@/components/common/Tip.vue'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { displayName } from '@/lib/format'
import type { Label, Message, MessageLabel } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { FOLDER_ICONS } from './folderIcons'
import LabelPopover from './LabelPopover.vue'
import ThreadMessage from './ThreadMessage.vue'
import InviteCard from './InviteCard.vue'

defineProps<{ showBack?: boolean }>()
const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const compose = useComposeStore()
const ai = useAiStore()

const thread = shallowRef<Message[]>([])
const loading = ref(false)
const error = ref('')
const expanded = ref<Set<string>>(new Set())
// Lives in the store so "Load images" survives the pane remounting (reading pane off, back/forward).
const remoteAllowed = computed(() => mail.remoteAllowed)
const summary = ref<{ id: string; text: string; loading: boolean } | null>(null)
const scroller = ref<HTMLElement | null>(null)

let token = 0
let timer: ReturnType<typeof setTimeout> | null = null

function applyThread(id: string, list: Message[]) {
  thread.value = list
  // Images were allowed for this conversation earlier: fetch the unstripped bodies again.
  const tid = list[0]?.threadId
  if (tid && mail.remoteAllowed.has(tid) && list.some((m) => m.remoteContentBlocked)) void nextTick(() => loadImages(tid))
  const last = list[list.length - 1]
  // Expand the newest message plus any unread ones, like most mail apps.
  expanded.value = new Set([id, last?.id, ...list.filter((m) => m.unread).map((m) => m.id)].filter(Boolean) as string[])
  scroller.value?.scrollTo({ top: 0 })
}

/** The loaded thread belongs to the open message (never show another conversation's body). */
const threadMatches = computed(() => !!mail.openId && thread.value.some((m) => m.id === mail.openId))

async function load(id: string) {
  const t = ++token
  loading.value = true
  error.value = ''
  try {
    const list = await mail.loadThread(id)
    if (t !== token) return
    if (!list.some((m) => m.id === id)) throw new Error('This message is no longer available. It may have been moved or deleted.')
    applyThread(id, list)
  } catch (e) {
    if (t !== token) return
    error.value = errorMessage(e)
    thread.value = []
  } finally {
    if (t === token) loading.value = false
  }
}

watch(() => mail.openId, (id) => {
  summary.value = null
  error.value = ''
  if (timer) clearTimeout(timer)
  if (!id) {
    token++
    thread.value = []
    return
  }
  // Cached conversations render immediately (no flash while holding j/k).
  const cached = mail.cachedThread(id)
  if (cached) {
    token++
    loading.value = false
    applyThread(id, cached)
    return
  }
  // Tiny debounce so holding j/k does not fire a request per row.
  timer = setTimeout(() => load(id), thread.value.length ? 60 : 0)
}, { immediate: true })

// A reply arriving while the conversation is open changes its row in the list: show it
// without resetting what the reader expanded or scrolled to.
const liveRow = computed(() => {
  const first = thread.value[0]
  if (!first) return ''
  const r = mail.messages.find((m) => m.accountId === first.accountId && m.threadId === first.threadId)
  return r ? `${first.accountId}|${first.threadId}|${r.id}:${r.threadCount ?? 1}` : ''
})
const convOf = (k: string) => k.split('|').slice(0, 2).join('|')
watch(liveRow, async (now, old) => {
  const id = mail.openId
  // Only for the conversation on screen: while another message is still loading, the old
  // thread's row changing must not cancel (and replace) that load.
  // Same conversation only (opening another one changes this too, and needs no refetch).
  if (!old || !now || now === old || convOf(now) !== convOf(old) || !id || !threadMatches.value) return
  const t = ++token
  try {
    mail.invalidateThread(id)
    const list = await mail.loadThread(id)
    if (t !== token || mail.openId !== id || !list.some((m) => m.id === id)) return
    const known = new Set(thread.value.map((m) => m.id))
    thread.value = list
    expanded.value = new Set([...expanded.value, ...list.filter((m) => !known.has(m.id)).map((m) => m.id)])
  } catch {
    /* keep showing what we have */
  }
})

const current = computed(() => {
  void mail.threadRev // the thread copy is patched in place (labels, read state)
  return mail.openSummary ?? thread.value.find((m) => m.id === mail.openId) ?? null
})
const newest = computed(() => thread.value[thread.value.length - 1] ?? null)
/** Meeting invitation carried by the conversation (latest one wins). */
const invite = computed(() => [...thread.value].reverse().find((m) => m.invite)?.invite ?? null)
const ownEmail = computed(() => (current.value ? accounts.accountMap.get(current.value.accountId)?.email ?? '' : ''))
const account = computed(() => (current.value ? accounts.accountMap.get(current.value.accountId) : undefined))
const allowRemote = computed(() => !!settings.settings?.showRemoteImages || (!!current.value && remoteAllowed.value.has(current.value.threadId)))

const labelPills = computed(() => {
  const m = current.value
  if (!m) return [] as { label: Label; detail?: MessageLabel }[]
  const details = new Map((m.labelDetails ?? []).map((d) => [d.labelId, d]))
  return m.labels.map((id) => ({ label: settings.labelMap.get(id), detail: details.get(id) })).filter((x): x is { label: Label; detail: MessageLabel | undefined } => !!x.label)
})

/** "Load images": the backend strips remote content, so refetch with it kept. */
async function loadImages(threadId: string) {
  mail.remoteAllowed = new Set([...mail.remoteAllowed, threadId])
  if (!hasMethod('GetMessageWithRemoteContent')) return
  const t = token
  const source = thread.value
  try {
    const updated = await Promise.all(source.map((m) => (m.remoteContentBlocked || m.hasRemoteContent ? api.getMessageWithRemoteContent(m.id) : Promise.resolve(m))))
    if (t !== token || thread.value !== source) return // another message was opened meanwhile
    thread.value = updated
  } catch (e) {
    toast.error('Couldn’t load images', { description: errorMessage(e) })
  }
}

function toggleExpanded(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

function respond(mode: 'reply' | 'replyAll' | 'forward', m: Message | null = newest.value) {
  if (m) compose.respond(mode, m)
}

function ids() {
  return current.value ? [current.value.id] : []
}

async function summarize() {
  const m = current.value
  if (!m) return
  summary.value = { id: m.id, text: '', loading: true }
  try {
    const text = await api.summarizeMessage(m.id)
    if (summary.value?.id === m.id) summary.value = { id: m.id, text, loading: false }
  } catch (e) {
    summary.value = null
    toast.error('Could not summarize', { description: errorMessage(e) })
  }
}

const moveTargets = computed(() => {
  const m = current.value
  if (!m) return []
  return (accounts.folders[m.accountId] ?? []).filter((f) => !['starred', 'drafts', 'sent'].includes(f.kind) && !m.folderIds.includes(f.id))
})

async function moveTo(folderId: string) {
  const m = current.value
  if (m) await mail.moveToFolder([m.id], folderId)
}

const isDraft = computed(() => !!newest.value?.isDraft || newest.value?.folderIds.some((f) => accounts.folderMap.get(f)?.kind === 'drafts'))
function editDraft() {
  if (newest.value) void compose.editDraft(newest.value)
}

const labelingNow = computed(() => !!current.value && ai.labeling.has(current.value.id))
</script>

<template>
  <section class="bg-background relative flex h-full min-w-0 flex-col" aria-label="Reading pane">
    <div v-if="!mail.openId" data-drag class="absolute inset-x-0 top-0 h-[calc(52px+var(--titlebar))]" aria-hidden="true" />
    <!-- Empty -->
    <div v-if="!mail.openId" class="text-muted-foreground flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <div class="bg-muted/70 flex size-14 items-center justify-center rounded-2xl">
        <MailIcon class="size-6" />
      </div>
      <div>
        <p class="text-foreground text-sm font-medium">No message selected</p>
        <p class="mt-1 text-xs">Pick a conversation, or press <kbd class="border-border rounded border px-1 font-sans">j</kbd> to start reading.</p>
      </div>
    </div>

    <template v-else>
      <!-- Toolbar -->
      <div data-drag class="border-border flex h-[calc(52px+var(--titlebar))] shrink-0 items-center gap-0.5 border-b px-3 pt-[var(--titlebar)]">
        <template v-if="showBack">
          <Tip label="Back to list" shortcut="esc"><Button variant="ghost" size="icon-sm" aria-label="Back to list" @click="mail.open(null)"><ArrowLeftIcon /></Button></Tip>
          <div class="bg-border mx-1.5 h-5 w-px" />
        </template>
        <Tip label="Archive" shortcut="e"><Button variant="ghost" size="icon-sm" aria-label="Archive" @click="mail.archive(ids())"><ArchiveIcon /></Button></Tip>
        <Tip label="Delete" shortcut="#"><Button variant="ghost" size="icon-sm" aria-label="Delete" @click="mail.trash(ids())"><Trash2Icon /></Button></Tip>
        <Tip :label="current?.unread ? 'Mark as read' : 'Mark as unread'" shortcut="u">
          <Button variant="ghost" size="icon-sm" aria-label="Toggle read" @click="current && mail.setRead(ids(), current.unread)">
            <MailOpenIcon v-if="current?.unread" /><MailIcon v-else />
          </Button>
        </Tip>
        <Tip :label="current?.starred ? 'Unstar' : 'Star'" shortcut="s">
          <Button variant="ghost" size="icon-sm" aria-label="Star" @click="current && mail.setStarred(ids(), !current.starred)">
            <StarIcon :class="current?.starred && 'fill-amber-400 text-amber-400'" />
          </Button>
        </Tip>
        <div class="bg-border mx-1.5 h-5 w-px" />
        <LabelPopover v-if="current" :message="current" />
        <Tip label="Label with AI">
          <Button variant="ghost" size="icon-sm" aria-label="Label with AI" :disabled="labelingNow" @click="current && ai.labelMessages([current.id])">
            <LoaderIcon v-if="labelingNow" class="animate-spin" />
            <SparklesIcon v-else class="text-violet-500" />
          </Button>
        </Tip>
        <div class="flex-1" />
        <Button v-if="isDraft" size="sm" class="mr-1 gap-1.5" @click="editDraft"><PencilIcon /> Edit draft</Button>
        <Tip label="Reply" shortcut="r"><Button variant="ghost" size="icon-sm" aria-label="Reply" @click="respond('reply')"><CornerUpLeftIcon /></Button></Tip>
        <Tip label="Reply all" shortcut="a"><Button variant="ghost" size="icon-sm" aria-label="Reply all" @click="respond('replyAll')"><ReplyAllIcon /></Button></Tip>
        <Tip label="Forward" shortcut="f"><Button variant="ghost" size="icon-sm" aria-label="Forward" @click="respond('forward')"><ForwardIcon /></Button></Tip>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon-sm" aria-label="More actions"><MoreHorizontalIcon /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-56">
            <DropdownMenuItem v-if="settings.aiReady" @click="summarize"><TextQuoteIcon /> Summarize</DropdownMenuItem>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger class="gap-2"><FolderInputIcon class="size-4" /> Move to</DropdownMenuSubTrigger>
              <DropdownMenuSubContent class="w-48">
                <DropdownMenuItem v-for="f in moveTargets" :key="f.id" @click="moveTo(f.id)">
                  <component :is="FOLDER_ICONS[f.kind] ?? FOLDER_ICONS.custom" /> {{ f.kind === 'spam' ? 'Junk' : f.name }}
                </DropdownMenuItem>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuSeparator />
            <DropdownMenuItem @click="mail.open(null)"><XIcon /> Close <DropdownMenuShortcut>Esc</DropdownMenuShortcut></DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div ref="scroller" class="thin-scroll min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable]">
        <div :class="showBack ? 'mx-auto w-full max-w-4xl' : ''">
        <div v-if="!threadMatches && !error" class="space-y-4 px-6 py-6">
          <Skeleton class="h-6 w-2/3" />
          <div class="flex items-center gap-3"><Skeleton class="size-9 rounded-full" /><div class="space-y-2"><Skeleton class="h-3.5 w-40" /><Skeleton class="h-3 w-56" /></div></div>
          <Skeleton class="h-48 w-full" />
        </div>
        <div v-else-if="error" class="flex flex-col items-center gap-3 p-10 text-center">
          <p class="text-sm font-medium">Couldn’t open this message</p>
          <p class="text-muted-foreground max-w-sm text-xs">{{ error }}</p>
          <Button variant="outline" size="sm" @click="mail.openId && load(mail.openId)">Try again</Button>
        </div>
        <template v-else-if="current">
          <!-- Subject & labels -->
          <div class="px-6 pt-5 pb-3">
            <h2 class="selectable text-[19px] leading-snug font-semibold tracking-tight text-balance">{{ current.subject || '(no subject)' }}</h2>
            <div class="mt-2 flex flex-wrap items-center gap-1.5">
              <span v-if="account && accounts.accounts.length > 1" class="text-muted-foreground mr-1 inline-flex items-center gap-1.5 text-xs">
                <span class="size-2 rounded-full" :style="{ background: account.color }" /> {{ account.displayName || account.email }}
              </span>
              <LabelPill v-for="p in labelPills" :key="p.label.id" :label="p.label" :detail="p.detail" size="sm" removable @remove="mail.removeLabel(ids(), p.label.id, true)" />
              <span v-if="thread.length > 1" class="text-muted-foreground text-xs">{{ thread.length }} messages</span>
            </div>
          </div>

          <!-- AI summary -->
          <div v-if="summary" class="mx-6 mb-3 rounded-lg border border-violet-500/20 bg-violet-500/5 px-4 py-3">
            <div class="mb-1 flex items-center gap-1.5 text-xs font-medium text-violet-600 dark:text-violet-400">
              <SparklesIcon class="size-3.5" /> Summary
              <button type="button" class="text-muted-foreground hover:text-foreground ml-auto" aria-label="Close summary" @click="summary = null"><XIcon class="size-3.5" /></button>
            </div>
            <p v-if="summary.loading" class="ai-shimmer text-sm">Reading the conversation…</p>
            <p v-else class="selectable text-sm leading-relaxed whitespace-pre-line">{{ summary.text }}</p>
          </div>

          <InviteCard v-if="invite" :key="invite.uid" :invite="invite" class="mb-4" />

          <div class="border-border/70 border-t">
            <ThreadMessage
              v-for="m in thread"
              :key="m.id"
              :message="m"
              :expanded="expanded.has(m.id)"
              :own-email="ownEmail"
              :allow-remote="allowRemote"
              @toggle="toggleExpanded(m.id)"
              @reply="respond('reply', m)"
              @load-images="loadImages(m.threadId)"
            />
          </div>

          <!-- Quick reply -->
          <div class="px-6 pt-2 pb-8">
            <button
              type="button"
              class="border-border text-muted-foreground hover:text-foreground hover:bg-muted/40 flex w-full items-center gap-2 rounded-lg border px-4 py-3 text-left text-sm transition-colors"
              @click="respond('reply')"
            >
              <CornerUpLeftIcon class="size-4" />
              Reply to {{ newest ? displayName(newest.from) : '' }}…
            </button>
          </div>
        </template>
        </div>
      </div>
    </template>
  </section>
</template>
