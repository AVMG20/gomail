<script setup lang="ts">
import { computed, ref, shallowRef, watch, type Component } from 'vue'
import DOMPurify from 'dompurify'
import {
  ArchiveIcon, CheckIcon, ClockIcon, CornerUpLeftIcon, KeyboardIcon, LayersIcon, MoonIcon, PencilIcon, PlusIcon,
  RefreshCwIcon, SearchIcon, SettingsIcon, SparklesIcon, TagIcon, LoaderIcon, ForwardIcon, CornerDownLeftIcon,
} from '@lucide/vue'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import CmdRoot from '@/components/common/cmd/CmdRoot.vue'
import CmdInput from '@/components/common/cmd/CmdInput.vue'
import CmdList from '@/components/common/cmd/CmdList.vue'
import CmdGroup from '@/components/common/cmd/CmdGroup.vue'
import CmdItem from '@/components/common/cmd/CmdItem.vue'
import KeyCaps from '@/components/common/KeyCaps.vue'
import LabelPill from '@/components/common/LabelPill.vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { FOLDER_ICONS } from '@/components/mail/folderIcons'
import { api } from '@/lib/api'
import { fuzzyFilter } from '@/lib/fuzzy'
import { displayName, listDate } from '@/lib/format'
import { hasMod, shortcutLabel } from '@/lib/platform'
import { currentIsDark } from '@/lib/theme'
import type { Label, MessageSummary, SearchResult } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const mail = useMailStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const compose = useComposeStore()
const ai = useAiStore()

const RECENT_KEY = 'gomail.recentSearches.v1'
const query = ref('')
const scopeAll = ref(true)
const results = shallowRef<SearchResult[]>([])
const searching = ref(false)
const recent = ref<string[]>(loadRecent())

function loadRecent(): string[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]') as string[]
  } catch {
    return []
  }
}
function pushRecent(q: string) {
  const s = q.trim()
  if (!s) return
  recent.value = [s, ...recent.value.filter((x) => x !== s)].slice(0, 6)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(recent.value))
  } catch {
    /* ignore */
  }
}

watch(() => ui.paletteOpen, (open) => {
  if (open) {
    query.value = ''
    results.value = []
    scopeAll.value = true
  }
})

const scopeAccount = computed(() => (scopeAll.value ? '' : mail.mailbox.accountId))
const currentAccount = computed(() => mail.mailboxAccount)

// ---- live server search (debounced, stale responses dropped)
let token = 0
let timer: ReturnType<typeof setTimeout> | null = null
watch([query, scopeAccount, () => mail.labelFilter.labels.join(',')], () => {
  if (timer) clearTimeout(timer)
  const q = query.value.trim()
  if (!q || /^\S+:$/.test(q)) {
    token++
    results.value = []
    searching.value = false
    return
  }
  searching.value = true
  timer = setTimeout(async () => {
    const t = ++token
    try {
      const res = await api.searchMessages({ query: q, accountId: scopeAccount.value, labels: mail.labelFilter.labels, limit: 20 })
      if (t === token) results.value = res
    } catch {
      if (t === token) results.value = []
    } finally {
      if (t === token) searching.value = false
    }
  }, 120)
})

function safeHighlight(html: string) {
  return DOMPurify.sanitize(html, { ALLOWED_TAGS: ['mark'], ALLOWED_ATTR: [] })
}

// ---- operator completions (label:, from:, is:, has:, in:)
const lastToken = computed(() => {
  const m = query.value.match(/(\S*)$/)
  return m ? m[1] : ''
})
interface Completion { value: string; label: string; hint?: string; color?: string }
const completions = computed<Completion[]>(() => {
  const tok = lastToken.value.toLowerCase()
  const m = tok.match(/^(label|from|is|has|in):(.*)$/)
  if (!m) {
    if (tok.length >= 2) {
      return ['from:', 'label:', 'is:unread', 'has:attachment', 'in:'].filter((o) => o.startsWith(tok) && o !== tok).map((o) => ({ value: o, label: o }))
    }
    return []
  }
  const [, op, rest] = m
  if (op === 'label') {
    return fuzzyFilter(rest, settings.labels, (l) => l.name, 8).map((l) => ({ value: `label:${l.name.includes(' ') ? `"${l.name}"` : l.name}`, label: l.name, color: l.color }))
  }
  if (op === 'from') {
    const list = [...mail.addressBook.values()]
    return fuzzyFilter(rest, list, (a) => `${a.name} ${a.email}`, 8).map((a) => ({ value: `from:${a.email}`, label: a.name || a.email, hint: a.name ? a.email : '' }))
  }
  const opts: Record<string, string[]> = { is: ['unread', 'read', 'starred'], has: ['attachment'], in: ['inbox', 'sent', 'drafts', 'archive', 'spam', 'trash', 'starred'] }
  return (opts[op] ?? []).filter((o) => o.startsWith(rest)).map((o) => ({ value: `${op}:${o}`, label: `${op}:${o}` }))
})

function complete(c: Completion) {
  query.value = query.value.replace(/(\S*)$/, `${c.value} `)
}

// ---- local fuzzy sections
const q = computed(() => query.value.trim())
const labelItems = computed<Label[]>(() => (q.value.includes(':') ? [] : fuzzyFilter(q.value, settings.labels, (l) => l.name, q.value ? 3 : 0)))

interface MailboxItem { key: string; title: string; subtitle: string; icon?: Component; accountId: string; folderId: string; kind: string; isAccount?: boolean }
const mailboxItems = computed<MailboxItem[]>(() => {
  const list: MailboxItem[] = []
  if (accounts.accounts.length > 1) list.push({ key: 'all', title: 'All inboxes', subtitle: 'Combined view', icon: LayersIcon, accountId: '', folderId: '', kind: 'inbox' })
  for (const a of accounts.accounts) {
    list.push({ key: a.id, title: `Switch to ${a.displayName || a.email}`, subtitle: a.email, accountId: a.id, folderId: '', kind: '', isAccount: true })
    for (const f of accounts.folders[a.id] ?? []) {
      list.push({ key: f.id, title: f.kind === 'spam' ? 'Junk' : f.name, subtitle: a.displayName || a.email, icon: FOLDER_ICONS[f.kind] ?? FOLDER_ICONS.custom, accountId: a.id, folderId: f.id, kind: f.kind })
    }
  }
  if (!q.value || q.value.includes(':')) return q.value ? [] : list.filter((x) => x.isAccount || x.key === 'all')
  return fuzzyFilter(q.value, list, (x) => `${x.title} ${x.subtitle}`, 6)
})

interface Action { id: string; title: string; icon: Component; shortcut?: string; run: () => void; keep?: boolean }
const actions = computed<Action[]>(() => {
  const open = mail.openSummary
  const list: Action[] = [
    { id: 'compose', title: 'Compose new message', icon: PencilIcon, shortcut: 'c', run: () => compose.compose() },
    { id: 'sync', title: 'Sync now', icon: RefreshCwIcon, run: () => void accounts.sync(mail.mailbox.accountId).catch(() => {}) },
    { id: 'settings', title: 'Open settings', icon: SettingsIcon, shortcut: 'mod+,', run: () => ui.openSettings() },
    { id: 'labels', title: 'Manage labels', icon: TagIcon, run: () => ui.openSettings('labels') },
    { id: 'theme', title: currentIsDark() ? 'Switch to light theme' : 'Switch to dark theme', icon: MoonIcon, run: () => settings.update({ theme: currentIsDark() ? 'light' : 'dark' }) },
    { id: 'shortcuts', title: 'Keyboard shortcuts', icon: KeyboardIcon, shortcut: '?', run: () => (ui.shortcutsOpen = true) },
    { id: 'add', title: 'Add account', icon: PlusIcon, run: () => (ui.addAccountOpen = true) },
  ]
  if (settings.aiReady) {
    list.push({ id: 'ai-all', title: 'Label all mail with AI…', icon: SparklesIcon, run: () => ai.requestBulk({ query: { accountId: '' }, ids: [], title: 'All mail in all accounts' }) })
  }
  if (open) {
    list.unshift(
      { id: 'reply', title: 'Reply', icon: CornerUpLeftIcon, shortcut: 'r', run: () => replyTo(open, 'reply') },
      { id: 'forward', title: 'Forward', icon: ForwardIcon, shortcut: 'f', run: () => replyTo(open, 'forward') },
      { id: 'archive', title: 'Archive conversation', icon: ArchiveIcon, shortcut: 'e', run: () => mail.archive([open.id]) },
    )
    if (settings.aiReady) list.splice(3, 0, { id: 'ai-one', title: 'Label this mail with AI', icon: SparklesIcon, run: () => ai.labelMessages([open.id]) })
  }
  if (!q.value) return list.slice(0, 6)
  if (q.value.includes(':')) return []
  return fuzzyFilter(q.value, list, (a) => a.title, 5)
})

async function replyTo(m: MessageSummary, mode: 'reply' | 'forward') {
  const thread = await mail.loadThread(m.id)
  const full = thread.find((x) => x.id === m.id) ?? thread[thread.length - 1]
  if (full) compose.respond(mode, full)
}

// ---- selection handlers
function close() {
  ui.paletteOpen = false
}

function openResult(r: SearchResult) {
  pushRecent(query.value)
  const m = r.message
  const folder = m.folderIds.map((id) => accounts.folderMap.get(id)).find((f) => f && f.kind !== 'custom') ?? accounts.folderOf(m.accountId, 'inbox')
  const sameView = mail.byId.has(m.id)
  if (!sameView) {
    mail.setMailbox({ accountId: m.accountId, folderId: folder?.id ?? '', kind: folder?.kind ?? 'inbox' })
    // Show it immediately; the list loads around it.
    setTimeout(() => mail.open(m.id), 0)
  } else mail.open(m.id)
  close()
}

function showAll() {
  const text = query.value.trim()
  if (!text) return
  pushRecent(text)
  mail.setMailbox({ accountId: scopeAccount.value, folderId: '', kind: 'all' })
  mail.search = text
  close()
}

function pickMailbox(x: MailboxItem) {
  if (x.isAccount) mail.switchAccount(x.accountId)
  else if (x.key === 'all') mail.switchAccount('')
  else mail.setMailbox({ accountId: x.accountId, folderId: x.folderId, kind: x.kind })
  close()
}

function runAction(a: Action) {
  close()
  setTimeout(a.run, 0)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && hasMod(e)) {
    e.preventDefault()
    e.stopPropagation()
    showAll()
  } else if (e.key === 'Backspace' && !query.value && mail.labelFilter.labels.length) {
    mail.toggleLabelFilter(mail.labelFilter.labels[mail.labelFilter.labels.length - 1])
  }
}

const activeLabels = computed(() => mail.labelFilter.labels.map((id) => settings.labelMap.get(id)).filter((l): l is Label => !!l))
const nothing = computed(() => q.value && !searching.value && !results.value.length && !labelItems.value.length && !mailboxItems.value.length && !actions.value.length && !completions.value.length)
const accountOf = (id: string) => accounts.accountMap.get(id)
const tipKeys = shortcutLabel('mod+enter')
</script>

<template>
  <Dialog v-model:open="ui.paletteOpen">
    <DialogContent class="top-[12vh] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-[640px]" :show-close-button="false" @keydown.capture="onKeydown">
      <DialogTitle class="sr-only">Search and commands</DialogTitle>
      <DialogDescription class="sr-only">Search all mail, jump to mailboxes, toggle labels or run actions.</DialogDescription>
      <CmdRoot>
        <CmdInput v-model="query" placeholder="Search mail, labels, mailboxes, actions…" auto-focus class="h-12 text-[15px]">
          <template #icon>
            <LoaderIcon v-if="searching" class="text-muted-foreground size-4 shrink-0 animate-spin" />
            <SearchIcon v-else class="text-muted-foreground size-4 shrink-0" />
          </template>
        </CmdInput>

        <!-- Scope chips -->
        <div class="border-border flex flex-wrap items-center gap-1.5 border-b px-3 py-2">
          <button
            type="button"
            :class="['inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors', scopeAll ? 'bg-foreground text-background border-transparent' : 'border-border text-muted-foreground hover:text-foreground']"
            @click="scopeAll = true"
          >
            <LayersIcon class="size-3" /> All accounts
          </button>
          <button
            v-if="currentAccount"
            type="button"
            :class="['inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors', !scopeAll ? 'bg-foreground text-background border-transparent' : 'border-border text-muted-foreground hover:text-foreground']"
            @click="scopeAll = false"
          >
            <span class="size-2 rounded-full" :style="{ background: currentAccount.color }" /> {{ currentAccount.displayName || currentAccount.email }}
          </button>
          <LabelPill v-for="l in activeLabels" :key="l.id" :label="l" size="sm" removable @remove="mail.toggleLabelFilter(l.id)" />
        </div>

        <CmdList class="max-h-[min(460px,60vh)]">
          <!-- Empty input: recent searches -->
          <CmdGroup v-if="!q && recent.length" heading="Recent searches">
            <CmdItem v-for="r in recent" :key="r" :value="`recent:${r}`" @select="query = r">
              <ClockIcon class="text-muted-foreground size-4" />
              <span class="flex-1 truncate">{{ r }}</span>
            </CmdItem>
          </CmdGroup>

          <CmdGroup v-if="completions.length" heading="Suggestions">
            <CmdItem v-for="c in completions" :key="c.value" :value="`complete:${c.value}`" @select="complete(c)">
              <span v-if="c.color" class="size-2.5 rounded-full" :style="{ background: c.color }" />
              <SearchIcon v-else class="text-muted-foreground size-4" />
              <span class="truncate">{{ c.label }}</span>
              <span v-if="c.hint" class="text-muted-foreground truncate text-xs">{{ c.hint }}</span>
            </CmdItem>
          </CmdGroup>

          <CmdGroup v-if="labelItems.length" heading="Labels">
            <CmdItem v-for="l in labelItems" :key="l.id" :value="`label:${l.id}`" @select="mail.toggleLabelFilter(l.id)">
              <span class="size-2.5 rounded-full" :style="{ background: l.color }" />
              <span class="flex-1 truncate">{{ l.name }}</span>
              <span class="text-muted-foreground text-xs">{{ mail.labelFilter.labels.includes(l.id) ? 'Filtering' : 'Filter list' }}</span>
              <CheckIcon class="size-4" :class="mail.labelFilter.labels.includes(l.id) ? 'opacity-100' : 'opacity-0'" />
            </CmdItem>
          </CmdGroup>

          <!-- Server results (backend ranking preserved) -->
          <CmdGroup v-if="results.length" heading="Mail">
            <CmdItem v-for="r in results" :key="r.message.id" :value="`mail:${r.message.id}`" class="items-start py-2" @select="openResult(r)">
              <span class="mt-1.5 size-2 shrink-0 rounded-full" :style="{ background: accountOf(r.message.accountId)?.color ?? 'var(--muted-foreground)' }" :title="accountOf(r.message.accountId)?.email" />
              <span class="min-w-0 flex-1">
                <span class="flex items-center gap-2">
                  <span :class="['truncate text-[13px]', r.message.unread ? 'font-semibold' : 'font-medium']">{{ displayName(r.message.from) }}</span>
                  <span class="text-muted-foreground ml-auto shrink-0 text-[11px]">{{ listDate(r.message.date) }}</span>
                </span>
                <span class="block truncate text-[13px]">{{ r.message.subject || '(no subject)' }}</span>
                <!-- eslint-disable-next-line vue/no-v-html -- sanitized to <mark> only -->
                <span class="text-muted-foreground line-clamp-1 text-xs" v-html="safeHighlight(r.highlight)" />
              </span>
              <span v-if="r.message.labels.length" class="mt-0.5 flex shrink-0 gap-1">
                <template v-for="id in r.message.labels.slice(0, 2)" :key="id">
                  <LabelPill v-if="settings.labelMap.get(id)" :label="settings.labelMap.get(id)!" />
                </template>
              </span>
            </CmdItem>
            <CmdItem :value="'mail:all'" @select="showAll">
              <SearchIcon class="text-muted-foreground size-4" />
              <span class="flex-1">Show all results for “{{ q }}”</span>
              <KeyCaps combo="mod+enter" />
            </CmdItem>
          </CmdGroup>

          <CmdGroup v-if="mailboxItems.length" heading="Mailboxes">
            <CmdItem v-for="(x, i) in mailboxItems" :key="x.key" :value="`mb:${x.key}`" @select="pickMailbox(x)">
              <AccountAvatar v-if="x.isAccount && accountOf(x.accountId)" :account="accountOf(x.accountId)!" :size="18" :glyph="false" />
              <component :is="x.icon" v-else class="text-muted-foreground size-4" />
              <span class="truncate">{{ x.title }}</span>
              <span class="text-muted-foreground truncate text-xs">{{ x.subtitle }}</span>
              <span class="ml-auto" />
              <KeyCaps v-if="x.isAccount && !q && i < 10" :combo="`mod+${accounts.accounts.findIndex((a) => a.id === x.accountId) + 1}`" />
              <KeyCaps v-else-if="x.key === 'all' && !q" combo="mod+0" />
            </CmdItem>
          </CmdGroup>

          <CmdGroup v-if="actions.length" heading="Actions">
            <CmdItem v-for="a in actions" :key="a.id" :value="`act:${a.id}`" @select="runAction(a)">
              <component :is="a.icon" class="text-muted-foreground size-4" :class="a.icon === SparklesIcon && 'text-violet-500'" />
              <span class="flex-1 truncate">{{ a.title }}</span>
              <KeyCaps v-if="a.shortcut" :combo="a.shortcut" />
            </CmdItem>
          </CmdGroup>

          <div v-if="nothing" class="px-6 py-10 text-center">
            <p class="text-sm font-medium">No results for “{{ q }}”</p>
            <p class="text-muted-foreground mx-auto mt-2 max-w-sm text-xs leading-relaxed">
              Try <code class="bg-muted rounded px-1">from:priya</code>, <code class="bg-muted rounded px-1">label:finance</code>,
              <code class="bg-muted rounded px-1">is:unread</code>, <code class="bg-muted rounded px-1">has:attachment</code> or a <code class="bg-muted rounded px-1">"quoted phrase"</code>.
            </p>
          </div>
          <div v-if="!q && !recent.length" class="text-muted-foreground px-3 pt-1 pb-2 text-xs">
            Tip: search across every account. Use <code class="bg-muted rounded px-1">from:</code>, <code class="bg-muted rounded px-1">label:</code> and <code class="bg-muted rounded px-1">is:unread</code> to narrow down.
          </div>
        </CmdList>

        <div class="border-border text-muted-foreground flex items-center gap-4 border-t px-3 py-2 text-[11px]">
          <span class="flex items-center gap-1"><KeyCaps combo="up" /><KeyCaps combo="down" /> navigate</span>
          <span class="flex items-center gap-1"><CornerDownLeftIcon class="size-3" /> open</span>
          <span class="flex items-center gap-1">{{ tipKeys }} show all</span>
          <span class="ml-auto flex items-center gap-1"><KeyCaps combo="esc" /> close</span>
        </div>
      </CmdRoot>
    </DialogContent>
  </Dialog>
</template>
