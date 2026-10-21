<script setup lang="ts">
// The senders marked "Not junk" in a mailbox: their mail that lands in Junk goes to the inbox.
// Opened from a small link under the mailbox's rules; remove one or all of them here.
import { computed } from 'vue'
import { XIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useSettingsStore } from '@/stores/settings'

const props = defineProps<{ accountId: string }>()
const open = defineModel<boolean>('open', { default: false })
const settings = useSettingsStore()
const senders = computed(() => settings.settings?.notJunkSenders?.[props.accountId] ?? [])

function save(list: string[]) {
  const next = { ...(settings.settings?.notJunkSenders ?? {}) }
  if (list.length) next[props.accountId] = list
  else delete next[props.accountId]
  void settings.update({ notJunkSenders: next }, 0)
  if (!list.length) open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-[440px]">
      <DialogHeader>
        <DialogTitle>Senders that skip Junk</DialogTitle>
        <DialogDescription>You marked mail from these senders “Not junk”. When their mail lands in Junk, it’s moved to the Inbox.</DialogDescription>
      </DialogHeader>
      <ul class="border-border -mx-1 max-h-72 divide-y overflow-y-auto rounded-md border" data-testid="not-junk-senders">
        <li v-for="e in senders" :key="e" class="flex items-center gap-2 py-1 pr-1 pl-3 text-sm">
          <span class="selectable min-w-0 flex-1 truncate" :title="e">{{ e }}</span>
          <Button variant="ghost" size="icon-sm" :aria-label="`Remove ${e}`" @click="save(senders.filter((x) => x !== e))"><XIcon /></Button>
        </li>
      </ul>
      <DialogFooter class="sm:justify-between">
        <Button variant="ghost" class="text-destructive hover:text-destructive" @click="save([])">Remove all</Button>
        <Button variant="outline" @click="open = false">Done</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
