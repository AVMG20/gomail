<script setup lang="ts">
// After "Not junk": the senders still have mail in Junk. Offer to move all of it to the inbox.
import { computed } from 'vue'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { useSenderRulesStore } from '@/stores/senderRules'

const rules = useSenderRulesStore()
const left = computed(() => rules.junkLeft)
const total = computed(() => left.value.reduce((n, s) => n + s.count, 0))
const open = computed({
  get: () => left.value.length > 0,
  set: (v) => { if (!v) rules.junkLeft = [] },
})
const title = computed(() => {
  const what = `${total.value} more ${total.value === 1 ? 'message' : 'messages'}`
  return left.value.length === 1 ? `Move ${what} from ${left.value[0].email} to the Inbox?` : `Move ${what} from ${left.value.length} senders to the Inbox?`
})
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent class="sm:max-w-[460px]">
      <AlertDialogHeader>
        <AlertDialogTitle class="break-words">{{ title }}</AlertDialogTitle>
        <AlertDialogDescription>
          {{ left.length === 1 ? 'This sender’s' : 'These senders’' }} other mail is still in Junk. New mail from them skips Junk from now on.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <ul v-if="left.length > 1" class="text-muted-foreground max-h-40 space-y-1 overflow-y-auto text-xs">
        <li v-for="s in left" :key="`${s.accountId} ${s.email}`" class="flex justify-between gap-3">
          <span class="truncate">{{ s.email }}</span><span class="shrink-0 tabular-nums">{{ s.count }}</span>
        </li>
      </ul>
      <AlertDialogFooter>
        <AlertDialogCancel>Keep in Junk</AlertDialogCancel>
        <AlertDialogAction @click="rules.moveJunkLeft()">Move to Inbox</AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
