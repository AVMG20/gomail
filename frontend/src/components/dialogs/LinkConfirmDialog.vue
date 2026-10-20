<script setup lang="ts">
// "Open link?" before a link from an email leaves the app.
import { computed } from 'vue'
import { ExternalLinkIcon, TriangleAlertIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { cancelLink, confirmLink, linkPrompt } from '@/composables/useLinkPrompt'
import { splitUrl } from '@/lib/trust'

const open = computed({
  get: () => !!linkPrompt.value,
  set: (v) => { if (!v) cancelLink() },
})
const parts = computed(() => (linkPrompt.value ? splitUrl(linkPrompt.value.url) : null))
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent class="sm:max-w-[480px]">
      <AlertDialogHeader>
        <AlertDialogTitle>Open this link?</AlertDialogTitle>
        <AlertDialogDescription>It opens in your default browser.</AlertDialogDescription>
      </AlertDialogHeader>
      <template v-if="linkPrompt && parts">
        <p class="bg-muted/60 selectable max-h-32 overflow-y-auto rounded-md px-3 py-2 font-mono text-xs leading-relaxed break-all" data-testid="link-url">
          <span class="text-muted-foreground">{{ parts.prefix }}</span><strong class="text-foreground font-semibold">{{ parts.host }}</strong><span class="text-muted-foreground">{{ parts.rest }}</span>
        </p>
        <p v-if="linkPrompt.mismatch" class="flex gap-2 rounded-md bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-300" role="alert">
          <TriangleAlertIcon class="mt-0.5 size-3.5 shrink-0" />
          <span>The link text shows <strong>{{ linkPrompt.textHost }}</strong>, but it goes to <strong>{{ linkPrompt.host }}</strong>. Only continue if you trust the sender.</span>
        </p>
        <p v-else-if="linkPrompt.lookalike" class="flex gap-2 rounded-md bg-amber-500/10 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:text-amber-300" role="alert">
          <TriangleAlertIcon class="mt-0.5 size-3.5 shrink-0" />
          <span>This address uses international characters, which can imitate a familiar site.</span>
        </p>
      </template>
      <AlertDialogFooter class="gap-2 sm:justify-between">
        <Button variant="ghost" @click="cancelLink()">Cancel</Button>
        <div class="flex flex-col-reverse gap-2 sm:flex-row">
          <Button variant="outline" class="max-w-full truncate" @click="confirmLink(true)">Always allow {{ linkPrompt?.host }}</Button>
          <Button autofocus @click="confirmLink(false)"><ExternalLinkIcon /> Open link</Button>
        </div>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
