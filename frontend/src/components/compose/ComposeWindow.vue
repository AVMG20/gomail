<script lang="ts">
/** Caret in the body when a window was minimised, per draft (restored when it reopens). */
const carets = new Map<string, number>()
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  ClipboardPasteIcon, CopyIcon, Maximize2Icon, Minimize2Icon, MinusIcon, PaperclipIcon, ScissorsIcon,
  SendIcon, SparklesIcon, Trash2Icon, XIcon, LoaderIcon, TextSelectIcon,
} from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuTrigger,
} from '@/components/ui/context-menu'
import Tip from '@/components/common/Tip.vue'
import { api, errorMessage } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import { shortcutLabel, uid } from '@/lib/platform'
import { useAccountsStore } from '@/stores/accounts'
import { draftHasContent, sendWarnings, useComposeStore, type Draft } from '@/stores/compose'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import RecipientField from './RecipientField.vue'
import RichEditor from './RichEditor.vue'

const props = defineProps<{ draft: Draft }>()
const compose = useComposeStore()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()

const d = computed(() => props.draft)
const title = computed(() => d.value.subject.trim() || (d.value.mode === 'forward' ? 'Forward' : d.value.mode === 'new' ? 'New message' : 'Reply'))
const fileInput = ref<HTMLInputElement | null>(null)
const toField = ref<InstanceType<typeof RecipientField> | null>(null)
const ccField = ref<InstanceType<typeof RecipientField> | null>(null)
const bccField = ref<InstanceType<typeof RecipientField> | null>(null)

/** Recipients typed but not yet turned into chips would otherwise be left out. */
function commitRecipients() {
  for (const f of [toField, ccField, bccField]) f.value?.commit()
}
const editorRef = ref<InstanceType<typeof RichEditor> | null>(null)
const subjectInput = ref<HTMLInputElement | null>(null)

onMounted(async () => {
  await nextTick()
  const ed = editorRef.value?.editor
  const caret = carets.get(d.value.id)
  // Reopened after Esc: back where the user was typing.
  if (ed && caret !== undefined) ed.commands.focus(Math.min(caret, ed.state.doc.content.size))
  else if (!d.value.to.length) toField.value?.focus()
  else if (d.value.mode === 'new' && !d.value.subject.trim()) subjectInput.value?.focus()
  else ed?.commands.focus('start')
})
onBeforeUnmount(() => {
  const ed = editorRef.value?.editor
  if (ed?.isFocused) carets.set(d.value.id, ed.state.selection.from)
  else carets.delete(d.value.id)
})

function onKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    if (e.defaultPrevented) return // the editor already submitted
    e.preventDefault()
    sendNow()
  } else if (e.key === 'Escape') {
    e.stopPropagation()
    e.preventDefault()
    compose.minimize(d.value.id)
  }
}

/** Attach files (the file picker, drag and drop, pasted files). */
async function attachFiles(files: File[]) {
  for (const f of files) {
    if (totalSize.value + f.size > LIMIT) toast.warning('Attachments now exceed 25 MB', { description: 'The message can’t be sent like this: remove a file or share it with a link.' })
    let dataUrl: string
    try {
      dataUrl = await new Promise<string>((resolve, reject) => {
        const r = new FileReader()
        r.onload = () => resolve(String(r.result))
        r.onerror = () => reject(r.error)
        r.readAsDataURL(f)
      })
    } catch (e) {
      toast.error(`Couldn’t attach ${f.name}`, { description: errorMessage(e) })
      continue
    }
    d.value.attachments.push({ id: uid('att'), filename: f.name || 'pasted-file', mimeType: f.type || 'application/octet-stream', size: f.size, dataBase64: dataUrl.slice(dataUrl.indexOf(',') + 1) })
    compose.resolveMissing(d.value.id, f.name)
  }
}

async function onFiles(e: Event) {
  await attachFiles(Array.from((e.target as HTMLInputElement).files ?? []))
  if (fileInput.value) fileInput.value.value = ''
}

// Drag files onto the window to attach them. dragenter/leave fire per child: count them.
const dragDepth = ref(0)
const hasFiles = (e: DragEvent) => !!e.dataTransfer?.types.includes('Files')
function onDragEnter(e: DragEvent) {
  if (!hasFiles(e)) return
  e.preventDefault()
  dragDepth.value++
}
function onDragOver(e: DragEvent) {
  if (!hasFiles(e)) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
}
function onDragLeave(e: DragEvent) {
  if (hasFiles(e)) dragDepth.value = Math.max(0, dragDepth.value - 1)
}
function onDrop(e: DragEvent) {
  dragDepth.value = 0
  const files = Array.from(e.dataTransfer?.files ?? [])
  if (!files.length) return
  // Files are attached, never dropped into the text as inline content.
  e.preventDefault()
  e.stopPropagation()
  void attachFiles(files)
}

/** Asked before sending: no subject, or "attached" in the text with nothing attached. */
const warnings = ref<ReturnType<typeof sendWarnings>>([])
/** What was last asked: the dialog's text and buttons (closing it clears `warnings`, before the
 *  close animation and its buttons' handlers run). */
const asked = ref<ReturnType<typeof sendWarnings>>([])
function sendNow(force = false) {
  commitRecipients()
  if (d.value.sending || d.value.queued) return
  // What must be fixed is said first: asking about the subject is pointless when an address is wrong.
  if (!compose.canSend(d.value.id)) return
  const w = force ? [] : sendWarnings(d.value)
  if (w.length) {
    warnings.value = w
    asked.value = w
    return
  }
  warnings.value = []
  void compose.send(d.value.id, settings.settings?.undoSendSeconds ?? 0)
}
/** "Attach a file" / "Add a subject": back to the composer, where the fix goes. */
function fixWarning() {
  const attach = asked.value.includes('attachment')
  warnings.value = []
  setTimeout(() => (attach ? fileInput.value?.click() : subjectInput.value?.focus()), 0)
}
const warnOpen = computed({ get: () => warnings.value.length > 0, set: (v) => { if (!v) warnings.value = [] } })

const confirmDiscard = ref(false)
async function closeWindow() {
  commitRecipients()
  if (!(await compose.saveAndClose(d.value.id))) confirmDiscard.value = true
}
function discard() {
  if (draftHasContent(d.value)) confirmDiscard.value = true
  else compose.discard(d.value.id)
}

const totalSize = computed(() => d.value.attachments.reduce((n, a) => n + a.size, 0))
const LIMIT = 25 * 1024 * 1024

// ---------------------------------------------------------------- subject context menu + grammar
const subjSel = ref<{ start: number; end: number }>({ start: 0, end: 0 })
const subjPending = ref<{ start: number; end: number } | null>(null)
const subjArrived = ref(false)

let subjSnapAt = 0
function snapSubject(e: MouseEvent) {
  if (e.button !== 2) return
  const el = subjectInput.value
  if (el) subjSel.value = { start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 }
  subjSnapAt = Date.now()
}
/** Keyboard-opened menu (no right mouse-down first): use the current selection. */
function snapSubjectKey() {
  const el = subjectInput.value
  if (el && Date.now() - subjSnapAt > 1000) subjSel.value = { start: el.selectionStart ?? 0, end: el.selectionEnd ?? 0 }
}
const subjHasSel = computed(() => subjSel.value.end > subjSel.value.start)

async function subjCopy() {
  await api.clipboardSet(d.value.subject.slice(subjSel.value.start, subjSel.value.end))
}
async function subjCut() {
  const { start, end } = subjSel.value
  await api.clipboardSet(d.value.subject.slice(start, end))
  d.value.subject = d.value.subject.slice(0, start) + d.value.subject.slice(end)
}
async function subjPaste() {
  const text = (await api.clipboardGet()).replace(/\s*\n\s*/g, ' ')
  const { start, end } = subjSel.value
  d.value.subject = d.value.subject.slice(0, start) + text + d.value.subject.slice(end)
}
function subjSelectAll() {
  subjectInput.value?.focus()
  subjectInput.value?.select()
}

async function improveSubject() {
  const before = d.value.subject
  if (!before.trim() || subjPending.value) return
  const { start, end } = subjHasSel.value ? subjSel.value : { start: 0, end: before.length }
  subjPending.value = { start, end }
  subjectInput.value?.setSelectionRange(before.length, before.length)
  subjectInput.value?.blur()
  try {
    const result = (await api.improveGrammar(before.slice(start, end))).replace(/\s*\n\s*/g, ' ')
    if (d.value.subject !== before) return // edited meanwhile; drop the result
    if (!result.trim() || result === before.slice(start, end)) {
      toast('Looks good already')
      return
    }
    d.value.subject = before.slice(0, start) + result + before.slice(end)
    subjArrived.value = true
    setTimeout(() => (subjArrived.value = false), 1200)
    toast.success('Subject improved', { action: { label: 'Undo', onClick: () => (d.value.subject = before) } })
  } catch (e) {
    toast.error('Could not improve grammar', { description: errorMessage(e) })
  } finally {
    subjPending.value = null
  }
}

const subjParts = computed(() => {
  const p = subjPending.value
  const s = d.value.subject
  return p ? [s.slice(0, p.start), s.slice(p.start, p.end), s.slice(p.end)] : ['', '', '']
})

const fromAccount = computed(() => accounts.accountMap.get(d.value.accountId))
// Swap the signature when the From account changes.
watch(() => d.value.accountId, (id, prev) => {
  if (id !== prev) editorRef.value?.applySignature(accounts.accountMap.get(id)?.signature ?? '')
})
</script>

<template>
  <div
    :class="[
      'bg-background border-border pointer-events-auto flex flex-col overflow-hidden border shadow-2xl',
      d.expanded ? 'fixed inset-x-[8vw] top-[6vh] bottom-[6vh] z-50 rounded-xl' : 'relative h-[min(560px,calc(100vh-80px))] w-[min(540px,calc(100vw-120px))] rounded-t-xl',
    ]"
    role="dialog"
    :aria-label="title"
    @keydown="onKey"
    @dragenter.capture="onDragEnter"
    @dragover.capture="onDragOver"
    @dragleave.capture="onDragLeave"
    @drop.capture="onDrop"
  >
    <!-- Drop target while files are dragged over the window -->
    <div v-if="dragDepth > 0" class="border-primary bg-background/90 text-primary pointer-events-none absolute inset-2 z-20 flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed text-sm font-medium">
      <PaperclipIcon class="size-5" /> Drop files to attach them
    </div>
    <!-- Title bar -->
    <div class="bg-muted/60 border-border flex h-10 shrink-0 cursor-default items-center gap-1 border-b pr-1.5 pl-4" @dblclick="d.expanded = !d.expanded">
      <span class="min-w-0 flex-1 truncate text-[13px] font-medium">{{ title }}</span>
      <Tip label="Minimize"><Button variant="ghost" size="icon-xs" aria-label="Minimize" @click="compose.minimize(d.id)"><MinusIcon /></Button></Tip>
      <Tip :label="d.expanded ? 'Exit full size' : 'Full size'">
        <Button variant="ghost" size="icon-xs" :aria-label="d.expanded ? 'Exit full size' : 'Full size'" @click="d.expanded = !d.expanded">
          <Minimize2Icon v-if="d.expanded" /><Maximize2Icon v-else />
        </Button>
      </Tip>
      <Tip label="Save & close"><Button variant="ghost" size="icon-xs" aria-label="Save and close" @click="closeWindow"><XIcon /></Button></Tip>
    </div>

    <!-- Header fields -->
    <div class="divide-border/70 shrink-0 divide-y px-4">
      <!-- Replies and forwards go out from the account that received the message (the backend refuses others). -->
      <div v-if="accounts.accounts.length > 1 && d.mode !== 'new'" class="flex h-9 items-center gap-1">
        <span class="text-muted-foreground w-10 shrink-0 text-[13px]">From</span>
        <span v-if="fromAccount" class="flex items-center gap-2 px-1.5 text-[13px]">
          <span class="size-2 rounded-full" :style="{ background: fromAccount.color }" />{{ fromAccount.email }}
        </span>
      </div>
      <div v-else-if="accounts.accounts.length > 1" class="flex h-9 items-center gap-1">
        <span class="text-muted-foreground w-10 shrink-0 text-[13px]">From</span>
        <Select v-model="d.accountId">
          <SelectTrigger size="sm" aria-label="From account" class="h-7 border-0 px-1.5 shadow-none focus-visible:ring-0 dark:bg-transparent">
            <SelectValue>
              <span v-if="fromAccount" class="flex items-center gap-2 text-[13px]">
                <span class="size-2 rounded-full" :style="{ background: fromAccount.color }" />{{ fromAccount.email }}
              </span>
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="a in accounts.accounts" :key="a.id" :value="a.id">
              <span class="size-2 rounded-full" :style="{ background: a.color }" /> {{ a.displayName }} · {{ a.email }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="flex items-start">
        <RecipientField ref="toField" v-model="d.to" label="To" class="flex-1" />
        <button v-if="!d.showCc" type="button" class="text-muted-foreground hover:text-foreground mt-2.5 shrink-0 text-xs" @click="d.showCc = true">Cc Bcc</button>
      </div>
      <template v-if="d.showCc">
        <RecipientField ref="ccField" v-model="d.cc" label="Cc" />
        <RecipientField ref="bccField" v-model="d.bcc" label="Bcc" />
      </template>
      <ContextMenu>
        <ContextMenuTrigger as-child>
          <div class="relative" style="--default-contextmenu: hide" @mousedown.capture="snapSubject" @contextmenu.capture="snapSubjectKey">
            <input
              ref="subjectInput"
              v-model="d.subject"
              type="text"
              placeholder="Subject"
              :readonly="!!subjPending"
              :class="['h-10 w-full bg-transparent text-[13.5px] font-medium outline-none', subjPending && 'text-transparent caret-transparent selection:bg-transparent', subjArrived && 'ai-arrive']"
              aria-label="Subject"
            />
            <!-- Shimmer overlay while grammar runs -->
            <div v-if="subjPending" class="pointer-events-none absolute inset-0 flex items-center overflow-hidden text-[13.5px] font-medium whitespace-pre" aria-hidden="true">
              <span>{{ subjParts[0] }}</span><span class="ai-shimmer">{{ subjParts[1] }}</span><span>{{ subjParts[2] }}</span>
            </div>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent class="w-52">
          <ContextMenuItem :disabled="!subjHasSel" @click="subjCut"><ScissorsIcon /> Cut <ContextMenuShortcut>{{ shortcutLabel('mod+x') }}</ContextMenuShortcut></ContextMenuItem>
          <ContextMenuItem :disabled="!subjHasSel" @click="subjCopy"><CopyIcon /> Copy <ContextMenuShortcut>{{ shortcutLabel('mod+c') }}</ContextMenuShortcut></ContextMenuItem>
          <ContextMenuItem @click="subjPaste"><ClipboardPasteIcon /> Paste <ContextMenuShortcut>{{ shortcutLabel('mod+v') }}</ContextMenuShortcut></ContextMenuItem>
          <ContextMenuItem @click="subjSelectAll"><TextSelectIcon /> Select all</ContextMenuItem>
          <template v-if="settings.grammarReady">
            <ContextMenuSeparator />
            <ContextMenuItem :disabled="!!subjPending || !d.subject.trim()" @click="improveSubject">
              <SparklesIcon class="text-violet-500" /> {{ subjHasSel ? 'Improve grammar of selection' : 'Improve grammar' }}
            </ContextMenuItem>
          </template>
          <template v-else>
            <ContextMenuSeparator />
            <ContextMenuItem @click="ui.openSettings('ai')"><SparklesIcon class="text-muted-foreground" /> Set up AI to improve grammar…</ContextMenuItem>
          </template>
        </ContextMenuContent>
      </ContextMenu>
    </div>
    <div class="border-border/70 mx-4 border-t" />

    <!-- Body -->
    <RichEditor ref="editorRef" v-model="d.html" :version="d.version" @submit="sendNow()" @files="attachFiles">
      <template #start>
        <Button size="sm" class="gap-1.5 px-3" :disabled="d.sending" @click="sendNow()">
          <LoaderIcon v-if="d.sending" class="animate-spin" /><SendIcon v-else />
          Send
        </Button>
        <span class="text-muted-foreground ml-1 hidden text-[11px] sm:inline">{{ d.savedAt ? 'Draft saved' : shortcutLabel('mod+enter') }}</span>
      </template>
      <template #end>
        <Tip label="Attach files">
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Attach files" @click="fileInput?.click()"><PaperclipIcon /></Button>
        </Tip>
        <Tip label="Discard draft">
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label="Discard draft" @click="discard"><Trash2Icon /></Button>
        </Tip>
      </template>
    </RichEditor>
    <input ref="fileInput" type="file" multiple class="hidden" @change="onFiles" />

    <div v-if="d.filesPending || d.missingFiles?.length" class="border-border flex shrink-0 items-center gap-2 border-t bg-amber-500/10 px-4 py-2 text-xs text-amber-800 dark:text-amber-300" role="status">
      <template v-if="d.filesPending">
        <LoaderIcon class="size-3.5 animate-spin" /> Restoring attachments…
      </template>
      <template v-else>
        <span class="min-w-0 flex-1">
          {{ d.missingFiles!.length === 1 ? 'This attachment' : 'These attachments' }} couldn’t be restored after gomail closed: <strong>{{ d.missingFiles!.join(', ') }}</strong>.
          Re-add {{ d.missingFiles!.length === 1 ? 'it' : 'them' }} to send.
        </span>
        <Button variant="ghost" size="xs" class="shrink-0" @click="compose.resolveMissing(d.id)">Continue without them</Button>
      </template>
    </div>
    <div v-if="d.forwarded?.length" class="border-border flex shrink-0 flex-wrap items-center gap-1.5 border-t px-4 py-2" aria-label="Forwarded attachments">
      <span v-for="(a, i) in d.forwarded" :key="i" class="bg-muted/60 text-muted-foreground inline-flex h-7 max-w-56 items-center gap-1.5 rounded-md px-2 text-xs" :title="`${a.filename} is forwarded with the original message`">
        <PaperclipIcon class="size-3.5" />
        <span class="truncate">{{ a.filename }}</span>
        <span>{{ formatBytes(a.size) }}</span>
      </span>
      <span class="text-muted-foreground ml-auto text-[11px]">Forwarded with the original</span>
    </div>
    <div v-if="d.attachments.length" class="border-border flex shrink-0 flex-wrap items-center gap-1.5 border-t px-4 py-2">
      <span v-for="a in d.attachments" :key="a.id" class="bg-muted inline-flex h-7 max-w-56 items-center gap-1.5 rounded-md pr-1 pl-2 text-xs">
        <PaperclipIcon class="text-muted-foreground size-3.5" />
        <span class="truncate">{{ a.filename }}</span>
        <span class="text-muted-foreground">{{ formatBytes(a.size) }}</span>
        <button type="button" class="text-muted-foreground hover:text-foreground rounded p-0.5" :aria-label="`Remove ${a.filename}`" @click="d.attachments = d.attachments.filter((x) => x.id !== a.id)"><XIcon class="size-3" /></button>
      </span>
      <span :class="['ml-auto text-[11px] tabular-nums', totalSize > LIMIT ? 'text-destructive font-medium' : 'text-muted-foreground']">
        {{ d.attachments.length }} file{{ d.attachments.length === 1 ? '' : 's' }} · {{ formatBytes(totalSize) }}<template v-if="totalSize > LIMIT"> · over 25 MB</template>
      </span>
    </div>

    <AlertDialog v-model:open="warnOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ asked.includes('attachment') ? 'Send without an attachment?' : 'Send without a subject?' }}</AlertDialogTitle>
          <AlertDialogDescription>
            <template v-if="asked.includes('attachment')">Your message mentions an attachment, but no file is attached.</template>
            <template v-if="asked.includes('subject')"> {{ asked.includes('attachment') ? 'It also has no subject.' : 'The message has no subject.' }}</template>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel @click="fixWarning">{{ asked.includes('attachment') ? 'Attach a file' : 'Add a subject' }}</AlertDialogCancel>
          <AlertDialogAction @click="sendNow(true)">Send anyway</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <AlertDialog v-model:open="confirmDiscard">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Discard this draft?</AlertDialogTitle>
          <AlertDialogDescription>The message and its attachments will be deleted.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep editing</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive hover:bg-destructive/90 text-white" @click="compose.discard(d.id)">Discard</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
