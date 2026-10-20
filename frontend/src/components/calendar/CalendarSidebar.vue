<script setup lang="ts">
// Calendar sidebar: new event, mini month, and one row per mailbox with a
// switch (or "Connect" when calendar access hasn't been granted) and its calendars.
import { computed, ref } from 'vue'
import { ChevronRightIcon, LoaderIcon, PlusIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import MiniMonth from './MiniMonth.vue'

defineProps<{ selected: Date; rangeStart?: Date; rangeEnd?: Date }>()
const emit = defineEmits<{ pick: [Date]; create: [] }>()
const cal = useCalendarStore()
const accounts = useAccountsStore()
const expanded = ref<Set<string>>(new Set(accounts.accounts.map((a) => a.id)))

function toggle(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

const busyDays = computed(() => {
  const s = new Set<string>()
  for (const e of cal.visibleEvents) s.add(e.start.slice(0, 10))
  return s
})
</script>

<template>
  <aside class="bg-sidebar text-sidebar-foreground border-sidebar-border flex h-full w-full flex-col border-r" aria-label="Calendars">
    <div data-drag class="px-3 pt-[calc(var(--titlebar)+0.75rem)] pb-3">
      <Button class="w-full justify-start gap-2" @click="emit('create')"><PlusIcon /> New event <span class="ml-auto text-[11px] opacity-70">N</span></Button>
    </div>
    <div class="thin-scroll min-h-0 flex-1 space-y-5 overflow-y-auto px-3 pb-4">
      <MiniMonth :selected="selected" :week-start="cal.weekStart" :range-start="rangeStart" :range-end="rangeEnd" :busy-days="busyDays" @pick="(d) => emit('pick', d)" />

      <section aria-labelledby="cal-mailboxes">
        <h2 id="cal-mailboxes" class="text-muted-foreground mb-1.5 px-1 text-[11px] font-medium tracking-wide uppercase">Calendars</h2>
        <ul class="space-y-1">
          <li v-for="a in accounts.accounts" :key="a.id">
            <!-- Mailbox header: click to collapse/expand; "Connect" when calendar access is missing. -->
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="hover:bg-sidebar-accent/60 flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:hover:bg-transparent"
                :aria-expanded="a.calendarConsent === false ? undefined : expanded.has(a.id)"
                :disabled="a.calendarConsent === false"
                @click="toggle(a.id)"
              >
                <ChevronRightIcon :class="['text-muted-foreground size-3.5 shrink-0 transition-transform', expanded.has(a.id) && 'rotate-90', a.calendarConsent === false && 'invisible']" />
                <AccountAvatar :account="a" :size="18" :glyph="false" />
                <span class="min-w-0 flex-1 truncate text-[12.5px] font-medium" :title="a.email">{{ a.email }}</span>
              </button>
              <Button
                v-if="a.calendarConsent === false"
                variant="outline"
                size="xs"
                class="h-6 shrink-0 text-[11px]"
                :disabled="cal.connecting === a.id"
                :aria-label="`Connect the calendar of ${a.email}`"
                @click="cal.connect(a.id)"
              >
                <LoaderIcon v-if="cal.connecting === a.id" class="animate-spin" /> Connect
              </Button>
            </div>
            <ul v-if="expanded.has(a.id) && a.calendarConsent !== false" class="mt-0.5 ml-5 space-y-px">
              <li v-for="c in cal.calendarsOf(a.id)" :key="c.id">
                <label
                  class="hover:bg-sidebar-accent/60 flex cursor-pointer items-center gap-2.5 rounded-md px-1.5 py-1 text-[13px]"
                  :title="`${c.name}\nAlt-click to show only this calendar`"
                  @click.alt.prevent="cal.soloCalendar(c.id)"
                >
                  <input
                    type="checkbox"
                    class="peer sr-only"
                    :checked="c.visible"
                    @change="cal.setCalendarVisible(c.id, ($event.target as HTMLInputElement).checked)"
                  />
                  <span
                    class="peer-focus-visible:ring-ring flex size-3.5 shrink-0 items-center justify-center rounded-[4px] border-2 transition-colors peer-focus-visible:ring-2"
                    :style="{ borderColor: c.color, background: c.visible ? c.color : 'transparent' }"
                    aria-hidden="true"
                  >
                    <svg v-if="c.visible" viewBox="0 0 24 24" class="size-2.5 text-white" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                  </span>
                  <span class="min-w-0 flex-1 truncate">{{ c.name }}</span>
                  <span v-if="!c.canEdit" class="text-muted-foreground text-[10.5px]">read-only</span>
                </label>
              </li>
              <li v-if="!cal.calendarsOf(a.id).length" class="text-muted-foreground px-1.5 py-1 text-xs">No calendars</li>
            </ul>
          </li>
        </ul>
        <p v-if="!accounts.accounts.length" class="text-muted-foreground px-1 text-xs">Add an account to see its calendar.</p>
      </section>

    </div>
  </aside>
</template>
