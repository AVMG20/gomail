<script setup lang="ts">
// Catches render/setup errors in a subtree and shows a recoverable fallback
// instead of an empty pane. The global handler still gets a toast via reportError.
import { onErrorCaptured, ref, watch } from 'vue'
import { TriangleAlertIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { copyText, errorDetails, reportError } from '@/lib/errors'

/** resetKey: clear the error when it changes (e.g. another message or tab is opened). */
const props = defineProps<{ name: string; resetKey?: string | number }>()
const failed = ref<unknown>(null)
const attempt = ref(0)

onErrorCaptured((err, _instance, info) => {
  failed.value = err
  reportError(err, `${props.name} (${info})`)
  return false
})

watch(() => props.resetKey, () => (failed.value = null))

function retry() {
  failed.value = null
  attempt.value++
}
</script>

<template>
  <div v-if="failed" role="alert" class="flex h-full flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
    <div class="bg-destructive/10 text-destructive flex size-12 items-center justify-center rounded-2xl"><TriangleAlertIcon class="size-5" /></div>
    <div>
      <p class="text-sm font-medium">The {{ name }} ran into a problem</p>
      <p class="text-muted-foreground mt-1 max-w-sm text-xs">{{ failed instanceof Error ? failed.message : String(failed) }}</p>
    </div>
    <div class="flex gap-2">
      <Button size="sm" variant="outline" @click="copyText(errorDetails(failed, name))">Copy details</Button>
      <Button size="sm" @click="retry">Try again</Button>
    </div>
  </div>
  <div v-else :key="attempt" class="contents"><slot /></div>
</template>
