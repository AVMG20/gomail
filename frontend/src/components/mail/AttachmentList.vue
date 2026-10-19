<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { DownloadIcon, FileArchiveIcon, FileIcon, FileImageIcon, FileSpreadsheetIcon, FileTextIcon, LoaderIcon, CalendarIcon, FileVideoIcon, FileAudioIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import Tip from '@/components/common/Tip.vue'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import type { Attachment } from '@/lib/types'

const AttachmentPreview = defineAsyncComponent(() => import('./AttachmentPreview.vue'))

const props = defineProps<{ messageId: string; attachments: Attachment[] }>()
const saving = ref<string | null>(null)
const preview = ref<Attachment | null>(null)
const canPreview = computed(() => hasMethod('GetAttachmentData'))

function iconFor(a: Attachment) {
  const t = a.mimeType
  if (t.startsWith('image/')) return FileImageIcon
  if (t.startsWith('video/')) return FileVideoIcon
  if (t.startsWith('audio/')) return FileAudioIcon
  if (t.includes('pdf') || t.startsWith('text/plain') || t.includes('word')) return FileTextIcon
  if (t.includes('sheet') || t.includes('csv') || t.includes('excel')) return FileSpreadsheetIcon
  if (t.includes('zip') || t.includes('compressed') || t.includes('tar')) return FileArchiveIcon
  if (t.includes('calendar')) return CalendarIcon
  return FileIcon
}

function previewable(a: Attachment) {
  return canPreview.value && (a.mimeType.startsWith('image/') || a.mimeType === 'application/pdf' || a.mimeType.startsWith('text/'))
}

async function save(a: Attachment) {
  saving.value = a.id
  try {
    await api.saveAttachment(props.messageId, a.id)
    toast.success(`Saved ${a.filename}`)
  } catch (e) {
    const msg = errorMessage(e)
    if (!/cancel/i.test(msg)) toast.error(`Could not save ${a.filename}`, { description: msg })
  } finally {
    saving.value = null
  }
}

function open(a: Attachment) {
  if (previewable(a)) preview.value = a
  else void save(a)
}

const visible = computed(() => props.attachments.filter((x) => !x.inline))
const total = computed(() => visible.value.reduce((n, a) => n + a.size, 0))
</script>

<template>
  <div class="mt-5">
    <div v-if="visible.length > 1" class="text-muted-foreground mb-2 text-xs">{{ visible.length }} attachments · {{ formatBytes(total) }}</div>
    <div class="flex flex-wrap gap-2">
      <div
        v-for="a in visible"
        :key="a.id"
        class="group border-border hover:bg-muted/60 flex max-w-64 items-center gap-2.5 rounded-lg border py-2 pr-1.5 pl-2.5 transition-colors"
      >
        <button type="button" class="flex min-w-0 flex-1 items-center gap-2.5 text-left outline-none focus-visible:underline" :aria-label="previewable(a) ? `Preview ${a.filename}` : `Save ${a.filename}`" @click="open(a)">
          <span class="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-md">
            <component :is="iconFor(a)" class="size-4" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-xs font-medium">{{ a.filename }}</span>
            <span class="text-muted-foreground block text-[11px]">{{ formatBytes(a.size) }}<template v-if="previewable(a)"> · Preview</template></span>
          </span>
        </button>
        <Tip :label="`Save ${a.filename}`">
          <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-background flex size-7 shrink-0 items-center justify-center rounded-md opacity-60 group-hover:opacity-100" :aria-label="`Save ${a.filename}`" @click="save(a)">
            <LoaderIcon v-if="saving === a.id" class="size-4 animate-spin" />
            <DownloadIcon v-else class="size-4" />
          </button>
        </Tip>
      </div>
    </div>
    <AttachmentPreview v-if="preview" :message-id="messageId" :attachment="preview" @close="preview = null" @save="save(preview!)" />
  </div>
</template>
