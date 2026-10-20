<script setup lang="ts">
import { computed } from 'vue'
import { RepeatIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { answerScope, scopeRequest } from './scopePrompt'

const verb = computed(() => ({ edit: 'Save changes to', move: 'Move', delete: 'Delete' })[scopeRequest.value?.action ?? 'edit'])
</script>

<template>
  <Dialog :open="!!scopeRequest" @update:open="(v: boolean) => !v && answerScope(null)">
    <DialogContent class="gap-4 sm:max-w-[380px]" :show-close-button="false">
      <div class="flex items-start gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-full"><RepeatIcon class="size-4" /></span>
        <div class="min-w-0">
          <DialogTitle class="text-base">{{ verb }} a recurring event</DialogTitle>
          <DialogDescription class="mt-1 truncate">“{{ scopeRequest?.title || 'Untitled' }}” repeats.</DialogDescription>
        </div>
      </div>
      <div class="grid gap-2">
        <Button variant="outline" class="justify-start" autofocus @click="answerScope('this')">This event only</Button>
        <Button :variant="scopeRequest?.action === 'delete' ? 'destructive' : 'outline'" class="justify-start" @click="answerScope('series')">All events in the series</Button>
      </div>
      <Button variant="ghost" size="sm" class="text-muted-foreground justify-self-end" @click="answerScope(null)">Cancel</Button>
    </DialogContent>
  </Dialog>
</template>
