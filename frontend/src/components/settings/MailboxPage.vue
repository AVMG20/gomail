<script setup lang="ts">
// Settings → Mailboxes → one mailbox: everything that applies to this mailbox only, in one place.
// App-wide settings stay on the App pages; the overviews there (Notifications, AI, Calendar)
// edit the same per-mailbox values as this page.
import { computed, defineAsyncComponent, nextTick, ref, watch } from 'vue'
import { ChevronRightIcon, KeyRoundIcon, LoaderIcon, OctagonAlertIcon, RefreshCwIcon, SparklesIcon, Trash2Icon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { ACCOUNT_COLORS } from '@/lib/color'
import { relativeTime } from '@/lib/format'
import type { Account, LabelRule } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useCalendarStore } from '@/stores/calendar'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import { useMailboxSettings } from './mailboxSettings'
import QuietLabelChips from './QuietLabelChips.vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const props = defineProps<{ accountId: string }>()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const mail = useMailStore()
const ui = useUiStore()
const ai = useAiStore()
const cal = useCalendarStore()
const mb = useMailboxSettings()
const a = computed(() => accounts.accountMap.get(props.accountId) ?? null)
const s = computed(() => settings.settings!)
const hasCalendar = hasMethod('ListCalendars')
const providerName = (acc: Account) => (acc.provider === 'gmail' ? 'Gmail' : 'Outlook')

// ---- name, color, signature
async function save(acc: Account, patch: Partial<Pick<Account, 'displayName' | 'color'>>) {
  const name = patch.displayName ?? acc.displayName
  const color = patch.color ?? acc.color
  if (name === acc.displayName && color === acc.color) return
  const prev = { ...acc }
  Object.assign(acc, { displayName: name, color })
  try {
    await api.updateAccount(acc.id, name, color)
  } catch (e) {
    Object.assign(acc, prev)
    toast.error('Could not update account', { description: errorMessage(e) })
  }
}
const editingSig = ref(false)
const SignatureEditor = defineAsyncComponent(() => import('./SignatureEditor.vue'))
let sigTimer: ReturnType<typeof setTimeout> | null = null
function saveSig(id: string, html: string) {
  if (sigTimer) clearTimeout(sigTimer)
  sigTimer = setTimeout(() => {
    accounts.saveSignature(id, html).catch((e) => toast.error('Could not save signature', { description: errorMessage(e) }))
  }, 700)
}

// ---- sign-in
const reconnecting = ref(false)
async function reconnect() {
  if (!a.value) return
  reconnecting.value = true
  try {
    await api.reauthAccount(a.value.id)
    await accounts.load()
    toast.success('Reconnected', { description: 'Syncing resumes in the background.' })
  } catch (e) {
    const msg = errorMessage(e)
    if (!/cancel/i.test(msg)) toast.error('Couldn’t reconnect', { description: msg })
  } finally {
    reconnecting.value = false
  }
}

// ---- remove
const confirmRemove = ref(false)
// Kept apart from the dialog's open state: the dialog's own close runs before the confirm click.
let removeTarget: Account | null = null
function askRemove() {
  removeTarget = a.value
  confirmRemove.value = true
}
async function remove() {
  const acc = removeTarget
  removeTarget = null
  if (!acc) return
  try {
    await api.removeAccount(acc.id)
    await accounts.load()
    mail.ensureValidMailbox()
    ui.settingsTab = 'accounts'
    toast(`Removed ${acc.email}`)
  } catch (e) {
    toast.error('Could not remove account', { description: errorMessage(e) })
  }
}

// Rail menu "Rename… / Change color… / Remove account…" land here.
watch(() => ui.focusAccount, async (f) => {
  if (!f || f.id !== props.accountId) return
  ui.focusAccount = null
  await nextTick()
  if (f.field === 'remove') return askRemove()
  const el = document.querySelector<HTMLElement>(`[data-account-${f.field}="${f.id}"]`)
  el?.scrollIntoView({ block: 'nearest' })
  if (f.field === 'name') {
    ;(el as HTMLInputElement | null)?.focus()
    ;(el as HTMLInputElement | null)?.select()
  } else el?.click()
}, { immediate: true })

// ---- labels and rules of this mailbox
const labels = computed(() => settings.labelsForAccount(props.accountId))
const rules = computed(() => (s.value.labelRules ?? []).filter((r) => !r.accountId || r.accountId === props.accountId))
function ruleText(r: LabelRule): string {
  const what = r.field === 'from' ? 'Sender' : 'Subject'
  const then = r.action === 'junk' ? 'Move to Junk' : `Add “${settings.labelMap.get(r.labelId)?.name ?? 'a label'}”`
  return `${what} contains “${r.contains.trim() || '…'}” → ${then}`
}

// ---- AI
const noKey = computed(() => !s.value.openRouterKeySet)
function labelAll() {
  if (!a.value) return
  ai.requestBulk({ query: { accountId: a.value.id }, ids: [], title: `All mail in ${a.value.email}` })
}
</script>

<template>
  <div v-if="a && settings.settings">
    <!-- Who this page is about -->
    <div class="mb-6 flex items-center gap-3">
      <AccountAvatar :account="a" :size="40" />
      <div class="min-w-0 flex-1">
        <div class="truncate text-sm font-semibold">{{ a.displayName || a.email }}</div>
        <div class="text-muted-foreground truncate text-xs">
          {{ a.email }} · {{ providerName(a) }} ·
          <span :class="a.syncError && 'text-destructive'">{{ a.syncError ? a.syncError : `synced ${relativeTime(a.lastSyncAt)}` }}</span>
        </div>
      </div>
      <Button variant="outline" size="sm" :aria-label="`Sync ${a.email} now`" @click="accounts.sync(a.id)"><RefreshCwIcon :class="accounts.syncing.has(a.id) && 'animate-spin'" /> Sync now</Button>
    </div>

    <div v-if="a.needsReauth" class="mb-6 flex items-center gap-3 rounded-lg bg-amber-500/10 px-3 py-2.5 text-xs" role="alert">
      <KeyRoundIcon class="size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
      <span class="min-w-0 flex-1"><span class="font-medium">Sign-in expired.</span> <span class="text-muted-foreground">Reconnect to keep this mailbox in sync.</span></span>
      <Button v-if="hasMethod('ReauthAccount')" size="xs" :disabled="reconnecting" @click="reconnect"><LoaderIcon v-if="reconnecting" class="animate-spin" /> Reconnect</Button>
    </div>

    <SettingSection title="Account">
      <SettingRow title="Name" description="Shown in the sidebar and as the sender name.">
        <Input :model-value="a.displayName" class="h-8 w-56 text-[13px]" :data-account-name="a.id" @change="(e: Event) => save(a!, { displayName: (e.target as HTMLInputElement).value.trim() || a!.email })" />
      </SettingRow>
      <SettingRow title="Color" description="Marks this mailbox in lists and the calendar.">
        <Popover>
          <PopoverTrigger as-child>
            <button type="button" class="border-border focus-visible:ring-ring flex size-8 items-center justify-center rounded-md border outline-none focus-visible:ring-2" :aria-label="`Color for ${a.email}`" :data-account-color="a.id">
              <span class="size-4 rounded-full" :style="{ background: a.color }" />
            </button>
          </PopoverTrigger>
          <PopoverContent class="w-auto p-2" align="end">
            <div class="grid grid-cols-4 gap-1.5">
              <button v-for="c in ACCOUNT_COLORS" :key="c" type="button" class="size-7 rounded-full" :style="{ background: c, boxShadow: a.color === c ? `0 0 0 2px var(--popover), 0 0 0 4px ${c}` : undefined }" :aria-label="`Color ${c}`" :aria-pressed="a.color === c" @click="save(a!, { color: c })" />
            </div>
          </PopoverContent>
        </Popover>
      </SettingRow>
      <SettingRow title="Signature" :description="a.signature ? 'Added below new messages and replies from this mailbox.' : 'No signature yet.'" :stacked="editingSig">
        <Button v-if="!editingSig" size="sm" variant="outline" @click="editingSig = true">{{ a.signature ? 'Edit' : 'Add signature' }}</Button>
        <template v-else>
          <SignatureEditor autofocus :model-value="a.signature ?? ''" @update:model-value="(v: string) => saveSig(a!.id, v)" />
          <Button size="sm" variant="ghost" class="mt-2" @click="editingSig = false">Done</Button>
        </template>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Notifications">
      <SettingRow title="New mail notifications" :description="s.notifications ? 'Show a notification when mail arrives here.' : 'Desktop notifications are off for all mailboxes.'">
        <Switch :model-value="s.notifications && mb.notifies(a.id)" :disabled="!s.notifications" @update:model-value="(v: boolean) => mb.setNotify(a!.id, v)" />
      </SettingRow>
      <SettingRow v-if="s.notifications && mb.notifies(a.id)" title="Arrive quietly" description="New mail with these labels doesn’t notify." stacked>
        <QuietLabelChips :account="a" />
      </SettingRow>
      <SettingRow v-if="!s.notifications" title="Turn on desktop notifications">
        <Button size="sm" variant="outline" @click="ui.settingsTab = 'notifications'">Notification settings</Button>
      </SettingRow>
    </SettingSection>

    <SettingSection title="AI labeling">
      <SettingRow title="Auto-label new mail with Jev" :description="noKey ? 'Needs an OpenRouter key (AI settings).' : 'Sorts new mail into this mailbox’s labels after each sync.'">
        <Switch :model-value="!noKey && mb.autoLabels(a.id)" :disabled="noKey" @update:model-value="(v: boolean) => mb.setAutoLabel(a!.id, v)" />
      </SettingRow>
      <SettingRow title="Let AI correct Gmail/Outlook labels" :description="s.aiCorrectsProviderLabels ? 'Removes imported labels it’s sure don’t apply.' : 'Off for all mailboxes in AI settings.'">
        <Switch :model-value="s.aiCorrectsProviderLabels && mb.corrects(a.id)" :disabled="!s.aiCorrectsProviderLabels" @update:model-value="(v: boolean) => mb.setCorrects(a!.id, v)" />
      </SettingRow>
      <SettingRow title="Label existing mail" description="Run Jev once on mail already downloaded here.">
        <div class="flex items-center gap-2">
          <Button v-if="noKey" size="sm" variant="ghost" @click="ui.settingsTab = 'ai'">AI settings</Button>
          <Button size="sm" variant="outline" :disabled="noKey || (!!ai.job && !ai.job.finished)" @click="labelAll"><SparklesIcon class="text-violet-500" /> Label all mail…</Button>
        </div>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Labels" :description="labels.length ? 'The labels used in this mailbox.' : undefined">
      <template #actions>
        <Button size="xs" variant="ghost" class="text-muted-foreground ml-auto" @click="ui.settingsTab = 'labels'">Manage <ChevronRightIcon /></Button>
      </template>
      <div v-if="labels.length" class="flex flex-wrap gap-1.5 py-3.5">
        <span v-for="l in labels" :key="l.id" class="bg-muted inline-flex h-6 items-center gap-1.5 rounded-full px-2 text-xs">
          <span class="size-2 rounded-full" :style="{ background: l.color }" aria-hidden="true" /> {{ l.name }}
        </span>
      </div>
      <p v-else class="text-muted-foreground py-3.5 text-xs">No labels in this mailbox yet.</p>
    </SettingSection>

    <SettingSection title="Rules" :description="rules.length ? 'Rules that run on new mail here.' : undefined">
      <template #actions>
        <Button size="xs" variant="ghost" class="text-muted-foreground ml-auto" @click="ui.settingsTab = 'labels'">Edit rules <ChevronRightIcon /></Button>
      </template>
      <ul v-if="rules.length" class="divide-border divide-y" aria-label="Rules for this mailbox">
        <li v-for="r in rules" :key="r.id" class="flex items-center gap-2 py-2.5 text-[13px]">
          <OctagonAlertIcon v-if="r.action === 'junk'" class="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate">{{ ruleText(r) }}</span>
          <span class="text-muted-foreground shrink-0 text-xs">{{ r.accountId ? 'This mailbox' : 'All mailboxes' }}</span>
        </li>
      </ul>
      <p v-else class="text-muted-foreground py-3.5 text-xs">No rules for this mailbox.</p>
    </SettingSection>

    <SettingSection v-if="hasCalendar" title="Calendar">
      <SettingRow v-if="a.calendarConsent === false" title="Calendar" description="This mailbox’s calendar isn’t connected yet.">
        <Button variant="outline" size="sm" :disabled="cal.connecting === a.id" @click="cal.connect(a.id)">Connect calendar</Button>
      </SettingRow>
      <template v-else>
        <SettingRow title="Show this calendar" description="When off, its events are hidden and it doesn’t sync.">
          <Switch :model-value="a.calendarEnabled !== false" @update:model-value="(v: boolean) => cal.setAccountEnabled(a!.id, v, false)" />
        </SettingRow>
        <SettingRow title="Event reminders" :description="s.calendarNotifications === false ? 'Event reminders are off for all mailboxes.' : 'Notify before this calendar’s events start.'">
          <Switch :model-value="!a.calendarMuted && s.calendarNotifications !== false" :disabled="a.calendarEnabled === false || s.calendarNotifications === false" @update:model-value="(v: boolean) => cal.setAccountMuted(a!.id, !v)" />
        </SettingRow>
      </template>
    </SettingSection>

    <SettingSection title="Sign-in">
      <SettingRow v-if="hasMethod('ReauthAccount')" title="Sign in again" description="If syncing stopped or you changed your password.">
        <Button variant="outline" size="sm" :disabled="reconnecting" @click="reconnect"><LoaderIcon v-if="reconnecting" class="animate-spin" /><KeyRoundIcon v-else /> Reconnect…</Button>
      </SettingRow>
      <SettingRow title="Remove this mailbox" description="Deletes its local mail and sign-in here. Nothing is deleted on the server.">
        <Button variant="outline" size="sm" class="text-destructive hover:text-destructive" @click="askRemove"><Trash2Icon /> Remove…</Button>
      </SettingRow>
    </SettingSection>

    <AlertDialog v-model:open="confirmRemove">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {{ a.email }}?</AlertDialogTitle>
          <AlertDialogDescription>Local copies of its mail and its sign-in token are deleted from this computer. Nothing is deleted from the server.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="remove">Remove account</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
  <p v-else class="text-muted-foreground py-6 text-sm">This mailbox was removed.</p>
</template>
