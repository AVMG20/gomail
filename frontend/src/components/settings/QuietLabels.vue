<script setup lang="ts">
// Settings → Notifications, every mailbox at a glance: the labels whose new mail arrives
// quietly (QuietLabelChips), or "Muted" for a muted mailbox (it never notifies; muting is
// on the mailbox's own page and in its menu). A mailbox without its own list uses the
// default (Junk only), like the backend. Each mailbox's own settings page shows the same.
import { BellOffIcon } from '@lucide/vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useMailboxSettings } from './mailboxSettings'
import QuietLabelChips from './QuietLabelChips.vue'
import SettingRow from './SettingRow.vue'

const accounts = useAccountsStore()
const mb = useMailboxSettings()
</script>

<template>
  <SettingRow title="Per mailbox" description="Let mail with certain labels arrive without a notification. Junk never notifies, and neither does a muted mailbox." stacked>
    <ul class="space-y-1.5" aria-label="Notifications per mailbox">
      <li v-for="a in accounts.accounts" :key="a.id" class="flex min-w-0 flex-wrap items-center gap-1.5">
        <span class="flex w-52 min-w-0 items-center gap-2 text-[13px]" :class="mb.isMuted(a.id) && 'text-muted-foreground'">
          <AccountAvatar :account="a" :size="18" :glyph="false" />
          <span class="truncate" :title="a.email">{{ a.email }}</span>
        </span>
        <span v-if="mb.isMuted(a.id)" class="text-muted-foreground inline-flex items-center gap-1 text-xs"><BellOffIcon class="size-3.5" aria-hidden="true" /> Muted</span>
        <QuietLabelChips v-else :account="a" />
      </li>
    </ul>
  </SettingRow>
</template>
