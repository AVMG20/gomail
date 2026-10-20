<script setup lang="ts">
// Settings → Mailboxes → All mailboxes: the list of connected mailboxes (each opens its own
// page with everything about it) and the OAuth apps used to sign in.
import { ChevronRightIcon, PlusIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { relativeTime } from '@/lib/format'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'

const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Your mailboxes" description="Open one for its name, signature, notifications, AI labeling, labels and calendar.">
      <button
        v-for="a in accounts.accounts"
        :key="a.id"
        type="button"
        class="hover:bg-muted/60 focus-visible:ring-ring -mx-2 flex w-[calc(100%+1rem)] items-center gap-3 rounded-md px-2 py-3 text-left outline-none focus-visible:ring-2"
        @click="ui.settingsTab = `mailbox:${a.id}`"
      >
        <AccountAvatar :account="a" :size="36" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[13px] font-medium">{{ a.displayName || a.email }}</span>
          <span class="text-muted-foreground block truncate text-xs">
            {{ a.email }} · {{ a.provider === 'gmail' ? 'Gmail' : 'Outlook' }} ·
            <span :class="(a.syncError || a.needsReauth) && 'text-destructive'">{{ a.needsReauth ? 'sign-in expired' : a.syncError ? a.syncError : `synced ${relativeTime(a.lastSyncAt)}` }}</span>
          </span>
        </span>
        <ChevronRightIcon class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
      </button>
      <p v-if="!accounts.accounts.length" class="text-muted-foreground py-3.5 text-xs">No mailboxes yet. Add a Gmail or Outlook account to get started.</p>
      <div class="py-3">
        <Button variant="outline" size="sm" @click="ui.addAccountOpen = true"><PlusIcon /> Add account</Button>
      </div>
    </SettingSection>

    <SettingSection title="Sign-in apps (OAuth)" description="Needed before you add a mailbox. Your own app keeps your sign-ins away from third parties.">
      <SettingRow title="Google client ID" stacked>
        <template #description>In Google Cloud Console → APIs &amp; Services → Credentials, create a “Desktop app” OAuth client, with the Gmail API turned on.</template>
        <Input :model-value="settings.settings.googleClientId" placeholder="1234567890-abc.apps.googleusercontent.com" class="font-mono text-xs" @update:model-value="(v) => settings.update({ googleClientId: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Google client secret" description="Optional. Paste it if Google shows one for your desktop client." stacked>
        <Input type="password" :model-value="settings.settings.googleClientSecret" placeholder="Optional" class="font-mono text-xs" @update:model-value="(v) => settings.update({ googleClientSecret: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Microsoft client ID" stacked>
        <template #description>In Microsoft Entra ID → App registrations, register a public client with redirect <code class="bg-muted rounded px-1">http://localhost</code> and the Mail.ReadWrite and Mail.Send permissions.</template>
        <Input :model-value="settings.settings.microsoftClientId" placeholder="00000000-0000-0000-0000-000000000000" class="font-mono text-xs" @update:model-value="(v) => settings.update({ microsoftClientId: String(v).trim() }, 600)" />
      </SettingRow>
      <SettingRow title="Microsoft tenant" description="“common” works for personal and work accounts. Or enter your tenant ID." stacked>
        <Input :model-value="settings.settings.microsoftTenant" placeholder="common" class="max-w-64 font-mono text-xs" @update:model-value="(v) => settings.update({ microsoftTenant: String(v).trim() || 'common' }, 600)" />
      </SettingRow>
    </SettingSection>
  </div>
</template>
