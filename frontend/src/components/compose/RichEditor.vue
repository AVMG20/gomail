<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Placeholder } from '@tiptap/extensions'
import { DOMSerializer, type Node as PMNode } from '@tiptap/pm/model'
import {
  BoldIcon, ItalicIcon, LinkIcon, ListIcon, ListOrderedIcon, QuoteIcon, UnderlineIcon, ScissorsIcon, CopyIcon,
  ClipboardPasteIcon, SparklesIcon, TextSelectIcon, Link2OffIcon,
} from '@lucide/vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { toast } from 'vue-sonner'
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuTrigger,
} from '@/components/ui/context-menu'
import Tip from '@/components/common/Tip.vue'
import { api, errorMessage } from '@/lib/api'
import { htmlToText } from '@/lib/format'
import { INLINE_IMAGE } from '@/lib/inlineImages'
import { shortcutLabel } from '@/lib/platform'
import { useSettingsStore } from '@/stores/settings'
import { AiPending, clearArrived, clearPending, markArrived, pendingRange, setPending } from './aiPending'
import { InlineImage } from './inlineImage'
import { LineBlocks, splitLines } from './lines'
import { Signature, setSignature } from './signature'

const props = defineProps<{
  modelValue: string
  version: number
  placeholder?: string
  /** Stores a dropped or pasted image with the draft; returns the src to put in the text (null: it couldn't be read). */
  addImage?: (file: File) => Promise<string | null>
  /** What the editor shows for an image's src (cid: images: their bytes). */
  resolveImage?: (src: string) => string
}>()
const emit = defineEmits<{ 'update:modelValue': [html: string]; submit: []; files: [files: File[]] }>()
const settings = useSettingsStore()
function openAiSettings() {
  void import('@/stores/ui').then((m) => m.useUiStore().openSettings('ai'))
}

const editor = useEditor({
  content: props.modelValue,
  extensions: [
    StarterKit.configure({
      heading: false, codeBlock: false, code: false, horizontalRule: false,
      link: { openOnClick: false, autolink: true, defaultProtocol: 'https' },
    }),
    Placeholder.configure({ placeholder: props.placeholder ?? 'Write your message…' }),
    AiPending,
    Signature,
    InlineImage.configure({ resolve: (src) => props.resolveImage?.(src) ?? src }),
    LineBlocks,
  ],
  editorProps: {
    attributes: { class: 'compose-editor-content', spellcheck: 'true', role: 'textbox', 'aria-multiline': 'true', 'aria-label': 'Message body' },
    handleKeyDown(_view, e) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
        emit('submit')
        return true
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        e.stopPropagation()
        linkOpen.value = true
        return true
      }
      return false
    },
    // Pasted images (a screenshot, an image copied in the browser) go in the text; other files are
    // attached. Rich text that comes with a picture of itself (Word) pastes as text.
    handlePaste(_view, e) {
      const files = Array.from(e.clipboardData?.files ?? [])
      const html = e.clipboardData?.getData('text/html') ?? ''
      if (!files.length || (html && htmlToText(html).trim())) return false
      void addFiles(files)
      return true
    },
    // Files dropped on the text: images where they are dropped, other files attached.
    handleDrop(view, e) {
      const files = Array.from(e.dataTransfer?.files ?? [])
      if (!files.length) return false
      void addFiles(files, view.posAtCoords({ left: e.clientX, top: e.clientY })?.pos)
      return true
    },
  },
  onUpdate({ editor: ed }) {
    emit('update:modelValue', ed.getHTML())
  },
})

// External content replacement (e.g. reply prefill on reuse).
watch(() => props.version, () => {
  if (editor.value && props.modelValue !== editor.value.getHTML()) editor.value.commands.setContent(props.modelValue, { emitUpdate: false })
})

onBeforeUnmount(() => editor.value?.destroy())

/** Images go in the text (at `pos`, else the caret), other files are attached. */
async function addFiles(files: File[], pos?: number) {
  const images = props.addImage ? files.filter((f) => INLINE_IMAGE.test(f.type)) : []
  const rest = files.filter((f) => !images.includes(f))
  if (rest.length) emit('files', rest)
  if (!images.length) return
  const srcs = (await Promise.all(images.map((f) => props.addImage!(f)))).filter((s): s is string => !!s)
  const ed = editor.value
  if (!ed || ed.isDestroyed || !srcs.length) return
  const at = Math.min(pos ?? ed.state.selection.from, ed.state.doc.content.size)
  ed.chain().focus().insertContentAt(at, srcs.map((src) => ({ type: 'image', attrs: { src } }))).run()
}

/** Plain text as editor content: one paragraph per line (a "\n" in a text node is only a space in mail). */
function textContent(text: string) {
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  if (lines.length === 1) return { type: 'text', text }
  return lines.map((l) => (l ? { type: 'paragraph', content: [{ type: 'text', text: l }] } : { type: 'paragraph' }))
}

/** Quote / lists: only the selected lines, also when they are <br> lines of one paragraph. */
function toggleBlock(kind: 'blockquote' | 'bulletList' | 'orderedList') {
  const c = editor.value?.chain().focus().command(splitLines)
  if (kind === 'blockquote') c?.toggleBlockquote().run()
  else if (kind === 'bulletList') c?.toggleBulletList().run()
  else c?.toggleOrderedList().run()
}

// ---------------------------------------------------------------- link popover
const linkOpen = ref(false)
const linkUrl = ref('')
const linkForm = ref<HTMLFormElement | null>(null)
watch(linkOpen, async (open) => {
  if (!open || !editor.value) return
  linkUrl.value = (editor.value.getAttributes('link').href as string | undefined) ?? ''
  await new Promise((r) => setTimeout(r, 30))
  const input = linkForm.value?.querySelector('input')
  input?.focus()
  input?.select()
})
function applyLink() {
  const ed = editor.value
  if (!ed) return
  let url = linkUrl.value.trim()
  if (!url) return removeLink()
  if (!/^(https?:|mailto:|tel:)/i.test(url)) url = url.includes('@') && !url.includes('/') ? `mailto:${url}` : `https://${url}`
  const { empty } = ed.state.selection
  if (empty && !ed.isActive('link')) {
    // Keep the link apart from the word before it, and don't let typing continue the link.
    const { from } = ed.state.selection
    const before = from > 1 ? ed.state.doc.textBetween(from - 1, from) : ''
    const parts: object[] = []
    if (before && !/\s/.test(before)) parts.push({ type: 'text', text: ' ' })
    parts.push({ type: 'text', text: url, marks: [{ type: 'link', attrs: { href: url } }] }, { type: 'text', text: ' ' })
    ed.chain().focus().insertContent(parts).unsetMark('link').run()
  } else ed.chain().focus().extendMarkRange('link').setLink({ href: url }).run()
  linkOpen.value = false
}
function removeLink() {
  editor.value?.chain().focus().extendMarkRange('link').unsetLink().run()
  linkOpen.value = false
}

// ---------------------------------------------------------------- context menu
// Snapshot the selection on right mouse-down, before WebKit moves it to the word under the cursor.
const snap = ref<{ from: number; to: number } | null>(null)
let snapAt = 0
function onMouseDown(e: MouseEvent) {
  if (e.button !== 2 || !editor.value) return
  const { from, to } = editor.value.state.selection
  snap.value = { from, to }
  snapAt = Date.now()
}
// A menu opened from the keyboard (Menu key, Shift+F10) has no mouse-down before it:
// take the current selection rather than a stale snapshot.
function onContextMenu() {
  if (!editor.value || Date.now() - snapAt < 1000) return
  const { from, to } = editor.value.state.selection
  snap.value = { from, to }
}

function selectedText(): string {
  const ed = editor.value
  if (!ed || !snap.value) return ''
  return ed.state.doc.textBetween(snap.value.from, snap.value.to, '\n')
}

async function copy() {
  await api.clipboardSet(selectedText())
}
async function cut() {
  const ed = editor.value
  if (!ed || !snap.value || snap.value.from === snap.value.to) return
  await api.clipboardSet(selectedText())
  ed.chain().focus().deleteRange(snap.value).run()
}
async function paste() {
  const ed = editor.value
  if (!ed) return
  const text = await api.clipboardGet()
  if (!text) return
  const range = snap.value ?? ed.state.selection
  ed.chain().focus().insertContentAt({ from: range.from, to: range.to }, textContent(text)).run()
}
function selectAll() {
  editor.value?.chain().focus().selectAll().run()
}

const improving = ref(false)

/** A click on the empty area around the text (not in it) puts the caret at the end of the
 *  user's own text, before the signature and quoted message, not after them. */
function onAreaClick(e: MouseEvent) {
  const ed = editor.value
  if (!ed) return
  const target = e.target as Element | null
  const pm = target?.closest?.('.ProseMirror')
  if (pm && pm !== target) return // a click in the text itself: ProseMirror placed the caret
  if (ed.state.selection.from !== ed.state.selection.to) return
  const own = ownTextRange(ed.state.doc)
  if (own && own.to > 0) ed.chain().focus(own.to - 1).run()
  else ed.chain().focus('end').run()
}

/** The text before the signature / quoted message ("On … wrote:" included), or null when there is none. */
function ownTextRange(doc: PMNode): { from: number; to: number } | null {
  let end: number | null = null
  let prev: { node: PMNode; offset: number } | null = null
  doc.forEach((node, offset) => {
    if (end !== null) return
    if (node.type.name === 'signature' || node.type.name === 'blockquote') {
      end = prev && node.type.name === 'blockquote' && /wrote:|Forwarded message/i.test(prev.node.textContent) ? prev.offset : offset
    }
    prev = { node, offset }
  })
  return end === null ? null : { from: 0, to: end }
}

async function improveGrammar() {
  const ed = editor.value
  if (!ed || improving.value) return
  const { state } = ed
  // Without a selection only the user's own text is improved: never the signature or the
  // quoted message (rewriting what someone else wrote would misquote them).
  const picked = snap.value && snap.value.from !== snap.value.to ? snap.value : null
  const sel = picked ?? ownTextRange(state.doc)
  const from = sel ? sel.from : 0
  const to = sel ? sel.to : state.doc.content.size
  if (!state.doc.textBetween(from, to, ' ').trim()) {
    toast('Nothing to improve yet')
    return
  }
  // Send HTML when the range spans formatting or several blocks; plain text otherwise.
  const slice = state.doc.slice(from, to)
  let rich = !picked // whole blocks (own text or everything) go as HTML
  if (sel) {
    let blocks = 0
    slice.content.descendants((node) => {
      if (node.isBlock) blocks++
      if (node.marks.length) rich = true
      return true
    })
    if (blocks > 1 || slice.openStart !== slice.openEnd) rich = true
  }
  let input: string
  if (!sel) input = ed.getHTML()
  else if (rich) {
    const div = document.createElement('div')
    div.appendChild(DOMSerializer.fromSchema(state.schema).serializeFragment(slice.content))
    input = div.innerHTML
  } else input = state.doc.textBetween(from, to, '\n')

  const before = ed.getHTML()
  improving.value = true
  ed.view.dispatch(setPending(ed.state.tr, from, to))
  try {
    const result = await api.improveGrammar(input)
    if (ed.isDestroyed) return // the window was minimised or closed meanwhile
    const range = pendingRange(ed.state) ?? { from, to }
    ed.view.dispatch(clearPending(ed.state.tr))
    if (!result.trim() || result === input) {
      toast('Looks good already', { description: 'No grammar changes suggested.' })
      return
    }
    if (!sel) {
      ed.commands.setContent(result)
      ed.view.dispatch(markArrived(ed.state.tr, 0, ed.state.doc.content.size))
    } else {
      const startSize = ed.state.doc.content.size
      if (rich) ed.chain().insertContentAt(range, result).run()
      else ed.chain().insertContentAt(range, textContent(result)).run()
      const end = range.to + (ed.state.doc.content.size - startSize)
      ed.view.dispatch(markArrived(ed.state.tr, range.from, Math.max(range.from, end)))
    }
    setTimeout(() => editor.value && editor.value.view.dispatch(clearArrived(editor.value.state.tr)), 1300)
    emit('update:modelValue', ed.getHTML())
    const after = ed.getHTML()
    toast.success('Grammar improved', {
      action: {
        label: 'Undo',
        onClick: () => {
          // Restoring the old text would also throw away anything typed since.
          if (ed.getHTML() !== after) return void toast('The message changed since', { description: 'Use Ctrl+Z to step back instead.' })
          ed.commands.setContent(before)
          emit('update:modelValue', ed.getHTML())
        },
      },
    })
  } catch (e) {
    if (!ed.isDestroyed) ed.view.dispatch(clearPending(ed.state.tr))
    toast.error('Could not improve grammar', { description: errorMessage(e) })
  } finally {
    improving.value = false
  }
}

function applySignature(html: string) {
  if (!editor.value) return
  setSignature(editor.value, html)
  emit('update:modelValue', editor.value.getHTML())
}

defineExpose({ editor, improveGrammar, applySignature, addFiles })
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <ContextMenu>
      <ContextMenuTrigger as-child>
        <div class="compose-editor thin-scroll min-h-0 flex-1 overflow-y-auto px-4 py-3" style="--default-contextmenu: hide" @mousedown.capture="onMouseDown" @contextmenu.capture="onContextMenu" @click="onAreaClick">
          <EditorContent :editor="editor" class="h-full" />
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent class="w-52">
        <ContextMenuItem :disabled="!snap || snap.from === snap.to" @click="cut"><ScissorsIcon /> Cut <ContextMenuShortcut>{{ shortcutLabel('mod+x') }}</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem :disabled="!snap || snap.from === snap.to" @click="copy"><CopyIcon /> Copy <ContextMenuShortcut>{{ shortcutLabel('mod+c') }}</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="paste"><ClipboardPasteIcon /> Paste <ContextMenuShortcut>{{ shortcutLabel('mod+v') }}</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuItem @click="selectAll"><TextSelectIcon /> Select all</ContextMenuItem>
        <template v-if="!settings.grammarReady">
          <ContextMenuSeparator />
          <ContextMenuItem @click="openAiSettings"><SparklesIcon class="text-muted-foreground" /> Set up AI to improve grammar…</ContextMenuItem>
        </template>
        <template v-if="settings.grammarReady">
          <ContextMenuSeparator />
          <ContextMenuItem :disabled="improving" @click="improveGrammar">
            <SparklesIcon class="text-violet-500" />
            {{ snap && snap.from !== snap.to ? 'Improve grammar of selection' : 'Improve grammar' }}
          </ContextMenuItem>
        </template>
      </ContextMenuContent>
    </ContextMenu>

    <div v-if="editor" class="flex items-center gap-0.5 px-3 pb-1">
      <slot name="start" />
      <div class="bg-border mx-1 h-4 w-px" />
      <Tip label="Bold" shortcut="mod+b">
        <button type="button" :class="['tb', editor?.isActive('bold') && 'tb-on']" aria-label="Bold" @click="editor?.chain().focus().toggleBold().run()"><BoldIcon class="size-4" /></button>
      </Tip>
      <Tip label="Italic" shortcut="mod+i">
        <button type="button" :class="['tb', editor?.isActive('italic') && 'tb-on']" aria-label="Italic" @click="editor?.chain().focus().toggleItalic().run()"><ItalicIcon class="size-4" /></button>
      </Tip>
      <Tip label="Underline" shortcut="mod+u">
        <button type="button" :class="['tb', editor?.isActive('underline') && 'tb-on']" aria-label="Underline" @click="editor?.chain().focus().toggleUnderline().run()"><UnderlineIcon class="size-4" /></button>
      </Tip>
      <Popover v-model:open="linkOpen">
        <PopoverTrigger as-child>
          <Tip label="Link" shortcut="mod+k">
            <button type="button" :class="['tb', editor?.isActive('link') && 'tb-on']" aria-label="Insert link"><LinkIcon class="size-4" /></button>
          </Tip>
        </PopoverTrigger>
        <PopoverContent side="top" align="center" :collision-padding="12" class="w-80 gap-2 p-2">
          <form ref="linkForm" class="flex items-center gap-1.5" @submit.prevent="applyLink">
            <Input v-model="linkUrl" placeholder="https://example.com" class="h-8 text-[13px]" aria-label="Link URL" />
            <Button type="submit" size="sm" class="h-8">Apply</Button>
          </form>
          <button v-if="editor?.isActive('link')" type="button" class="text-muted-foreground hover:text-destructive inline-flex items-center gap-1.5 self-start px-1 text-xs" @click="removeLink"><Link2OffIcon class="size-3.5" /> Remove link</button>
        </PopoverContent>
      </Popover>
      <Tip label="Bulleted list">
        <button type="button" :class="['tb', editor?.isActive('bulletList') && 'tb-on']" aria-label="Bulleted list" @click="toggleBlock('bulletList')"><ListIcon class="size-4" /></button>
      </Tip>
      <Tip label="Numbered list">
        <button type="button" :class="['tb', editor?.isActive('orderedList') && 'tb-on']" aria-label="Numbered list" @click="toggleBlock('orderedList')"><ListOrderedIcon class="size-4" /></button>
      </Tip>
      <Tip label="Quote">
        <button type="button" :class="['tb', editor?.isActive('blockquote') && 'tb-on']" aria-label="Quote" @click="toggleBlock('blockquote')"><QuoteIcon class="size-4" /></button>
      </Tip>
      <div class="flex-1" />
      <slot name="end" />
    </div>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.tb {
  @apply text-muted-foreground hover:text-foreground hover:bg-muted flex size-7 items-center justify-center rounded-md transition-colors;
}
.tb-on {
  @apply bg-muted text-foreground;
}
</style>
