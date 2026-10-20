<script setup lang="ts">
// Settings → Notifications, every mailbox at a glance:
//  - a switch that turns new-mail notifications off for that mailbox (calendar reminders are separate);
//  - the labels whose new mail arrives quietly (QuietLabelChips).
// A mailbox without its own list uses the default (Junk only), like the backend.
// Each mailbox's own settings page shows the same two settings.
import { Switch } from '@/components/ui/switch'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useMailboxSettings } from './mailboxSettings'
import QuietLabelChips from './QuietLabelChips.vue'
import SettingRow from './SettingRow.vue'

const accounts = useAccountsStore()
const mb = useMailboxSettings()
</script>

<template>
  <SettingRow title="Per mailbox" description="Turn a mailbox off, or let mail with some labels arrive quietly. Junk never notifies." stacked>
    <ul class="space-y-1.5" aria-label="Notifications per mailbox">
      <li v-for="a in accounts.accounts" :key="a.id" class="flex items-start gap-3">
        <Switch
          class="mt-0.5 shrink-0"
          :model-value="mb.notifies(a.id)"
          :aria-label="`Notifications for ${a.email}`"
          @update:model-value="(v: boolean) => mb.setNotify(a.id, v)"
        />
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          <span class="flex w-52 min-w-0 items-center gap-2 text-[13px]" :class="!mb.notifies(a.id) && 'text-muted-foreground'">
            <AccountAvatar :account="a" :size="18" :glyph="false" />
            <span class="truncate" :title="a.email">{{ a.email }}</span>
          </span>
          <span v-if="!mb.notifies(a.id)" class="text-muted-foreground text-xs">Notifications off</span>
          <QuietLabelChips v-else :account="a" />
        </div>
      </li>
    </ul>
  </SettingRow>
</template>
