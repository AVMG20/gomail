<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDownIcon, CornerUpLeftIcon, PaperclipIcon } from '@lucide/vue'
import SenderAvatar from '@/components/common/SenderAvatar.vue'
import Tip from '@/components/common/Tip.vue'
import { displayName, formatAddress, listDate, longDate, parseMailto } from '@/lib/format'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { useComposeStore } from '@/stores/compose'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import type { Address, Message } from '@/lib/types'
import AttachmentList from './AttachmentList.vue'
import MessageBody from './MessageBody.vue'

const props = defineProps<{ message: Message; expanded: boolean; ownEmail: string; allowRemote: boolean }>()
const emit = defineEmits<{ toggle: []; reply: []; loadImages: [] }>()

const showDetails = ref(false)
const confirmUnsub = ref(false)
const compose = useComposeStore()

async function unsubscribe() {
  const u = props.message.unsubscribe
  if (!u) return
  try {
    if (hasMethod('Unsubscribe')) {
      const r = await api.unsubscribe(props.message.id)
      // "url": the backend already opened the page in the browser (don't open it twice).
      if (r.method === 'url') toast('Opened the unsubscribe page in your browser')
      else toast.success(`Unsubscribed from ${displayName(props.message.from)}`, { description: r.method === 'mailto' ? 'An unsubscribe email was sent.' : 'It can take a few days for mail to stop.' })
    } else if (u.url) {
      await api.openExternal(u.url)
    } else if (u.mailto) {
      void compose.composeWith(parseMailto(u.mailto.startsWith('mailto:') ? u.mailto : `mailto:${u.mailto}`))
    }
  } catch (e) {
    toast.error('Couldn’t unsubscribe', { description: errorMessage(e) })
  }
}

function short(list: Address[]) {
  return list.map((a) => (a.email.toLowerCase() === props.ownEmail.toLowerCase() ? 'me' : displayName(a))).join(', ')
}
const recipients = computed(() => {
  const to = short(props.message.to)
  const cc = props.message.cc.length ? `, cc ${short(props.message.cc)}` : ''
  return `to ${to || 'undisclosed recipients'}${cc}`
})
</script>

<template>
  <article class="border-border/70 border-b last:border-b-0">
    <!-- Collapsed -->
    <button
      v-if="!expanded"
      type="button"
      class="hover:bg-muted/40 flex w-full items-center gap-3 px-6 py-3 text-left outline-none focus-visible:bg-muted/40"
      @click="emit('toggle')"
    >
      <SenderAvatar :address="message.from" :size="28" />
      <span class="w-40 shrink-0 truncate text-[13px] font-medium">{{ displayName(message.from) }}</span>
      <span class="text-muted-foreground min-w-0 flex-1 truncate text-[13px]">{{ message.snippet }}</span>
      <PaperclipIcon v-if="message.attachments.length" class="text-muted-foreground size-3.5 shrink-0" />
      <span class="text-muted-foreground shrink-0 text-xs">{{ listDate(message.date) }}</span>
    </button>

    <!-- Expanded -->
    <div v-else class="px-6 pt-4 pb-5">
      <div class="flex items-start gap-3">
        <SenderAvatar :address="message.from" :size="36" />
        <div class="min-w-0 flex-1">
          <div class="flex items-baseline gap-2">
            <span class="selectable truncate text-[14px] font-semibold">{{ displayName(message.from) }}</span>
            <span class="text-muted-foreground selectable truncate text-xs">{{ message.from.email }}</span>
            <button
              v-if="message.unsubscribe && (message.unsubscribe.url || message.unsubscribe.mailto)"
              type="button"
              class="text-muted-foreground hover:text-foreground shrink-0 text-xs underline-offset-2 hover:underline"
              @click="confirmUnsub = true"
            >Unsubscribe</button>
          </div>
          <button type="button" class="text-muted-foreground hover:text-foreground flex max-w-full items-center gap-1 text-xs" @click="showDetails = !showDetails">
            <span class="truncate">{{ recipients }}</span>
            <ChevronDownIcon class="size-3 shrink-0 transition-transform" :class="showDetails && 'rotate-180'" />
          </button>
          <dl v-if="showDetails" class="bg-muted/50 selectable mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md px-3 py-2 text-xs">
            <dt class="text-muted-foreground">From</dt><dd class="break-all">{{ formatAddress(message.from) }}</dd>
            <dt class="text-muted-foreground">To</dt><dd class="break-all">{{ message.to.map(formatAddress).join(', ') }}</dd>
            <template v-if="message.cc.length"><dt class="text-muted-foreground">Cc</dt><dd class="break-all">{{ message.cc.map(formatAddress).join(', ') }}</dd></template>
            <template v-if="message.replyTo.length"><dt class="text-muted-foreground">Reply-To</dt><dd class="break-all">{{ message.replyTo.map(formatAddress).join(', ') }}</dd></template>
            <dt class="text-muted-foreground">Date</dt><dd>{{ longDate(message.date) }}</dd>
          </dl>
        </div>
        <div class="flex shrink-0 items-center gap-1">
          <button type="button" class="text-muted-foreground text-xs" :title="longDate(message.date)" @click="emit('toggle')">{{ listDate(message.date) }}</button>
          <Tip label="Reply" shortcut="r">
            <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-muted ml-1 flex size-7 items-center justify-center rounded-md" aria-label="Reply" @click="emit('reply')">
              <CornerUpLeftIcon class="size-4" />
            </button>
          </Tip>
        </div>
      </div>
      <div class="mt-4 pl-0">
        <MessageBody :message="message" :allow-remote="allowRemote" @load-images="emit('loadImages')" />
        <AttachmentList v-if="message.attachments.length" :message-id="message.id" :attachments="message.attachments" />
      </div>
    </div>
    <AlertDialog v-model:open="confirmUnsub">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Unsubscribe from {{ displayName(message.from) }}?</AlertDialogTitle>
          <AlertDialogDescription>
            {{ message.unsubscribe?.oneClick ? 'gomail will send an unsubscribe request to the sender on your behalf.' : message.unsubscribe?.mailto ? 'gomail will send an unsubscribe email from your account.' : 'This opens the sender’s unsubscribe page in your browser.' }}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction @click="unsubscribe">Unsubscribe</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </article>
</template>
