<script setup lang="ts">
import { ref } from 'vue'
import { DownloadIcon, FileArchiveIcon, FileIcon, FileImageIcon, FileSpreadsheetIcon, FileTextIcon, LoaderIcon, CalendarIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { api, errorMessage } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import type { Attachment } from '@/lib/types'

const props = defineProps<{ messageId: string; attachments: Attachment[] }>()
const saving = ref<string | null>(null)

function iconFor(a: Attachment) {
  const t = a.mimeType
  if (t.startsWith('image/')) return FileImageIcon
  if (t.includes('pdf') || t.startsWith('text/plain')) return FileTextIcon
  if (t.includes('sheet') || t.includes('csv') || t.includes('excel')) return FileSpreadsheetIcon
  if (t.includes('zip') || t.includes('compressed')) return FileArchiveIcon
  if (t.includes('calendar')) return CalendarIcon
  return FileIcon
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
</script>

<template>
  <div class="mt-4 flex flex-wrap gap-2">
    <button
      v-for="a in attachments.filter((x) => !x.inline)"
      :key="a.id"
      type="button"
      class="group border-border hover:bg-muted/60 flex max-w-64 items-center gap-2.5 rounded-lg border px-2.5 py-2 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring"
      :title="`Save ${a.filename}`"
      @click="save(a)"
    >
      <span class="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-md">
        <component :is="iconFor(a)" class="size-4" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block truncate text-xs font-medium">{{ a.filename }}</span>
        <span class="text-muted-foreground block text-[11px]">{{ formatBytes(a.size) }}</span>
      </span>
      <LoaderIcon v-if="saving === a.id" class="text-muted-foreground size-4 animate-spin" />
      <DownloadIcon v-else class="text-muted-foreground size-4 opacity-0 transition-opacity group-hover:opacity-100" />
    </button>
  </div>
</template>
