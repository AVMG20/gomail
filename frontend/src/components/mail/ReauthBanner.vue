<script setup lang="ts">
import { computed, ref } from 'vue'
import { KeyRoundIcon, LoaderIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'

const accounts = useAccountsStore()
const mail = useMailStore()
const pending = ref<string | null>(null)

// The account(s) in view whose sign-in expired.
const affected = computed(() => {
  const list = mail.mailbox.accountId ? accounts.accounts.filter((a) => a.id === mail.mailbox.accountId) : accounts.accounts
  return list.filter((a) => a.needsReauth)
})

async function reconnect(id: string) {
  pending.value = id
  try {
    await api.reauthAccount(id)
    await accounts.load()
    toast.success('Reconnected', { description: 'Syncing resumes in the background.' })
  } catch (e) {
    const msg = errorMessage(e)
    if (!/cancel/i.test(msg)) toast.error('Couldn’t reconnect', { description: msg })
  } finally {
    pending.value = null
  }
}

// Accounts recreated from a backup (ImportBackup) wait for their first sign-in.
const restored = (syncError: string | undefined) => !!syncError?.startsWith('Restored from a backup')

async function cancel() {
  await api.cancelAddAccount()
  pending.value = null
}
</script>

<template>
  <div v-for="a in affected" :key="a.id" class="border-border flex shrink-0 items-center gap-3 border-b bg-amber-500/10 px-4 py-2 text-xs" role="alert">
    <KeyRoundIcon class="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
    <span class="min-w-0 flex-1">
      <span class="font-medium">{{ restored(a.syncError) ? `Sign in to ${a.email} to finish restoring.` : `Sign-in expired for ${a.email}.` }}</span>{{ ' ' }}<span class="text-muted-foreground">{{ pending === a.id ? 'Finish signing in in your browser…' : restored(a.syncError) ? 'Your mail downloads again and gets its labels back.' : 'Reconnect to keep this mailbox in sync.' }}</span>
    </span>
    <template v-if="pending === a.id">
      <LoaderIcon class="text-muted-foreground size-4 animate-spin" />
      <Button variant="ghost" size="xs" @click="cancel">Cancel</Button>
    </template>
    <Button v-else-if="hasMethod('ReauthAccount')" size="xs" @click="reconnect(a.id)">{{ restored(a.syncError) ? 'Sign in' : 'Reconnect' }}</Button>
  </div>
</template>
