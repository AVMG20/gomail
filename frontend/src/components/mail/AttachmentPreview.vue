<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { DownloadIcon, LoaderIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { api, errorMessage } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import type { Attachment } from '@/lib/types'

const props = defineProps<{ messageId: string; attachment: Attachment }>()
const emit = defineEmits<{ close: []; save: [] }>()
const open = ref(true)
const url = ref('')
const kind = ref<'image' | 'pdf' | 'text' | ''>('')
const text = ref('')
const error = ref('')

let alive = true
onMounted(async () => {
  try {
    const data = await api.getAttachmentData(props.messageId, props.attachment.id)
    if (!alive) return // closed before the data arrived: don't create a URL nobody revokes
    const mime = data.mimeType || props.attachment.mimeType
    const bytes = Uint8Array.from(atob(data.dataBase64), (c) => c.charCodeAt(0))
    if (/^text\//i.test(mime)) {
      kind.value = 'text'
      text.value = new TextDecoder().decode(bytes).slice(0, 200_000)
      return
    }
    // Blob URLs share the app's origin, so their type is pinned: images only ever
    // go into <img> (no script, even for SVG) and the iframe only ever gets a PDF.
    // Anything else (e.g. text/html posing as an attachment) is never rendered.
    const isImage = /^image\/(png|jpe?g|gif|webp|bmp|avif|svg\+xml)$/i.test(mime)
    const isPdf = mime.toLowerCase() === 'application/pdf'
    if (!isImage && !isPdf) throw new Error('This file type can’t be previewed. Save it instead.')
    url.value = URL.createObjectURL(new Blob([bytes], { type: isPdf ? 'application/pdf' : mime }))
    kind.value = isImage ? 'image' : 'pdf'
  } catch (e) {
    error.value = errorMessage(e)
  }
})
onBeforeUnmount(() => {
  alive = false
  if (url.value) URL.revokeObjectURL(url.value)
})

function onOpen(v: boolean) {
  open.value = v
  if (!v) setTimeout(() => emit('close'), 150)
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpen">
    <DialogContent class="flex h-[min(80vh,820px)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[min(90vw,960px)]">
      <div class="border-border flex items-center gap-3 border-b py-3 pr-14 pl-5">
        <div class="min-w-0 flex-1">
          <DialogTitle class="truncate text-sm">{{ attachment.filename }}</DialogTitle>
          <DialogDescription class="text-xs">{{ formatBytes(attachment.size) }} · {{ attachment.mimeType }}</DialogDescription>
        </div>
        <Button variant="outline" size="sm" @click="emit('save')"><DownloadIcon /> Save</Button>
      </div>
      <div class="bg-muted/40 flex min-h-0 flex-1 items-center justify-center overflow-auto">
        <LoaderIcon v-if="!kind && !error" class="text-muted-foreground size-5 animate-spin" />
        <p v-else-if="error" class="text-muted-foreground p-8 text-center text-sm">Preview not available: {{ error }}</p>
        <img v-else-if="kind === 'image'" :src="url" :alt="attachment.filename" class="max-h-full max-w-full object-contain p-4" />
        <iframe v-else-if="kind === 'pdf'" :src="url" class="size-full border-0 bg-white" title="PDF preview" />
        <pre v-else class="selectable size-full overflow-auto p-5 font-mono text-xs whitespace-pre-wrap">{{ text }}</pre>
      </div>
    </DialogContent>
  </Dialog>
</template>
