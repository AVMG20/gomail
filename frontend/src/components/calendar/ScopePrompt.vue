<script setup lang="ts">
import { computed } from 'vue'
import { RepeatIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { answerConfirm, answerScope, confirmRequest, scopeRequest } from './scopePrompt'

const verb = computed(() => ({ edit: 'Save changes to', move: 'Move', resize: 'Change the time of', delete: 'Delete' })[scopeRequest.value?.action ?? 'edit'])
</script>

<template>
  <Dialog :open="!!scopeRequest" @update:open="(v: boolean) => !v && answerScope(null)">
    <!-- z-[60]: above the event card (z-50) these prompts are asked from. -->
    <DialogContent class="z-[60] gap-4 sm:max-w-[380px]" overlay-class="z-[60]" :show-close-button="false">
      <div class="flex items-start gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-full"><RepeatIcon class="size-4" /></span>
        <div class="min-w-0">
          <DialogTitle class="text-base">{{ verb }} a recurring event</DialogTitle>
          <DialogDescription class="mt-1"><span class="block truncate">“{{ scopeRequest?.title || 'Untitled event' }}” repeats.</span><template v-if="scopeRequest?.action === 'delete'"> Deleting can’t be undone.</template></DialogDescription>
        </div>
      </div>
      <div class="grid gap-2">
        <Button variant="outline" class="justify-start" autofocus @click="answerScope('this')">This event only</Button>
        <Button :variant="scopeRequest?.action === 'delete' ? 'destructive' : 'outline'" class="justify-start" @click="answerScope('series')">All events in the series</Button>
      </div>
      <Button variant="ghost" size="sm" class="text-muted-foreground justify-self-end" @click="answerScope(null)">Cancel</Button>
    </DialogContent>
  </Dialog>
  <Dialog :open="!!confirmRequest" @update:open="(v: boolean) => !v && answerConfirm(false)">
    <DialogContent class="z-[60] gap-4 sm:max-w-[400px]" overlay-class="z-[60]" :show-close-button="false">
      <div>
        <DialogTitle class="text-base">{{ confirmRequest?.title }}</DialogTitle>
        <DialogDescription class="mt-1.5 leading-relaxed">{{ confirmRequest?.description }}</DialogDescription>
      </div>
      <div class="flex justify-end gap-2">
        <Button variant="ghost" size="sm" @click="answerConfirm(false)">{{ confirmRequest?.cancel }}</Button>
        <Button size="sm" autofocus @click="answerConfirm(true)">{{ confirmRequest?.confirm }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
