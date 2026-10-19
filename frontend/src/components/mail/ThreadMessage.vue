<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDownIcon, CornerUpLeftIcon, PaperclipIcon } from '@lucide/vue'
import SenderAvatar from '@/components/common/SenderAvatar.vue'
import Tip from '@/components/common/Tip.vue'
import { displayName, formatAddress, listDate, longDate } from '@/lib/format'
import type { Address, Message } from '@/lib/types'
import AttachmentList from './AttachmentList.vue'
import MessageBody from './MessageBody.vue'

const props = defineProps<{ message: Message; expanded: boolean; ownEmail: string; allowRemote: boolean }>()
const emit = defineEmits<{ toggle: []; reply: []; loadImages: [] }>()

const showDetails = ref(false)

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
  </article>
</template>
