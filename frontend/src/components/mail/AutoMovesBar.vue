<script setup lang="ts">
// "Sorted here for you": above the Junk, Archive and Trash lists, what Jev, rules and label
// actions moved here in the last day, why, and a one-click way back. Automation only feels
// safe when you can see what it did.
import { computed, ref, watch } from 'vue'
import { InboxIcon, SparklesIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { relativeTime } from '@/lib/format'
import type { AutoMove } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'

const mail = useMailStore()
const accounts = useAccountsStore()
const moves = ref<AutoMove[]>([])
const open = ref(false)

const kind = computed(() => mail.mailbox.kind)
const shown = computed(() => hasMethod('AutoMoves') && ['spam', 'archive', 'trash'].includes(kind.value) && !mail.search)
const where = computed(() => ({ spam: 'Junk', archive: 'the archive', trash: 'Trash' })[kind.value] ?? '')

let seq = 0
async function load() {
  if (!shown.value) return void (moves.value = [])
  const s = ++seq
  try {
    const list = await api.autoMoves(mail.mailbox.accountId, kind.value, 24)
    if (s === seq) moves.value = list
  } catch {
    if (s === seq) moves.value = []
  }
}
// Reload when the box changes or its mail does (a sync, an undo).
watch(() => [mail.mailbox.accountId, kind.value, mail.search, mail.total], load, { immediate: true })

const name = (m: AutoMove) => m.fromName || m.fromEmail
async function back(m: AutoMove) {
  moves.value = moves.value.filter((x) => x.messageId !== m.messageId)
  try {
    if (m.to === 'spam') await mail.notJunk([m.messageId])
    else {
      const inbox = accounts.folderOf(m.accountId, 'inbox')?.id
      if (inbox) await mail.moveToFolder([m.messageId], inbox)
    }
  } catch (e) {
    toast.error('Couldn’t move it back', { description: errorMessage(e) })
    void load()
  }
}
</script>

<template>
  <div v-if="shown && moves.length" class="border-border bg-muted/30 flex items-center gap-2 border-b px-4 py-1.5 text-xs">
    <SparklesIcon class="size-3.5 shrink-0 text-violet-500" aria-hidden="true" />
    <span class="text-muted-foreground min-w-0 flex-1 truncate">
      {{ moves.length }} {{ moves.length === 1 ? 'mail was' : 'mails were' }} sorted into {{ where }} for you today
    </span>
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button variant="ghost" size="xs" class="shrink-0">Review</Button>
      </PopoverTrigger>
      <PopoverContent align="end" class="w-[26rem] p-0">
        <div class="border-border border-b px-3 py-2 text-xs font-medium">Sorted into {{ where }} in the last 24 hours</div>
        <ul class="max-h-80 overflow-y-auto py-1" aria-label="Sorted for you">
          <li v-for="m in moves" :key="m.messageId" class="hover:bg-muted/50 flex items-start gap-2 px-3 py-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate text-[13px] font-medium">{{ name(m) }}</span>
                <span class="text-muted-foreground ml-auto shrink-0 text-[11px]">{{ relativeTime(m.at) }}</span>
              </div>
              <div class="truncate text-xs">{{ m.subject || '(no subject)' }}</div>
              <div class="text-muted-foreground truncate text-[11px]">{{ m.reason }}</div>
            </div>
            <Button variant="outline" size="xs" class="mt-0.5 shrink-0" :aria-label="`Move ${m.subject} back to the Inbox`" @click="back(m)">
              <InboxIcon /> {{ m.to === 'spam' ? 'Not junk' : 'To Inbox' }}
            </Button>
          </li>
        </ul>
      </PopoverContent>
    </Popover>
  </div>
</template>
