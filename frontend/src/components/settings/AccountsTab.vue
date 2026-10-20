<script setup lang="ts">
import { defineAsyncComponent, nextTick, ref, watch } from 'vue'
import { PlusIcon, RefreshCwIcon, Trash2Icon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { api, errorMessage } from '@/lib/api'
import { ACCOUNT_COLORS } from '@/lib/color'
import { relativeTime } from '@/lib/format'
import type { Account } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const accounts = useAccountsStore()
const settings = useSettingsStore()
const mail = useMailStore()
const ui = useUiStore()
const removing = ref<Account | null>(null)
// The account being removed, kept apart from the dialog's open state: the dialog's own
// close (which clears `removing`) runs before the confirm button's click handler.
let removeTarget: Account | null = null
function askRemove(a: Account) {
  removeTarget = a
  removing.value = a
}

// Rail menu "Rename… / Change color… / Remove account…" land on that account.
watch(() => ui.focusAccount, async (f) => {
  if (!f) return
  ui.focusAccount = null
  await nextTick()
  const a = accounts.accountMap.get(f.id)
  if (!a) return
  if (f.field === 'remove') return askRemove(a)
  const el = document.querySelector<HTMLElement>(`[data-account-${f.field}="${f.id}"]`)
  el?.scrollIntoView({ block: 'nearest' })
  if (f.field === 'name') {
    ;(el as HTMLInputElement | null)?.focus()
    ;(el as HTMLInputElement | null)?.select()
  } else el?.click()
}, { immediate: true })
const editingSig = ref<string | null>(null)
const SignatureEditor = defineAsyncComponent(() => import('./SignatureEditor.vue'))
let sigTimer: ReturnType<typeof setTimeout> | null = null
function saveSig(id: string, html: string) {
  if (sigTimer) clearTimeout(sigTimer)
  sigTimer = setTimeout(() => {
    accounts.saveSignature(id, html).catch((e) => toast.error('Could not save signature', { description: errorMessage(e) }))
  }, 700)
}

async function save(a: Account, patch: Partial<Pick<Account, 'displayName' | 'color'>>) {
  const name = patch.displayName ?? a.displayName
  const color = patch.color ?? a.color
  if (name === a.displayName && color === a.color) return
  const prev = { ...a }
  Object.assign(a, { displayName: name, color })
  try {
    await api.updateAccount(a.id, name, color)
  } catch (e) {
    Object.assign(a, prev)
    toast.error('Could not update account', { description: errorMessage(e) })
  }
}

async function remove() {
  const a = removeTarget
  removeTarget = null
  removing.value = null
  if (!a) return
  try {
    await api.removeAccount(a.id)
    await accounts.load()
    mail.ensureValidMailbox()
    toast(`Removed ${a.email}`)
  } catch (e) {
    toast.error('Could not remove account', { description: errorMessage(e) })
  }
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Accounts">
      <template v-for="a in accounts.accounts" :key="a.id">
      <div class="flex items-center gap-3 py-3">
        <AccountAvatar :account="a" :size="36" />
        <div class="min-w-0 flex-1">
          <Input :model-value="a.displayName" class="h-8 max-w-64 text-[13px] font-medium" aria-label="Account name" :data-account-name="a.id" @change="(e: Event) => save(a, { displayName: (e.target as HTMLInputElement).value.trim() || a.email })" />
          <div class="text-muted-foreground mt-1 truncate pl-0.5 text-xs">
            {{ a.email }} · {{ a.provider === 'gmail' ? 'Gmail' : 'Outlook' }} · <span :class="a.syncError && 'text-destructive'">{{ a.syncError ? a.syncError : `synced ${relativeTime(a.lastSyncAt)}` }}</span>
            · <button type="button" class="hover:text-foreground underline-offset-2 hover:underline" @click="editingSig = editingSig === a.id ? null : a.id">{{ a.signature ? 'Edit signature' : 'Add signature' }}</button>
          </div>
        </div>
        <Popover>
          <PopoverTrigger as-child>
            <button type="button" class="border-border flex size-8 items-center justify-center rounded-md border" aria-label="Account color" :data-account-color="a.id">
              <span class="size-4 rounded-full" :style="{ background: a.color }" />
            </button>
          </PopoverTrigger>
          <PopoverContent class="w-auto p-2" align="end">
            <div class="grid grid-cols-4 gap-1.5">
              <button v-for="c in ACCOUNT_COLORS" :key="c" type="button" class="size-7 rounded-full" :style="{ background: c, boxShadow: a.color === c ? `0 0 0 2px var(--popover), 0 0 0 4px ${c}` : undefined }" :aria-label="c" @click="save(a, { color: c })" />
            </div>
          </PopoverContent>
        </Popover>
        <Button variant="ghost" size="icon-sm" aria-label="Sync now" @click="accounts.sync(a.id)"><RefreshCwIcon :class="accounts.syncing.has(a.id) && 'animate-spin'" /></Button>
        <Button variant="ghost" size="icon-sm" class="hover:text-destructive" aria-label="Remove account" @click="askRemove(a)"><Trash2Icon /></Button>
      </div>
      <div v-if="editingSig === a.id" class="-mt-1 pb-3 pl-12">
        <SignatureEditor autofocus :model-value="a.signature ?? ''" @update:model-value="(v: string) => saveSig(a.id, v)" />
      </div>
      </template>
      <div class="py-3">
        <Button variant="outline" size="sm" @click="ui.addAccountOpen = true"><PlusIcon /> Add account</Button>
      </div>
    </SettingSection>

    <SettingSection title="OAuth credentials" description="gomail connects with your own OAuth app so your tokens never pass through a third party.">
      <SettingRow title="Google client ID" stacked>
        <template #description>Create a “Desktop app” OAuth client in Google Cloud Console → APIs &amp; Services → Credentials, with the Gmail API enabled.</template>
        <Input :model-value="settings.settings.googleClientId" placeholder="1234567890-abc.apps.googleusercontent.com" class="font-mono text-xs" @update:model-value="(v) => settings.update({ googleClientId: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Google client secret" description="Desktop clients get a non-confidential secret; paste it here if Google shows one." stacked>
        <Input type="password" :model-value="settings.settings.googleClientSecret" placeholder="Optional" class="font-mono text-xs" @update:model-value="(v) => settings.update({ googleClientSecret: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Microsoft client ID" stacked>
        <template #description>Register an app in Microsoft Entra ID → App registrations (public client, redirect <code class="bg-muted rounded px-1">http://localhost</code>), with Mail.ReadWrite and Mail.Send permissions.</template>
        <Input :model-value="settings.settings.microsoftClientId" placeholder="00000000-0000-0000-0000-000000000000" class="font-mono text-xs" @update:model-value="(v) => settings.update({ microsoftClientId: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Microsoft tenant" description="Use “common” for personal and work accounts, or your tenant ID." stacked>
        <Input :model-value="settings.settings.microsoftTenant" placeholder="common" class="max-w-64 font-mono text-xs" @update:model-value="(v) => settings.update({ microsoftTenant: String(v).trim() || 'common' }, 600)" />
      </SettingRow>
    </SettingSection>

    <AlertDialog :open="!!removing" @update:open="(v) => !v && (removing = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {{ removing?.email }}?</AlertDialogTitle>
          <AlertDialogDescription>Local copies of its mail and its sign-in token are deleted from this computer. Nothing is deleted from the server.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="remove">Remove account</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
