<script setup lang="ts">
// One label in the folder sidebar: click filters the list; right-click to show only /
// hide it, rename it in place, recolor or delete it.
import { computed, nextTick, ref } from 'vue'
import { CheckIcon, EyeIcon, EyeOffIcon, ListFilterIcon, PaletteIcon, PencilIcon, SettingsIcon, Trash2Icon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuSub, ContextMenuSubContent,
  ContextMenuSubTrigger, ContextMenuTrigger,
} from '@/components/ui/context-menu'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { LABEL_COLORS } from '@/lib/color'
import { openLabelSettings } from '@/lib/labelScope'
import type { Label, LabelCount } from '@/lib/types'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

const props = defineProps<{ label: Label; count?: LabelCount }>()
const mail = useMailStore()
const settings = useSettingsStore()

const shown = computed(() => mail.labelFilter.labels.includes(props.label.id))
const hidden = computed(() => mail.labelFilter.hidden.includes(props.label.id))
/** Gmail/Outlook and built-in labels follow the provider: only their color is ours to change. In
 *  the unified view one entry stands for the label of that name in every mailbox: those are edited
 *  in each mailbox's settings. */
const inMailbox = computed(() => !!mail.mailbox.accountId)
const own = computed(() => inMailbox.value && (!props.label.source || props.label.source === 'user'))

function formatCount(n: number) {
  return n > 999 ? `${Math.floor(n / 1000)}k` : String(n)
}

// ---- rename in place
const renaming = ref(false)
const draft = ref('')
const input = ref<HTMLInputElement | null>(null)
function startRename() {
  draft.value = props.label.name
  renaming.value = true
  // After the menu closes and gives focus back.
  setTimeout(() => void nextTick(() => { input.value?.focus(); input.value?.select() }), 50)
}
function commitRename() {
  if (!renaming.value) return
  renaming.value = false
  const typed = draft.value.trim()
  if (!typed || typed === props.label.name) return
  const r = settings.renameLabel(props.label.id, typed)
  if (r.deduped) toast(`You already have a label called “${typed}”`, { description: `This one was renamed to “${r.name}”.` })
}

// ---- delete
const confirmDelete = ref(false)
function remove() {
  settings.removeLabel(props.label.id)
  toast(`Label “${props.label.name}” deleted`)
}
</script>

<template>
  <li>
    <div v-if="renaming" class="bg-sidebar-accent flex h-8 items-center gap-2.5 rounded-md px-2.5">
      <span class="flex size-4 shrink-0 items-center justify-center"><span class="size-2.5 rounded-full" :style="{ background: label.color }" /></span>
      <input
        ref="input"
        v-model="draft"
        class="min-w-0 flex-1 bg-transparent text-[13px] outline-none"
        aria-label="Label name"
        maxlength="60"
        @keydown.enter.prevent="commitRename"
        @keydown.esc.prevent="renaming = false"
        @blur="commitRename"
      >
    </div>
    <ContextMenu v-else>
      <ContextMenuTrigger as-child>
        <button
          type="button"
          :class="[
            'flex h-8 w-full items-center gap-2.5 rounded-md px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
            shown ? 'bg-sidebar-accent text-foreground font-medium' : 'text-sidebar-foreground/85 hover:bg-sidebar-accent/60',
          ]"
          :aria-pressed="shown"
          @click="mail.toggleLabelFilter(label.id)"
        >
          <span class="flex size-4 shrink-0 items-center justify-center">
            <span class="size-2.5 rounded-full" :style="{ background: label.color, boxShadow: shown ? `0 0 0 3px color-mix(in oklab, ${label.color} 25%, transparent)` : undefined }" />
          </span>
          <span :class="['flex-1 truncate text-left', hidden && 'text-muted-foreground line-through']">{{ label.name }}</span>
          <EyeOffIcon v-if="hidden" class="text-muted-foreground size-3.5" aria-label="Hidden from the list" />
          <!-- Unread, like the folders above (the label filter popover shows totals). -->
          <span v-if="count?.unread" class="text-foreground/80 text-xs font-medium tabular-nums" :aria-label="`${count.unread} unread`">{{ formatCount(count.unread) }}</span>
        </button>
      </ContextMenuTrigger>
      <ContextMenuContent class="w-52">
        <ContextMenuItem @click="mail.toggleLabelFilter(label.id)">
          <ListFilterIcon /> {{ shown ? 'Stop showing only this' : 'Show only this label' }}
        </ContextMenuItem>
        <ContextMenuItem @click="mail.toggleHiddenLabel(label.id)">
          <component :is="hidden ? EyeIcon : EyeOffIcon" /> {{ hidden ? 'Show in the list again' : 'Hide from the list' }}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem v-if="own" @click="startRename"><PencilIcon /> Rename</ContextMenuItem>
        <ContextMenuSub v-if="inMailbox">
          <ContextMenuSubTrigger class="gap-2"><PaletteIcon class="text-muted-foreground size-4" /> Color</ContextMenuSubTrigger>
          <ContextMenuSubContent class="w-auto p-2">
            <div class="grid grid-cols-5 gap-1.5">
              <button
                v-for="c in LABEL_COLORS"
                :key="c"
                type="button"
                class="flex size-6 items-center justify-center rounded-full"
                :style="{ background: c }"
                :aria-label="`Color ${c}`"
                @click="settings.patchLabel(label.id, { color: c })"
              ><CheckIcon v-if="label.color.toLowerCase() === c.toLowerCase()" class="size-3.5 text-white" /></button>
            </div>
          </ContextMenuSubContent>
        </ContextMenuSub>
        <ContextMenuItem @click="openLabelSettings(mail.mailbox.accountId)"><SettingsIcon /> Edit in Settings…</ContextMenuItem>
        <template v-if="own">
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" @click="confirmDelete = true"><Trash2Icon /> Delete label…</ContextMenuItem>
        </template>
      </ContextMenuContent>
    </ContextMenu>

    <AlertDialog v-model:open="confirmDelete">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete the label “{{ label.name }}”?</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="count?.total">It’s removed from {{ count.total.toLocaleString() }} message{{ count.total === 1 ? '' : 's' }}. </template>
            This can’t be undone. Labels in Gmail or Outlook aren’t changed.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-white hover:bg-destructive/90" @click="remove">Delete label</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </li>
</template>
