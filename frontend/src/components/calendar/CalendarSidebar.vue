<script setup lang="ts">
// Calendar sidebar: new event, mini month, and one collapsible group per mailbox
// with its calendars as show/hide checkboxes. Right-click (or the hover "⋯")
// on a group header opens the mailbox menu: disable/enable, mute, solo, sync,
// color. Right-click a calendar to show/hide it, show only it, or recolor it.
// Mailboxes without calendar access show "Connect" instead.
import { computed, ref } from 'vue'
import { BellOffIcon, ChevronRightIcon, EllipsisIcon, EyeIcon, EyeOffIcon, LoaderIcon, PaletteIcon, PlusIcon, ScanEyeIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuSub, ContextMenuSubContent,
  ContextMenuSubTrigger, ContextMenuTrigger,
} from '@/components/ui/context-menu'
import ColorSwatches from './ColorSwatches.vue'
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import MailboxMenuItems from './MailboxMenuItems.vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useCalendarStore } from '@/stores/calendar'
import MiniMonth from './MiniMonth.vue'

defineProps<{ selected: Date; rangeStart?: Date; rangeEnd?: Date }>()
const emit = defineEmits<{ pick: [Date]; create: [] }>()
const cal = useCalendarStore()
const accounts = useAccountsStore()
const expanded = ref<Set<string>>(new Set(accounts.accounts.map((a) => a.id)))

/** A right-click (or Ctrl-click, macOS's right-click) opens the calendar's menu; WebKit also
 *  delivers it as a click on the label, which would toggle the checkbox. */
function ignoreSecondaryClick(e: MouseEvent) {
  if (e.button !== 0 || e.ctrlKey) e.preventDefault()
}
// Belt and braces: a checkbox change right after the menu opened came from that same click.
let menuOpenedAt = 0
function onVisibleChange(id: string, e: Event, visible: boolean) {
  const box = e.target as HTMLInputElement
  if (Date.now() - menuOpenedAt < 600) {
    box.checked = visible
    return
  }
  void cal.setCalendarVisible(id, box.checked)
}

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
    <div data-drag class="px-3 pt-3 pb-3">
      <Button class="w-full justify-start gap-2" @click="emit('create')"><PlusIcon /> New event <span class="ml-auto text-[11px] opacity-70">N</span></Button>
    </div>
    <div class="thin-scroll min-h-0 flex-1 space-y-5 overflow-y-auto px-3 pb-4">
      <MiniMonth :selected="selected" :week-start="cal.weekStart" :range-start="rangeStart" :range-end="rangeEnd" :busy-days="busyDays" @pick="(d) => emit('pick', d)" />

      <section aria-labelledby="cal-mailboxes">
        <h2 id="cal-mailboxes" class="text-muted-foreground mb-1.5 px-1 text-[11px] font-medium tracking-wide uppercase">Calendars</h2>
        <ul class="space-y-1">
          <li v-for="a in accounts.accounts" :key="a.id">
            <!-- Mailbox header: click to collapse/expand; right-click or "⋯" for the mailbox menu. -->
            <div v-if="a.calendarConsent === false" class="flex items-center gap-1">
              <div class="flex min-w-0 flex-1 items-center gap-2 px-1 py-1.5">
                <ChevronRightIcon class="invisible size-3.5 shrink-0" />
                <AccountAvatar :account="a" :size="18" :glyph="false" />
                <span class="text-muted-foreground min-w-0 flex-1 truncate text-[12.5px] font-medium" :title="a.email">{{ a.email }}</span>
              </div>
              <Button
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
            <ContextMenu v-else>
              <ContextMenuTrigger as-child>
                <div :class="['group/mbx hover:bg-sidebar-accent/60 flex items-center rounded-md', a.calendarEnabled === false && 'mbx-disabled']">
                  <button
                    type="button"
                    class="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    :aria-expanded="expanded.has(a.id)"
                    @click="toggle(a.id)"
                  >
                    <ChevronRightIcon :class="['text-muted-foreground size-3.5 shrink-0 transition-transform', expanded.has(a.id) && 'rotate-90']" />
                    <AccountAvatar :account="a" :size="18" :glyph="false" class="mbx-dot" />
                    <span :class="['min-w-0 flex-1 truncate text-[12.5px] font-medium', a.calendarEnabled === false && 'text-muted-foreground']" :title="a.email">{{ a.email }}</span>
                    <span v-if="a.calendarEnabled === false" class="bg-muted text-muted-foreground shrink-0 rounded px-1 text-[10px] leading-4">Disabled</span>
                    <BellOffIcon v-else-if="a.calendarMuted" class="text-muted-foreground size-3.5 shrink-0" aria-label="Notifications muted" />
                  </button>
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <button
                        type="button"
                        class="text-muted-foreground hover:text-foreground mr-0.5 flex size-6 shrink-0 items-center justify-center rounded opacity-0 outline-none group-hover/mbx:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:opacity-100"
                        :aria-label="`Calendar options for ${a.email}`"
                      >
                        <EllipsisIcon class="size-4" />
                      </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="start" class="w-56">
                      <MailboxMenuItems :account="a" kind="dropdown" />
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </ContextMenuTrigger>
              <ContextMenuContent class="w-56">
                <MailboxMenuItems :account="a" kind="context" />
              </ContextMenuContent>
            </ContextMenu>
            <ul v-if="expanded.has(a.id) && a.calendarConsent !== false" class="mt-0.5 ml-5 space-y-px">
              <li v-for="c in cal.calendarsOf(a.id)" :key="c.id">
                <ContextMenu>
                <ContextMenuTrigger as-child>
                <label
                  :class="[
                    'flex items-center gap-2.5 rounded-md px-1.5 py-1 text-[13px]',
                    a.calendarEnabled === false ? 'text-muted-foreground mbx-disabled cursor-default' : 'hover:bg-sidebar-accent/60 cursor-pointer',
                  ]"
                  :title="a.calendarEnabled === false ? `${c.name}\nThis mailbox’s calendar is disabled` : `${c.name}\nShow or hide · Alt-click to show only this calendar`"
                  @click.alt.prevent="a.calendarEnabled !== false && cal.soloCalendar(c.id)"
                  @click="ignoreSecondaryClick"
                  @contextmenu="menuOpenedAt = Date.now()"
                >
                  <input
                    type="checkbox"
                    class="peer sr-only"
                    :checked="c.visible"
                    :disabled="a.calendarEnabled === false"
                    @change="onVisibleChange(c.id, $event, c.visible)"
                  />
                  <span
                    class="mbx-dot peer-focus-visible:ring-ring flex size-3.5 shrink-0 items-center justify-center rounded-[4px] border-2 transition-colors peer-focus-visible:ring-2"
                    :style="{ borderColor: c.color, background: c.visible ? c.color : 'transparent' }"
                    aria-hidden="true"
                  >
                    <svg v-if="c.visible" viewBox="0 0 24 24" class="size-2.5 text-white" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
                  </span>
                  <span class="min-w-0 flex-1 truncate">{{ c.name }}</span>
                  <span v-if="!c.canEdit" class="text-muted-foreground text-[10.5px]">read-only</span>
                </label>
                </ContextMenuTrigger>
                <ContextMenuContent class="w-56">
                  <ContextMenuLabel class="text-muted-foreground truncate text-xs font-normal">{{ c.name }}</ContextMenuLabel>
                  <ContextMenuItem :disabled="a.calendarEnabled === false" @select="cal.setCalendarVisible(c.id, !c.visible)">
                    <component :is="c.visible ? EyeOffIcon : EyeIcon" /> {{ c.visible ? 'Hide calendar' : 'Show calendar' }}
                  </ContextMenuItem>
                  <ContextMenuItem :disabled="a.calendarEnabled === false" @select="cal.soloCalendar(c.id)"><ScanEyeIcon /> Show only this calendar</ContextMenuItem>
                  <ContextMenuSeparator />
                  <ContextMenuSub>
                    <ContextMenuSubTrigger class="gap-2"><PaletteIcon class="text-muted-foreground size-4" /> Color</ContextMenuSubTrigger>
                    <ContextMenuSubContent class="w-auto p-0">
                      <ColorSwatches :current="c.color" :reset-label="`Use ${a.provider === 'outlook' ? 'Outlook' : 'Google'}’s color`" @pick="(col) => cal.setCalendarColor(c.id, col)" />
                    </ContextMenuSubContent>
                  </ContextMenuSub>
                </ContextMenuContent>
                </ContextMenu>
              </li>
              <li v-if="!cal.calendarsOf(a.id).length" class="text-muted-foreground px-1.5 py-1 text-xs">{{ cal.loaded || cal.error ? 'No calendars' : 'Loading…' }}</li>
            </ul>
          </li>
        </ul>
        <p v-if="!accounts.accounts.length" class="text-muted-foreground px-1 text-xs">Add an account to see its calendar.</p>
      </section>

    </div>
  </aside>
</template>

<style scoped>
/* Disabled mailbox: desaturated colour dots and avatar. */
.mbx-disabled :deep(.mbx-dot) {
  filter: grayscale(1);
  opacity: 0.55;
}
</style>
