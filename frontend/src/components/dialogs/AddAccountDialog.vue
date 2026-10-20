<script setup lang="ts">
import { ref, watch } from 'vue'
import { ExternalLinkIcon, LoaderIcon, SettingsIcon, ShieldCheckIcon, TriangleAlertIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { api, errorMessage } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const accounts = useAccountsStore()
const mail = useMailStore()

const SETUP_GUIDE = 'https://github.com/AVMG20/gomail/blob/main/docs/BACKEND.md'
const pending = ref<'' | 'gmail' | 'outlook'>('')
const failedProvider = ref<'gmail' | 'outlook'>('gmail')
const error = ref('')
const needsConfig = ref(false)

watch(() => ui.addAccountOpen, (open) => {
  if (open) {
    error.value = ''
    needsConfig.value = false
  } else if (pending.value) void api.cancelAddAccount()
})

async function connect(provider: 'gmail' | 'outlook') {
  pending.value = provider
  error.value = ''
  needsConfig.value = false
  failedProvider.value = provider
  try {
    const acc = await api.addAccount(provider)
    await accounts.load()
    mail.switchAccount(acc.id)
    ui.addAccountOpen = false
    toast.success(`Connected ${acc.email}`, { description: 'Your mail is syncing in the background.' })
  } catch (e) {
    const msg = errorMessage(e)
    if (/cancel/i.test(msg)) return
    error.value = msg
    needsConfig.value = /one-time setup|client.?id|not configured|oauth/i.test(msg)
  } finally {
    pending.value = ''
  }
}

async function cancel() {
  await api.cancelAddAccount()
  pending.value = ''
}

function openOAuthSettings() {
  ui.addAccountOpen = false
  ui.openSettings('accounts')
}
</script>

<template>
  <Dialog v-model:open="ui.addAccountOpen">
    <DialogContent class="sm:max-w-[420px]" @interact-outside="(e: Event) => pending && e.preventDefault()">
      <DialogHeader>
        <DialogTitle>Add an account</DialogTitle>
        <DialogDescription>Sign in with your provider in the browser. gomail never sees your password.</DialogDescription>
      </DialogHeader>

      <div v-if="pending" class="flex flex-col items-center gap-3 py-6 text-center">
        <div class="relative">
          <ProviderIcon :provider="pending" class="size-10" />
          <LoaderIcon class="text-muted-foreground absolute -right-3 -bottom-2 size-5 animate-spin" />
        </div>
        <div>
          <p class="text-sm font-medium">Waiting for you to finish signing in…</p>
          <p class="text-muted-foreground mt-1 text-xs">Complete the {{ pending === 'gmail' ? 'Google' : 'Microsoft' }} sign-in in your browser, then come back here.</p>
        </div>
        <Button variant="outline" size="sm" class="mt-2" @click="cancel">Cancel</Button>
      </div>

      <div v-else class="grid gap-2.5">
        <button type="button" class="border-border hover:bg-muted/60 flex items-center gap-3 rounded-lg border px-4 py-3 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring" @click="connect('gmail')">
          <ProviderIcon provider="gmail" class="size-7" />
          <span class="flex-1"><span class="block text-sm font-medium">Connect Gmail</span><span class="text-muted-foreground text-xs">Google Workspace or personal Gmail</span></span>
        </button>
        <button type="button" class="border-border hover:bg-muted/60 flex items-center gap-3 rounded-lg border px-4 py-3 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring" @click="connect('outlook')">
          <ProviderIcon provider="outlook" class="size-7" />
          <span class="flex-1"><span class="block text-sm font-medium">Connect Outlook</span><span class="text-muted-foreground text-xs">Microsoft 365, Outlook.com, Hotmail</span></span>
        </button>

        <!-- No sign-in client yet: explain the one-time setup in plain words. -->
        <div v-if="error && needsConfig" class="bg-muted/60 mt-1 rounded-lg px-3.5 py-3 text-xs leading-relaxed">
          <p class="text-foreground text-[13px] font-medium">Signing in with {{ failedProvider === 'gmail' ? 'Google' : 'Microsoft' }} needs a one-time setup</p>
          <ol class="text-muted-foreground mt-1.5 list-decimal space-y-0.5 pl-4">
            <li>Create a free sign-in client in the {{ failedProvider === 'gmail' ? 'Google Cloud Console' : 'Microsoft Entra admin center' }} (about 5 minutes).</li>
            <li>Paste its client ID in Settings → Mailboxes.</li>
            <li>Come back here and connect.</li>
          </ol>
          <p class="text-muted-foreground mt-1.5">Official gomail release builds come with this already set up.</p>
          <div class="mt-2.5 flex flex-wrap gap-2">
            <Button size="xs" @click="api.openExternal(SETUP_GUIDE)"><ExternalLinkIcon /> Open setup guide</Button>
            <Button variant="outline" size="xs" @click="openOAuthSettings"><SettingsIcon /> Settings → Mailboxes</Button>
          </div>
        </div>
        <div v-else-if="error" class="bg-destructive/5 border-destructive/20 mt-1 rounded-lg border px-3 py-2.5 text-xs">
          <p class="text-destructive flex items-start gap-2 font-medium"><TriangleAlertIcon class="mt-px size-3.5 shrink-0" /> {{ error }}</p>
        </div>
        <p class="text-muted-foreground mt-1 flex items-center gap-1.5 text-[11px]"><ShieldCheckIcon class="size-3.5" /> Tokens are stored in your system keychain.</p>
      </div>
    </DialogContent>
  </Dialog>
</template>
