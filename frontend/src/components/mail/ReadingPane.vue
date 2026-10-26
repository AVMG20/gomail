<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useElementSize } from '@vueuse/core'
import {
  ArchiveIcon, CornerUpLeftIcon, ForwardIcon, MailIcon, MoreHorizontalIcon, ReplyAllIcon, SparklesIcon, StarIcon,
  Trash2Icon, FolderInputIcon, TextQuoteIcon, LoaderIcon, MailOpenIcon, XIcon, ArrowLeftIcon, PencilIcon, OctagonAlertIcon, InboxIcon,
  ContrastIcon,
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
import { themeState } from '@/lib/theme'
import { needsRemoteRefetch, normalizeSenders, remoteAllowedFor } from '@/lib/trust'
import type { Label, Message, MessageLabel } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { FOLDER_ICONS } from './folderIcons'
import LabelPopover from './LabelPopover.vue'
import SenderRulesMenu from './SenderRulesMenu.vue'
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
/** A narrow pane can't fit every toolbar button: Reply all, Forward and Label with AI move into "More". */
const toolbar = ref<HTMLElement | null>(null)
const { width: toolbarWidth } = useElementSize(toolbar)
const compactBar = computed(() => toolbarWidth.value > 0 && toolbarWidth.value < 500)

let token = 0
let timer: ReturnType<typeof setTimeout> | null = null

function applyThread(id: string, list: Message[]) {
  thread.value = list
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
/** Experimental "All mail dark" is on: the toolbar offers the original colors for this conversation. */
const darkenOn = computed(() => themeState.value.dark && settings.settings?.emailBackground === 'dark')
const originalColors = computed(() => !!current.value && mail.originalColors.has(current.value.threadId))
function toggleOriginalColors() {
  const t = current.value?.threadId
  if (!t) return
  const next = new Set(mail.originalColors)
  if (next.has(t)) next.delete(t)
  else next.add(t)
  mail.originalColors = next
}
const ownEmail = computed(() => (current.value ? accounts.accountMap.get(current.value.accountId)?.email ?? '' : ''))
const account = computed(() => (current.value ? accounts.accountMap.get(current.value.accountId) : undefined))
const trustedSenders = computed(() => settings.settings?.trustedImageSenders ?? [])
/** Remote images for this message: global switch, trusted sender, or "Load images" for the conversation. */
function allowRemoteFor(m: Message): boolean {
  return remoteAllowedFor(m, { showRemoteImages: settings.settings?.showRemoteImages, trustedImageSenders: trustedSenders.value, allowedThreads: remoteAllowed.value })
}

const labelPills = computed(() => {
  const m = current.value
  if (!m) return [] as { label: Label; detail?: MessageLabel }[]
  const details = new Map((m.labelDetails ?? []).map((d) => [d.labelId, d]))
  return m.labels.map((id) => ({ label: settings.labelMap.get(id), detail: details.get(id) })).filter((x): x is { label: Label; detail: MessageLabel | undefined } => !!x.label)
})

/** "Load images": allow this conversation; ensureRemoteBodies() fetches the unstripped bodies. */
function loadImages(threadId: string) {
  mail.remoteAllowed = new Set([...mail.remoteAllowed, threadId])
}

/** "Always load images from <sender>": stored in settings; the backend stops stripping their mail. */
function trustSender(m: Message) {
  const list = settings.settings?.trustedImageSenders ?? []
  void settings.update({ trustedImageSenders: normalizeSenders([...list, m.from.email]) }, 0)
  toast(`Images from ${m.from.email} will always load`, { description: 'Change this in Settings → Mail → Privacy.' })
}

// The backend strips remote content from GetThread/GetMessage. Whenever a shown message is
// allowed to load images but its body is still stripped, fetch it with remote content kept.
// Every path converges here (the "Load images" button, a trusted sender, the global switch,
// a thread reloaded by a background refresh), and results are merged by id into whatever
// is shown at that moment, so a reload in between can't drop them.
const fetchingRemote = new Set<string>()
async function ensureRemoteBodies() {
  if (!hasMethod('GetMessageWithRemoteContent')) return
  const want = thread.value.filter((m) => !fetchingRemote.has(m.id) && needsRemoteRefetch(m, allowRemoteFor(m)))
  if (!want.length) return
  want.forEach((m) => fetchingRemote.add(m.id))
  try {
    const got = await Promise.all(want.map((m) => api.getMessageWithRemoteContent(m.id)))
    // GetMessageWithRemoteContent never strips; mark it so this can't loop.
    const fresh = got.map((m) => ({ id: m.id, bodyHtml: m.bodyHtml, hasRemoteContent: m.hasRemoteContent, remoteContentBlocked: false }))
    mail.updateCachedBodies(fresh)
    const byMsg = new Map(fresh.map((m) => [m.id, m]))
    if (thread.value.some((m) => byMsg.has(m.id))) thread.value = thread.value.map((m) => (byMsg.has(m.id) ? { ...m, ...byMsg.get(m.id)! } : m))
  } catch (e) {
    toast.error('Couldn’t load images', { description: errorMessage(e) })
  } finally {
    want.forEach((m) => fetchingRemote.delete(m.id))
  }
}
watch([thread, remoteAllowed, trustedSenders, () => settings.settings?.showRemoteImages], () => void ensureRemoteBodies(), { immediate: true })

function toggleExpanded(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

function respond(mode: 'reply' | 'replyAll' | 'forward', m: Message | null = replyTo.value) {
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
  return (accounts.folders[m.accountId] ?? []).filter((f) => !['starred', 'drafts', 'sent', 'spam'].includes(f.kind) && !m.folderIds.includes(f.id))
})

async function moveTo(folderId: string) {
  const m = current.value
  if (m) await mail.moveToFolder([m.id], folderId)
}

const isDraftMsg = compose.isDraftMessage
const isDraft = computed(() => !!newest.value && isDraftMsg(newest.value))
/** The thread minus drafts still being written: autosave stores them on the server, and a half-typed
 *  reply must not look like a sent message. Gmail gives each save a new id, so while a composer replies
 *  into this thread every draft in it is hidden. */
const shownThread = computed(() => {
  const ids = new Set(thread.value.map((m) => m.id))
  const open = compose.drafts.filter((d) => !d.queued)
  const openIds = new Set(open.map((d) => d.remoteId).filter(Boolean))
  const replying = open.some((d) => d.replyToId && ids.has(d.replyToId))
  const list = thread.value.filter((m) => !openIds.has(m.id) && !(replying && isDraftMsg(m)))
  return list.length ? list : thread.value
})
function editDraft(m: Message | null = newest.value) {
  if (m) void compose.editDraft(m)
}
/** What Reply / Forward answer: the newest message that isn't a draft (an autosaved reply ends the
 *  thread), or with conversations off the message opened. */
const replyTo = computed(() => (mail.openId ? compose.replyTargetFor(shownThread.value, mail.openId) ?? null : null))

const labelingNow = computed(() => !!current.value && ai.labeling.has(current.value.id))
</script>

<template>
  <section class="bg-background relative flex h-full min-w-0 flex-col" aria-label="Reading pane">
    <div v-if="!mail.openId" data-drag class="absolute inset-x-0 top-0 h-[52px]" aria-hidden="true" />
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
      <div ref="toolbar" data-drag class="border-border flex h-[52px] shrink-0 items-center gap-0.5 border-b px-3">
        <template v-if="showBack">
          <Tip label="Back to list" shortcut="esc"><Button variant="ghost" size="icon-sm" aria-label="Back to list" @click="mail.open(null)"><ArrowLeftIcon /></Button></Tip>
          <div class="bg-border mx-1.5 h-5 w-px" />
        </template>
        <Tip label="Archive" shortcut="e"><Button variant="ghost" size="icon-sm" aria-label="Archive" @click="mail.archive(ids())"><ArchiveIcon /></Button></Tip>
        <Tip label="Delete" shortcut="#"><Button variant="ghost" size="icon-sm" aria-label="Delete" @click="mail.trash(ids())"><Trash2Icon /></Button></Tip>
        <Tip v-if="mail.mailbox.kind !== 'spam'" label="Move to Junk" shortcut="!"><Button variant="ghost" size="icon-sm" aria-label="Move to Junk" @click="mail.junk(ids())"><OctagonAlertIcon /></Button></Tip>
        <Tip v-else label="Not junk" shortcut="!"><Button variant="ghost" size="icon-sm" aria-label="Not junk" @click="mail.notJunk(ids())"><InboxIcon /></Button></Tip>
        <Tip :label="current?.unread ? 'Mark as read' : 'Mark as unread'" :shortcut="current?.unread ? 'shift+i' : 'u'">
          <Button variant="ghost" size="icon-sm" :aria-label="current?.unread ? 'Mark as read' : 'Mark as unread'" @click="current && mail.setRead(ids(), current.unread)">
            <MailOpenIcon v-if="current?.unread" /><MailIcon v-else />
          </Button>
        </Tip>
        <Tip :label="current?.starred ? 'Unstar' : 'Star'" shortcut="s">
          <Button variant="ghost" size="icon-sm" :aria-label="current?.starred ? 'Unstar' : 'Star'" @click="current && mail.setStarred(ids(), !current.starred)">
            <StarIcon :class="current?.starred && 'fill-amber-400 text-amber-400'" />
          </Button>
        </Tip>
        <div class="bg-border mx-1.5 h-5 w-px" />
        <LabelPopover v-if="current" :message="current" />
        <Tip v-if="!compactBar" label="Label with AI">
          <Button variant="ghost" size="icon-sm" aria-label="Label with AI" :disabled="labelingNow" @click="current && ai.labelMessages([current.id])">
            <LoaderIcon v-if="labelingNow" class="animate-spin" />
            <SparklesIcon v-else class="text-violet-500" />
          </Button>
        </Tip>
        <Tip v-if="darkenOn" :label="originalColors ? 'Show in dark colors' : 'Show original colors'">
          <Button variant="ghost" size="icon-sm" :aria-label="originalColors ? 'Show in dark colors' : 'Show original colors'" :aria-pressed="originalColors" :class="originalColors && 'bg-muted text-foreground'" @click="toggleOriginalColors"><ContrastIcon /></Button>
        </Tip>
        <div class="flex-1" />
        <Button v-if="isDraft" size="sm" class="mr-1 gap-1.5" @click="editDraft"><PencilIcon /> Edit draft</Button>
        <Tip label="Reply" shortcut="r"><Button variant="ghost" size="icon-sm" aria-label="Reply" @click="respond('reply')"><CornerUpLeftIcon /></Button></Tip>
        <Tip v-if="!compactBar" label="Reply all" shortcut="a"><Button variant="ghost" size="icon-sm" aria-label="Reply all" @click="respond('replyAll')"><ReplyAllIcon /></Button></Tip>
        <Tip v-if="!compactBar" label="Forward" shortcut="f"><Button variant="ghost" size="icon-sm" aria-label="Forward" @click="respond('forward')"><ForwardIcon /></Button></Tip>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon-sm" aria-label="More actions"><MoreHorizontalIcon /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-56">
            <template v-if="compactBar">
              <DropdownMenuItem @click="respond('replyAll')"><ReplyAllIcon /> Reply all <DropdownMenuShortcut>A</DropdownMenuShortcut></DropdownMenuItem>
              <DropdownMenuItem @click="respond('forward')"><ForwardIcon /> Forward <DropdownMenuShortcut>F</DropdownMenuShortcut></DropdownMenuItem>
              <DropdownMenuItem :disabled="labelingNow" @click="current && ai.labelMessages([current.id])"><SparklesIcon class="text-violet-500" /> Label with AI</DropdownMenuItem>
              <DropdownMenuSeparator />
            </template>
            <DropdownMenuItem v-if="settings.aiReady" @click="summarize"><TextQuoteIcon /> Summarize</DropdownMenuItem>
            <DropdownMenuSub v-if="moveTargets.length">
              <DropdownMenuSubTrigger class="gap-2"><FolderInputIcon class="size-4" /> Move to</DropdownMenuSubTrigger>
              <DropdownMenuSubContent class="w-48">
                <DropdownMenuItem v-for="f in moveTargets" :key="f.id" @click="moveTo(f.id)">
                  <component :is="FOLDER_ICONS[f.kind] ?? FOLDER_ICONS.custom" /> {{ f.kind === 'spam' ? 'Junk' : f.name }}
                </DropdownMenuItem>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <SenderRulesMenu v-if="current" menu="dropdown" :message="current" />
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
              <span v-if="shownThread.length > 1" class="text-muted-foreground text-xs">{{ shownThread.length }} messages</span>
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
              v-for="m in shownThread"
              :key="m.id"
              :message="m"
              :expanded="expanded.has(m.id)"
              :own-email="ownEmail"
              :allow-remote="allowRemoteFor(m)"
              @toggle="toggleExpanded(m.id)"
              @respond="(mode) => respond(mode, m)"
              @edit="editDraft(m)"
              @load-images="loadImages(m.threadId)"
              @trust-sender="trustSender(m)"
            />
          </div>

          <!-- Quick reply -->
          <div v-if="replyTo && !isDraftMsg(replyTo)" class="px-6 pt-2 pb-8">
            <button
              type="button"
              class="border-border text-muted-foreground hover:text-foreground hover:bg-muted/40 flex w-full items-center gap-2 rounded-lg border px-4 py-3 text-left text-sm transition-colors"
              @click="respond('reply')"
            >
              <CornerUpLeftIcon class="size-4" />
              Reply to {{ displayName(replyTo.from) }}…
            </button>
          </div>
        </template>
        </div>
      </div>
    </template>
  </section>
</template>
