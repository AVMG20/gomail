<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Placeholder } from '@tiptap/extensions'
import { BoldIcon, CodeIcon, ItalicIcon, LinkIcon } from '@lucide/vue'
import { cleanSignature, pastedSignature, RICH_ONLY } from '@/components/compose/signature'

const props = defineProps<{ modelValue: string; autofocus?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [html: string] }>()

// Signatures with markup the rich editor can't hold open in HTML mode.
const mode = ref<'rich' | 'html'>(RICH_ONLY.test(props.modelValue) ? 'html' : 'rich')
const source = ref(props.modelValue)
const preview = computed(() => cleanSignature(source.value))
function onSource(v: string) {
  source.value = v
  emit('update:modelValue', v.trim() ? v : '')
}
/** A signature copied from web mail (tables, images) switches to HTML mode instead of being flattened. */
function keepPasted(html: string) {
  mode.value = 'html'
  onSource(html)
}
function setMode(m: 'rich' | 'html') {
  if (m === mode.value) return
  if (m === 'html') source.value = props.modelValue
  else editor.value?.commands.setContent(props.modelValue, { emitUpdate: false })
  mode.value = m
}

const editor = useEditor({
  content: props.modelValue,
  // Opened from "Add signature": take focus so typing (incl. space) goes into the editor, not the toggle button.
  autofocus: props.autofocus ? 'end' : false,
  extensions: [
    StarterKit.configure({ heading: false, codeBlock: false, code: false, horizontalRule: false, blockquote: false, link: { openOnClick: false, autolink: true } }),
    Placeholder.configure({ placeholder: 'Add a signature…' }),
  ],
  editorProps: {
    attributes: { class: 'compose-editor-content', 'aria-label': 'Signature' },
    handlePaste: (_view, event) => {
      const html = pastedSignature(event.clipboardData?.getData('text/html') ?? '')
      const ed = editor.value
      if (!html || !ed) return false
      const { from, to } = ed.state.selection
      const replaceAll = ed.isEmpty || (from <= 1 && to >= ed.state.doc.content.size - 1)
      keepPasted(replaceAll ? html : ed.getHTML() + html)
      return true
    },
  },
  onUpdate: ({ editor: ed }) => emit('update:modelValue', ed.isEmpty ? '' : ed.getHTML()),
})

watch(() => props.modelValue, (v) => {
  if (mode.value === 'html') return
  if (editor.value && v !== editor.value.getHTML() && !(v === '' && editor.value.isEmpty)) editor.value.commands.setContent(v, { emitUpdate: false })
})
onBeforeUnmount(() => editor.value?.destroy())

/** Pasting a copied (rendered) signature into the HTML box inserts its markup, not its plain text. */
function onSourcePaste(e: ClipboardEvent) {
  const html = pastedSignature(e.clipboardData?.getData('text/html') ?? '')
  if (!html || /^\s*</.test(e.clipboardData?.getData('text/plain') ?? '')) return
  e.preventDefault()
  const ta = e.target as HTMLTextAreaElement
  keepPasted(ta.value.slice(0, ta.selectionStart) + html + ta.value.slice(ta.selectionEnd))
}

function toggleLink() {
  const ed = editor.value
  if (!ed) return
  if (ed.isActive('link')) ed.chain().focus().unsetLink().run()
  else {
    const { from, to } = ed.state.selection
    const text = ed.state.doc.textBetween(from, to).trim()
    const href = /^https?:\/\//.test(text) ? text : /\S+@\S+/.test(text) ? `mailto:${text}` : text ? `https://${text}` : ''
    if (href) ed.chain().focus().setLink({ href }).run()
  }
}
</script>

<template>
  <div class="border-input focus-within:ring-ring/40 focus-within:border-ring rounded-md border focus-within:ring-2">
    <div class="border-border flex items-center gap-0.5 border-b px-1.5 py-1">
      <template v-if="editor && mode === 'rich'">
        <button type="button" :class="['tb', editor.isActive('bold') && 'tb-on']" aria-label="Bold" @click="editor.chain().focus().toggleBold().run()"><BoldIcon class="size-3.5" /></button>
        <button type="button" :class="['tb', editor.isActive('italic') && 'tb-on']" aria-label="Italic" @click="editor.chain().focus().toggleItalic().run()"><ItalicIcon class="size-3.5" /></button>
        <button type="button" :class="['tb', editor.isActive('link') && 'tb-on']" aria-label="Link selected text" title="Select text, then click to link it" @click="toggleLink"><LinkIcon class="size-3.5" /></button>
      </template>
      <span v-else class="text-muted-foreground px-1 text-[11px]">Paste a copied signature or HTML: images, tables and inline styles are kept.</span>
      <span class="flex-1" />
      <div class="bg-muted inline-flex rounded p-0.5" role="radiogroup" aria-label="Signature editor">
        <button v-for="o in [{ v: 'rich', l: 'Editor' }, { v: 'html', l: 'HTML' }] as const" :key="o.v" type="button" role="radio" :aria-checked="mode === o.v"
          :class="['flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium', mode === o.v ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']"
          @click="setMode(o.v)"><CodeIcon v-if="o.v === 'html'" class="size-3" />{{ o.l }}</button>
      </div>
    </div>
    <EditorContent v-show="mode === 'rich'" :editor="editor" class="compose-editor max-h-40 min-h-16 overflow-y-auto px-3 py-2 text-[13px]" />
    <template v-if="mode === 'html'">
      <textarea
        :value="source"
        spellcheck="false"
        aria-label="Signature HTML"
        placeholder="<p>Alex Morgan<br><img src=&quot;https://…/logo.png&quot; width=&quot;120&quot;></p>"
        class="bg-transparent block min-h-24 w-full resize-y px-3 py-2 font-mono text-[12px] outline-none"
        @input="onSource(($event.target as HTMLTextAreaElement).value)"
        @paste="onSourcePaste"
      />
      <div v-if="source.trim()" class="border-border border-t px-3 py-2">
        <div class="text-muted-foreground mb-1 text-[10.5px] font-medium tracking-wide uppercase">Preview</div>
        <!-- eslint-disable-next-line vue/no-v-html -- sanitized by cleanSignature -->
        <div class="gm-signature-preview max-h-48 overflow-auto text-[13px]" v-html="preview" />
      </div>
    </template>
  </div>
</template>

<style scoped>
@reference "../../style.css";
.tb { @apply text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded transition-colors; }
.tb-on { @apply bg-muted text-foreground; }
.gm-signature-preview :deep(img) { max-width: 100%; height: auto; }
.gm-signature-preview :deep(a) { color: var(--primary); text-decoration: underline; }
</style>
