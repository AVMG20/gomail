<script setup lang="ts">
// Settings → General → Notifications, per mailbox:
//  - a switch that turns new-mail notifications off for that mailbox (calendar reminders are separate);
//  - the labels whose new mail arrives quietly. Junk never notifies, so it's shown as a fixed chip.
// A mailbox without its own list uses the default (Junk only), like the backend.
import { computed } from 'vue'
import { PlusIcon, XIcon } from '@lucide/vue'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'

const JUNK = 'junk'
const settings = useSettingsStore()
const accounts = useAccountsStore()
const quiet = computed(() => settings.settings?.quietLabels ?? {})
const muted = computed(() => new Set(settings.settings?.mutedMailboxes ?? []))
// Labels the user can make quiet (Junk always is).
const options = computed(() => settings.labels.filter((l) => l.id !== JUNK))
const junkLabel = computed(() => settings.labelMap.get(JUNK) ?? { id: JUNK, name: 'Junk', color: 'var(--muted-foreground)' })

/** The mailbox's quiet labels that still exist, without Junk (shown separately). */
function listFor(accountId: string): string[] {
  return (quiet.value[accountId] ?? []).filter((id) => id !== JUNK && settings.labelMap.has(id))
}
function toggle(accountId: string, labelId: string, on: boolean) {
  const cur = listFor(accountId)
  const next = on ? [...new Set([...cur, labelId])] : cur.filter((l) => l !== labelId)
  void settings.update({ quietLabels: { ...quiet.value, [accountId]: [JUNK, ...next] } }, 300)
}
function setNotify(accountId: string, on: boolean) {
  const next = new Set(muted.value)
  if (on) next.delete(accountId)
  else next.add(accountId)
  void settings.update({ mutedMailboxes: [...next] }, 0)
}
const label = (id: string) => settings.labelMap.get(id)
</script>

<template>
  <div class="py-3.5">
    <div class="text-[13px] font-medium">Per mailbox</div>
    <p class="text-muted-foreground mt-0.5 text-xs leading-relaxed">
      Turn notifications off for a mailbox, or let mail with certain labels arrive quietly. Junk never notifies. Calendar reminders aren’t affected.
    </p>
    <ul class="mt-2.5 space-y-1.5">
      <li v-for="a in accounts.accounts" :key="a.id" class="flex items-start gap-3">
        <Switch
          class="mt-0.5 shrink-0"
          :model-value="!muted.has(a.id)"
          :aria-label="`Notifications for ${a.email}`"
          @update:model-value="(v: boolean) => setNotify(a.id, v)"
        />
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          <span class="flex w-52 min-w-0 items-center gap-2 text-[13px]" :class="muted.has(a.id) && 'text-muted-foreground'">
            <AccountAvatar :account="a" :size="18" :glyph="false" />
            <span class="truncate" :title="a.email">{{ a.email }}</span>
          </span>
          <span v-if="muted.has(a.id)" class="text-muted-foreground text-xs">Notifications off</span>
          <template v-else>
            <span
              class="bg-muted text-muted-foreground inline-flex h-6 items-center gap-1.5 rounded-full px-2 text-xs"
              title="Junk never notifies"
            >
              <span class="size-2 rounded-full" :style="{ background: junkLabel.color }" aria-hidden="true" />
              {{ junkLabel.name }}
            </span>
            <span
              v-for="id in listFor(a.id)"
              :key="id"
              class="bg-muted inline-flex h-6 items-center gap-1.5 rounded-full pr-1 pl-2 text-xs"
            >
              <span class="size-2 rounded-full" :style="{ background: label(id)?.color }" aria-hidden="true" />
              {{ label(id)?.name }}
              <button
                type="button"
                class="hover:bg-background focus-visible:ring-ring flex size-4 items-center justify-center rounded-full focus-visible:ring-2 focus-visible:outline-none"
                :aria-label="`Notify again for ${label(id)?.name} in ${a.email}`"
                @click="toggle(a.id, id, false)"
              >
                <XIcon class="size-3" />
              </button>
            </span>
            <DropdownMenu v-if="options.length">
              <DropdownMenuTrigger as-child>
                <button
                  type="button"
                  class="text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring inline-flex h-6 items-center gap-1 rounded-full border border-dashed px-2 text-xs focus-visible:ring-2 focus-visible:outline-none"
                  :aria-label="`Add a quiet label for ${a.email}`"
                >
                  <PlusIcon class="size-3" aria-hidden="true" /> Label
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" class="max-h-72 w-52 overflow-y-auto">
                <DropdownMenuLabel class="text-muted-foreground text-xs">Arrive quietly</DropdownMenuLabel>
                <DropdownMenuCheckboxItem
                  v-for="l in options"
                  :key="l.id"
                  :model-value="listFor(a.id).includes(l.id)"
                  @select.prevent
                  @update:model-value="(v: boolean) => toggle(a.id, l.id, v)"
                >
                  <span class="size-2 rounded-full" :style="{ background: l.color }" aria-hidden="true" /> {{ l.name }}
                </DropdownMenuCheckboxItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </template>
        </div>
      </li>
    </ul>
  </div>
</template>
