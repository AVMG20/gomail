<script setup lang="ts">
// Account + calendar picker: only editable calendars of enabled mailboxes,
// grouped by mailbox with color dots.
import { computed } from 'vue'
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'

const model = defineModel<string>({ required: true })
defineProps<{ compact?: boolean }>()
const cal = useCalendarStore()
const accounts = useAccountsStore()

const groups = computed(() =>
  accounts.accounts
    .map((a) => ({ account: a, calendars: cal.writableCalendars.filter((c) => c.accountId === a.id) }))
    .filter((g) => g.calendars.length),
)
const current = computed(() => cal.calendarMap.get(model.value))
const currentAccount = computed(() => (current.value ? accounts.accountMap.get(current.value.accountId) : undefined))
</script>

<template>
  <Select v-model="model">
    <SelectTrigger :class="['w-full', compact ? 'h-8 text-xs' : '']" aria-label="Calendar">
      <SelectValue placeholder="Choose a calendar">
        <span v-if="current" class="flex min-w-0 items-center gap-2">
          <span class="size-2.5 shrink-0 rounded-full" :style="{ background: current.color }" />
          <span class="truncate">{{ current.name }}</span>
          <span v-if="!compact" class="text-muted-foreground truncate text-xs">{{ currentAccount?.email }}</span>
        </span>
      </SelectValue>
    </SelectTrigger>
    <!-- popper: opens below (or above) the field instead of over it, also near the window's edge. -->
    <SelectContent position="popper">
      <SelectGroup v-for="g in groups" :key="g.account.id">
        <SelectLabel class="text-muted-foreground text-[11px]">{{ g.account.email }}</SelectLabel>
        <SelectItem v-for="c in g.calendars" :key="c.id" :value="c.id">
          <span class="flex items-center gap-2">
            <span class="size-2.5 shrink-0 rounded-full" :style="{ background: c.color }" />
            {{ c.name }}
            <span v-if="!c.visible" class="text-muted-foreground text-[11px]">hidden</span>
          </span>
        </SelectItem>
      </SelectGroup>
    </SelectContent>
  </Select>
</template>
