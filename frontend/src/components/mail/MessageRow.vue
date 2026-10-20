<script setup lang="ts">
import { computed } from 'vue'
import { ArchiveIcon, MailIcon, MailOpenIcon, PaperclipIcon, StarIcon, Trash2Icon } from '@lucide/vue'
import LabelPill from '@/components/common/LabelPill.vue'
import Tip from '@/components/common/Tip.vue'
import { displayName, listDate } from '@/lib/format'
import type { Account, Label, MessageSummary } from '@/lib/types'
import { chipLabelIds } from '@/lib/labelScope'
import { useAccountsStore } from '@/stores/accounts'

const props = defineProps<{
  message: MessageSummary
  open: boolean
  selected: boolean
  selectionMode: boolean
  compact: boolean
  account?: Account
  showAccount: boolean
  labelMap: Map<string, Label>
  sentView: boolean
  /** Only the hovered row mounts its quick actions (keeps scrolling cheap). */
  hovered: boolean
}>()

const emit = defineEmits<{
  open: [e: MouseEvent]
  toggle: [e: MouseEvent | null]
  archive: []
  trash: []
  read: [read: boolean]
  star: [starred: boolean]
}>()

const who = computed(() => {
  const m = props.message
  if (props.sentView && m.to.length) return `To: ${m.to.map((a) => displayName(a)).join(', ')}`
  // Conversations: "Sam, me, Alex" like most mail apps.
  const p = m.participants
  if (p && p.length > 1) return p.length > 3 ? `${p[0]} … ${p.slice(-2).join(', ')}` : p.join(', ')
  return displayName(m.from)
})

const accounts = useAccountsStore()
const pills = computed(() => {
  const details = new Map((props.message.labelDetails ?? []).map((d) => [d.labelId, d]))
  return chipLabelIds(props.message.labels, props.message.folderIds, (f) => accounts.folderMap.get(f)?.kind)
    .map((id) => ({ label: props.labelMap.get(id), detail: details.get(id) }))
    .filter((x): x is { label: Label; detail: typeof x.detail } => !!x.label)
})
const shown = computed(() => pills.value.slice(0, 2))
const extra = computed(() => pills.value.length - shown.value.length)
const date = computed(() => listDate(props.message.date))
</script>

<template>
  <div
    :data-id="message.id"
    role="option"
    :aria-selected="open"
    :class="[
      'group/row relative flex h-full cursor-default items-stretch border-b border-border/60 pr-3 pl-1 transition-colors select-none',
      open ? 'bg-selected' : selected ? 'bg-primary/[0.06]' : 'hover:bg-muted/50',
    ]"
    @click="emit('open', $event)"
  >
    <span
      v-if="showAccount && account"
      class="absolute top-2 bottom-2 left-0 w-[3px] rounded-r-full"
      :style="{ background: account.color }"
      :title="account.email"
    />
    <span v-if="open" class="absolute inset-y-0 left-0 w-[3px]" :style="{ background: showAccount && account ? account.color : 'var(--account)' }" />

    <!-- Leading gutter: unread dot, or a checkbox on hover / selection mode -->
    <div :class="['flex w-8 shrink-0 justify-center', compact ? 'items-center' : 'pt-[15px]']" @click.stop="emit('toggle', $event)">
      <!-- Lightweight checkbox (a reka Checkbox per virtual row is costly while scrolling) -->
      <span
        role="checkbox"
        :aria-checked="selected"
        aria-label="Select message"
        :class="[
          'flex size-4 shrink-0 items-center justify-center rounded-[4px] border transition-[opacity,background-color]',
          selected ? 'bg-primary border-primary text-primary-foreground' : 'border-input bg-background dark:bg-input/30',
          selected || selectionMode ? 'opacity-100' : 'opacity-0 group-hover/row:opacity-100',
        ]"
        @click.stop="emit('toggle', $event)"
      >
        <svg v-if="selected" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
      </span>
      <span
        v-if="message.unread && !selected && !selectionMode"
        class="bg-primary pointer-events-none absolute size-2 rounded-full transition-opacity group-hover/row:opacity-0"
        :class="compact ? '' : 'mt-1'"
      />
    </div>

    <!-- Comfortable: three lines -->
    <div v-if="!compact" class="flex min-w-0 flex-1 flex-col justify-center gap-0.5 py-2">
      <div class="flex items-center gap-2">
        <span :class="['min-w-0 truncate text-[13.5px]', message.unread ? 'text-foreground font-semibold' : 'text-foreground/80 font-medium']">{{ who }}</span>
        <span v-if="(message.threadCount ?? 1) > 1" class="text-muted-foreground shrink-0 text-[11px] tabular-nums" :aria-label="`${message.threadCount} messages`">{{ message.threadCount }}</span>
        <span class="flex-1" />
        <PaperclipIcon v-if="message.hasAttachments" class="text-muted-foreground size-3.5 shrink-0" />
        <StarIcon v-if="message.starred" class="size-3.5 shrink-0 fill-amber-400 text-amber-400 group-hover/row:invisible" />
        <span :class="['shrink-0 text-xs tabular-nums group-hover/row:invisible', message.unread ? 'text-foreground font-medium' : 'text-muted-foreground']">{{ date }}</span>
      </div>
      <div :class="['truncate text-[13px]', message.unread ? 'text-foreground font-medium' : 'text-foreground/75']">{{ message.subject || '(no subject)' }}</div>
      <div class="flex min-w-0 items-center gap-2">
        <span class="text-muted-foreground min-w-[40%] flex-1 truncate text-[12.5px]">{{ message.snippet }}</span>
        <!-- Pills may shrink/hide but never push the snippet out -->
        <span v-if="pills.length" class="flex max-w-[55%] min-w-0 shrink items-center justify-end gap-1 overflow-hidden">
          <LabelPill v-for="(p, i) in shown" :key="p.label.id" :label="p.label" :detail="p.detail" :class="i > 0 ? 'hidden @[440px]:inline-flex' : 'min-w-0 shrink'" />
          <span v-if="pills.length > 1" class="text-muted-foreground shrink-0 text-[10.5px] font-medium @[440px]:hidden">+{{ pills.length - 1 }}</span>
          <span v-if="extra > 0" class="text-muted-foreground hidden shrink-0 text-[10.5px] font-medium @[440px]:inline">+{{ extra }}</span>
        </span>
      </div>
    </div>

    <!-- Compact: one line -->
    <div v-else class="flex min-w-0 flex-1 items-center gap-3">
      <span :class="['w-[28%] max-w-44 min-w-24 shrink-0 truncate text-[13px]', message.unread ? 'font-semibold' : 'text-foreground/80']">{{ who }}<span v-if="(message.threadCount ?? 1) > 1" class="text-muted-foreground ml-1 text-[11px] font-normal">{{ message.threadCount }}</span></span>
      <span class="min-w-0 flex-1 truncate text-[13px]">
        <span :class="message.unread ? 'font-medium' : 'text-foreground/80'">{{ message.subject || '(no subject)' }}</span>
        <span class="text-muted-foreground"> — {{ message.snippet }}</span>
      </span>
      <span v-if="pills.length" class="hidden shrink-0 items-center gap-1 @[560px]:flex">
        <LabelPill :label="pills[0].label" :detail="pills[0].detail" />
        <span v-if="pills.length > 1" class="text-muted-foreground text-[10.5px] font-medium">+{{ pills.length - 1 }}</span>
      </span>
      <!-- Narrow compact rows: the labels as color dots rather than not at all. -->
      <span v-if="pills.length" class="flex shrink-0 items-center gap-0.5 @[560px]:hidden" :title="pills.map((p) => p.label.name).join(', ')" :aria-label="`Labels: ${pills.map((p) => p.label.name).join(', ')}`">
        <span v-for="p in pills.slice(0, 3)" :key="p.label.id" class="size-2 rounded-full" :style="{ background: p.label.color }" />
      </span>
      <PaperclipIcon v-if="message.hasAttachments" class="text-muted-foreground size-3.5 shrink-0" />
      <StarIcon v-if="message.starred" class="size-3.5 shrink-0 fill-amber-400 text-amber-400 group-hover/row:invisible" aria-label="Starred" />
      <span :class="['w-16 shrink-0 text-right text-xs tabular-nums group-hover/row:invisible', message.unread ? 'font-medium' : 'text-muted-foreground']">{{ date }}</span>
    </div>

    <!-- Hover quick actions -->
    <div
      v-if="hovered"
      :class="[
        'bg-background/95 border-border/70 absolute right-2 flex items-center gap-0.5 rounded-md border p-0.5 shadow-sm backdrop-blur',
        compact ? 'top-1/2 -translate-y-1/2' : 'top-1.5',
      ]"
      aria-hidden="true"
      @click.stop
    >
      <Tip label="Archive" shortcut="e">
        <span role="presentation" class="hover:bg-muted text-muted-foreground hover:text-foreground flex size-7 cursor-default items-center justify-center rounded" @click="emit('archive')"><ArchiveIcon class="size-4" /></span>
      </Tip>
      <Tip label="Delete" shortcut="#">
        <span role="presentation" class="hover:bg-muted text-muted-foreground hover:text-foreground flex size-7 cursor-default items-center justify-center rounded" @click="emit('trash')"><Trash2Icon class="size-4" /></span>
      </Tip>
      <Tip :label="message.unread ? 'Mark as read' : 'Mark as unread'" :shortcut="message.unread ? 'shift+i' : 'u'">
        <span role="presentation" class="hover:bg-muted text-muted-foreground hover:text-foreground flex size-7 cursor-default items-center justify-center rounded" @click="emit('read', message.unread)">
          <MailOpenIcon v-if="message.unread" class="size-4" />
          <MailIcon v-else class="size-4" />
        </span>
      </Tip>
      <Tip :label="message.starred ? 'Unstar' : 'Star'" shortcut="s">
        <span role="presentation" class="hover:bg-muted text-muted-foreground hover:text-foreground flex size-7 cursor-default items-center justify-center rounded" @click="emit('star', !message.starred)">
          <StarIcon class="size-4" :class="message.starred && 'fill-amber-400 text-amber-400'" />
        </span>
      </Tip>
    </div>
  </div>
</template>
