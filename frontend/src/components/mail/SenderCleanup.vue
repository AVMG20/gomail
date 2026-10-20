<script setup lang="ts">
// "Clean up by sender": who fills the inbox, biggest first, with one decision per sender: send
// everything to Junk (now and from now on), always label it, archive what's there, or keep it.
// Keyboard first: arrows pick a sender, J / L / A / K act, Enter shows the sender's mail.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { ArchiveIcon, CheckIcon, ListFilterIcon, LoaderIcon, MailMinusIcon, SearchIcon, ShieldBanIcon, TagIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import KeyCaps from '@/components/common/KeyCaps.vue'
import SenderAvatar from '@/components/common/SenderAvatar.vue'
import Tip from '@/components/common/Tip.vue'
import SegmentedControl from '@/components/settings/SegmentedControl.vue'
import { api, errorMessage } from '@/lib/api'
import { announce } from '@/lib/announce'
import { ruleTarget } from '@/lib/junk'
import {
  filterSenders, senderRowKey, senderRules, senderScope, senderSearch, senderStats, senderTitle, type SenderGrouping, type SenderSummary,
} from '@/lib/senders'
import { useAccountsStore } from '@/stores/accounts'
import { useCleanupStore } from '@/stores/cleanup'
import { inChunks, useMailStore } from '@/stores/mail'
import { useSenderRulesStore } from '@/stores/senderRules'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const KIND = 'inbox'
const LIMIT = 300

const cleanup = useCleanupStore()
const accounts = useAccountsStore()
const mail = useMailStore()
const rules = useSenderRulesStore()
const settings = useSettingsStore()
const ui = useUiStore()

const grouping = ref<SenderGrouping>('address')
const filter = ref('')
const list = ref<SenderSummary[]>([])
const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
/** Rows being junked or archived: hidden until the next load confirms. */
const pending = ref<Set<string>>(new Set())
const active = ref(0)
const labelMenu = ref('')
const searchEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

const multi = computed(() => accounts.accounts.length > 1)
const ownEmail = (accountId: string) => accounts.accountMap.get(accountId)?.email ?? ''
const mailboxName = (accountId: string) => {
  const a = accounts.accountMap.get(accountId)
  return a ? a.displayName || a.email : ''
}
const title = computed(() => (cleanup.accountId ? `Clean up ${mailboxName(cleanup.accountId) || 'the inbox'}` : 'Clean up all inboxes'))
const mailboxValue = computed({
  get: () => cleanup.accountId || '*',
  set: (v: string) => (cleanup.accountId = v === '*' ? '' : v),
})

const rows = computed(() => filterSenders(list.value, filter.value, cleanup.kept).filter((s) => !pending.value.has(senderRowKey(s))))
const current = computed(() => rows.value[Math.min(active.value, rows.value.length - 1)] ?? null)
const keptCount = computed(() => list.value.filter((s) => cleanup.kept.has(senderRowKey(s))).length)

// ---- loading
let seq = 0
async function load() {
  const token = ++seq
  const keepKey = current.value ? senderRowKey(current.value) : ''
  if (!list.value.length) status.value = 'loading'
  try {
    const fresh = await api.topSenders(cleanup.accountId, KIND, grouping.value, LIMIT)
    if (token !== seq) return
    list.value = fresh
    pending.value = new Set()
    status.value = 'ready'
    // Stay on the same sender when it is still there; else on the same position.
    const i = keepKey ? rows.value.findIndex((s) => senderRowKey(s) === keepKey) : -1
    if (i >= 0) active.value = i
    active.value = Math.max(0, Math.min(active.value, rows.value.length - 1))
  } catch (e) {
    if (token !== seq) return
    status.value = 'error'
    error.value = errorMessage(e)
  }
}
const reloadSoon = useDebounceFn(() => void load(), 500)

watch(() => [cleanup.open, cleanup.accountId, grouping.value] as const, ([open], old) => {
  if (!open) return
  if (!old?.[0] || old[1] !== cleanup.accountId || old[2] !== grouping.value) {
    list.value = []
    active.value = 0
    filter.value = ''
  }
  void load()
}, { immediate: true })

// Mail moved elsewhere (sync, rules, Undo in a toast): the counts follow.
const off = api.on('messages:changed', () => {
  if (cleanup.open) void reloadSoon()
})
onBeforeUnmount(off)

watch(filter, () => (active.value = 0))
watch(active, () => void nextTick(() => listEl.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' })))

// ---- actions
function hide(s: SenderSummary) {
  pending.value = new Set(pending.value).add(senderRowKey(s))
}
function unhide(s: SenderSummary) {
  const next = new Set(pending.value)
  next.delete(senderRowKey(s))
  pending.value = next
}

function rulesOf(s: SenderSummary) {
  return senderRules(settings.settings?.labelRules, s)
}
function labelsOf(s: SenderSummary) {
  return settings.labelsForAccount(s.accountId).filter((l) => l.id !== 'junk')
}
const who = (s: SenderSummary) => (s.key.startsWith('@') ? s.domain : s.address)

/** Junk all: a junk rule for the sender (or company domain) and its inbox mail to Junk, read. */
async function junk(s: SenderSummary) {
  hide(s)
  focusList()
  try {
    await rules.junkSender([], senderScope(s, ownEmail(s.accountId)), { accountId: s.accountId, address: s.address })
  } finally {
    await load()
  }
}

/** Always label: a label rule, and the label on the sender's mail now (checked again: remove it). */
async function label(s: SenderSummary, labelId: string) {
  labelMenu.value = ''
  const have = rulesOf(s).label.filter((r) => r.labelId === labelId)
  const name = settings.labelMap.get(labelId)?.name ?? 'the label'
  if (have.length) await rules.removeRules(have, `${who(s)} is no longer labeled “${name}”`)
  else await rules.labelSender(s.accountId, s.address, labelId, senderScope(s, ownEmail(s.accountId)))
  void load()
  focusList()
}

/** Archive all: exactly this sender's inbox mail, with Undo back to the inbox. */
async function archive(s: SenderSummary) {
  hide(s)
  focusList()
  let ids: string[]
  try {
    ids = await api.senderMessageIds(s.accountId, KIND, s.key)
  } catch (e) {
    unhide(s)
    toast.error('Couldn’t look up the messages', { description: errorMessage(e) })
    return
  }
  if (!ids.length) {
    void load()
    return
  }
  const n = ids.length
  const what = `${n.toLocaleString()} ${n === 1 ? 'message' : 'messages'} from ${who(s)}`
  try {
    await inChunks(ids, api.archive)
    announce(`Archived ${what}`)
    toast(`Archived ${what}`, {
      action: {
        label: 'Undo',
        onClick: async () => {
          const inbox = accounts.folderOf(s.accountId, 'inbox')?.id
          try {
            if (inbox) await inChunks(ids, (c) => api.moveToFolder(c, inbox))
          } catch (e) {
            toast.error('Undo failed', { description: errorMessage(e) })
          }
          void mail.softRefresh()
          accounts.refreshCounts()
          void load()
        },
      },
    })
  } catch (e) {
    toast.error('Archive failed', { description: errorMessage(e) })
  }
  void mail.softRefresh()
  accounts.refreshCounts()
  await load()
}

function keep(s: SenderSummary) {
  cleanup.keep(senderRowKey(s))
  focusList()
}
function showKept() {
  for (const s of list.value) cleanup.unkeep(senderRowKey(s))
}

/** The sender's mail in the list behind: its mailbox's inbox, searched with from:. */
function showMail(s: SenderSummary) {
  ui.mainView = 'mail'
  mail.setMailbox(mail.inboxOf(s.accountId))
  mail.search = senderSearch(s)
  cleanup.open = false
}

// ---- keyboard
function focusList() {
  void nextTick(() => listEl.value?.focus())
}
function move(delta: number) {
  if (!rows.value.length) return
  active.value = Math.max(0, Math.min(rows.value.length - 1, active.value + delta))
}
function onListKey(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  // A focused button inside a row handles its own Enter and Space.
  if (e.target !== listEl.value && (e.key === 'Enter' || e.key === ' ')) return
  const s = current.value
  const act = (fn: () => void) => {
    e.preventDefault()
    fn()
  }
  switch (e.key) {
    case 'ArrowDown': return act(() => move(1))
    case 'ArrowUp': return act(() => (active.value === 0 ? searchEl.value?.focus() : move(-1)))
    case 'PageDown': return act(() => move(8))
    case 'PageUp': return act(() => move(-8))
    case 'Home': return act(() => (active.value = 0))
    case 'End': return act(() => (active.value = Math.max(0, rows.value.length - 1)))
    case '/': return act(() => searchEl.value?.focus())
  }
  if (!s) return
  switch (e.key.toLowerCase()) {
    case 'j': return act(() => void junk(s))
    case 'a': return act(() => void archive(s))
    case 'k': return act(() => keep(s))
    case 'l': return act(() => (labelMenu.value = senderRowKey(s)))
    case 'enter':
    case 'o': return act(() => showMail(s))
  }
}
function onSearchKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'Enter') {
    e.preventDefault()
    active.value = 0
    focusList()
  }
}
function pick(i: number) {
  active.value = i
  focusList()
}
function onOpenAuto(e: Event) {
  // Start in the list, so the single-key actions work right away.
  e.preventDefault()
  focusList()
}
</script>

<template>
  <Dialog v-model:open="cleanup.open">
    <DialogContent class="flex h-[min(720px,calc(100vh-4rem))] flex-col gap-0 overflow-hidden p-0 sm:max-w-[720px]" @open-auto-focus="onOpenAuto">
      <DialogHeader class="gap-1 px-5 pt-5 pb-3">
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>Who fills the inbox, biggest first. Decide once per sender, and new mail follows the rule.</DialogDescription>
      </DialogHeader>

      <div class="flex flex-wrap items-center gap-2 px-5 pb-3">
        <label class="border-input focus-within:border-ring focus-within:ring-ring/50 dark:bg-input/30 flex h-8 min-w-48 flex-1 items-center gap-2 rounded-md border px-2.5 focus-within:ring-3">
          <SearchIcon class="text-muted-foreground size-4 shrink-0" />
          <input
            ref="searchEl"
            v-model="filter"
            type="search"
            class="placeholder:text-muted-foreground h-full w-full min-w-0 bg-transparent text-sm outline-none"
            placeholder="Filter senders"
            aria-label="Filter senders"
            @keydown="onSearchKey"
          >
        </label>
        <Select v-if="multi" v-model="mailboxValue">
          <SelectTrigger size="sm" class="max-w-48" aria-label="Mailbox"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="*">All inboxes</SelectItem>
            <SelectItem v-for="a in accounts.accounts" :key="a.id" :value="a.id">{{ a.displayName || a.email }}</SelectItem>
          </SelectContent>
        </Select>
        <SegmentedControl
          v-model="grouping"
          aria-label="Group by"
          :options="[{ value: 'address', label: 'Address' }, { value: 'domain', label: 'Domain' }]"
        />
      </div>

      <div
        ref="listEl"
        role="listbox"
        tabindex="0"
        aria-label="Senders"
        :aria-activedescendant="current ? `cleanup-row-${active}` : undefined"
        class="min-h-0 flex-1 overflow-y-auto border-t outline-none"
        @keydown="onListKey"
      >
        <div v-if="status === 'loading' && !list.length" class="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
          <LoaderIcon class="size-4 animate-spin" /> Counting senders…
        </div>
        <div v-else-if="status === 'error'" class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-sm">
          <p>Couldn’t count the senders. {{ error }}</p>
          <Button variant="outline" size="sm" @click="load">Try again</Button>
        </div>
        <div v-else-if="!rows.length" class="text-muted-foreground flex h-full flex-col items-center justify-center gap-1 px-6 text-center text-sm">
          <CheckIcon class="mb-1 size-5" />
          <p v-if="filter.trim()">No sender matches “{{ filter.trim() }}”.</p>
          <p v-else>Nothing left to clean up here.</p>
        </div>
        <ul v-else class="py-1">
          <li
            v-for="(s, i) in rows"
            :id="`cleanup-row-${i}`"
            :key="senderRowKey(s)"
            :data-index="i"
            role="option"
            :aria-selected="i === active"
            :class="['group flex cursor-default items-center gap-3 px-5 py-2.5', i === active ? 'bg-accent/70' : 'hover:bg-muted/50']"
            @click="pick(i)"
            @dblclick="showMail(s)"
          >
            <SenderAvatar :address="{ name: s.key.startsWith('@') ? s.domain : s.name, email: s.address }" :size="32" />
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center gap-2">
                <span class="truncate font-medium" data-sender-title>{{ senderTitle(s) }}</span>
                <span v-if="rulesOf(s).junk.length" class="text-destructive bg-destructive/10 inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-px text-[11px]">
                  <ShieldBanIcon class="size-3" /> Always junked
                </span>
                <span
                  v-for="r in rulesOf(s).label"
                  :key="r.id"
                  class="bg-muted text-muted-foreground inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-px text-[11px]"
                >
                  <span class="size-2 rounded-full" :style="{ background: settings.labelMap.get(r.labelId)?.color }" />
                  {{ settings.labelMap.get(r.labelId)?.name ?? 'Label' }}
                </span>
              </div>
              <div class="text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs">
                <span class="truncate">{{ s.key.startsWith('@') ? (s.addresses > 1 ? `${s.addresses} addresses` : s.address) : s.address }}</span>
                <template v-if="!cleanup.accountId && multi"><span aria-hidden="true">·</span><span class="truncate">{{ mailboxName(s.accountId) }}</span></template>
                <Tip v-if="s.unsubscribe" label="A mailing list: it offers unsubscribe">
                  <MailMinusIcon class="size-3 shrink-0" aria-label="Mailing list" />
                </Tip>
              </div>
              <div class="text-muted-foreground mt-0.5 text-xs tabular-nums">{{ senderStats(s) }}</div>
            </div>

            <div :class="['flex shrink-0 items-center gap-0.5', i === active ? '' : 'opacity-0 group-hover:opacity-100 focus-within:opacity-100']">
              <Tip label="Junk all, and new mail too" shortcut="j">
                <Button variant="ghost" size="xs" class="hover:text-destructive" @click.stop="junk(s)"><ShieldBanIcon /> Junk all</Button>
              </Tip>
              <DropdownMenu :open="labelMenu === senderRowKey(s)" @update:open="(o) => (labelMenu = o ? senderRowKey(s) : '')">
                <Tip label="Always label" shortcut="l">
                  <DropdownMenuTrigger as-child>
                    <Button variant="ghost" size="xs" @click.stop><TagIcon /> Label</Button>
                  </DropdownMenuTrigger>
                </Tip>
                <DropdownMenuContent align="end" class="w-56" @close-auto-focus.prevent="focusList">
                  <DropdownMenuLabel class="text-muted-foreground truncate text-xs font-normal">Always label {{ ruleTarget(s.key) }} as</DropdownMenuLabel>
                  <DropdownMenuCheckboxItem
                    v-for="l in labelsOf(s)"
                    :key="l.id"
                    :model-value="rulesOf(s).label.some((r) => r.labelId === l.id)"
                    @select="label(s, l.id)"
                  >
                    <span class="size-2.5 shrink-0 rounded-full" :style="{ background: l.color }" /> <span class="truncate">{{ l.name }}</span>
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuItem v-if="!labelsOf(s).length" disabled>No labels in this mailbox</DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem @select="ui.openSettings(`mailbox:${s.accountId}`, 'labels')"><ListFilterIcon /> Edit rules…</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              <Tip label="Archive what’s in the inbox" shortcut="a">
                <Button variant="ghost" size="xs" @click.stop="archive(s)"><ArchiveIcon /> Archive all</Button>
              </Tip>
              <Tip label="Keep: hide this sender for now" shortcut="k">
                <Button variant="ghost" size="xs" @click.stop="keep(s)"><CheckIcon /> Keep</Button>
              </Tip>
            </div>
          </li>
        </ul>
      </div>

      <div class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-t px-5 py-2.5 text-xs">
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="up" /><KeyCaps combo="down" /> move</span>
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="j" /> junk</span>
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="l" /> label</span>
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="a" /> archive</span>
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="k" /> keep</span>
        <span class="inline-flex items-center gap-1.5"><KeyCaps combo="enter" /> show mail</span>
        <span class="ml-auto tabular-nums">
          {{ rows.length.toLocaleString() }} {{ rows.length === 1 ? 'sender' : 'senders' }}
          <template v-if="keptCount"> · {{ keptCount }} kept <button type="button" class="hover:text-foreground underline underline-offset-2" @click="showKept">Show</button></template>
        </span>
      </div>
    </DialogContent>
  </Dialog>
</template>
