<script setup lang="ts">
import { onBeforeUnmount, watch } from 'vue'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Placeholder } from '@tiptap/extensions'
import { BoldIcon, ItalicIcon, LinkIcon } from '@lucide/vue'

const props = defineProps<{ modelValue: string; autofocus?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [html: string] }>()

const editor = useEditor({
  content: props.modelValue,
  // Opened from "Add signature": take focus so typing (incl. space) goes into the editor, not the toggle button.
  autofocus: props.autofocus ? 'end' : false,
  extensions: [
    StarterKit.configure({ heading: false, codeBlock: false, code: false, horizontalRule: false, blockquote: false, link: { openOnClick: false, autolink: true } }),
    Placeholder.configure({ placeholder: 'Add a signature…' }),
  ],
  editorProps: { attributes: { class: 'compose-editor-content', 'aria-label': 'Signature' } },
  onUpdate: ({ editor: ed }) => emit('update:modelValue', ed.isEmpty ? '' : ed.getHTML()),
})

watch(() => props.modelValue, (v) => {
  if (editor.value && v !== editor.value.getHTML() && !(v === '' && editor.value.isEmpty)) editor.value.commands.setContent(v, { emitUpdate: false })
})
onBeforeUnmount(() => editor.value?.destroy())

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
    <div v-if="editor" class="border-border flex items-center gap-0.5 border-b px-1.5 py-1">
      <button type="button" :class="['tb', editor.isActive('bold') && 'tb-on']" aria-label="Bold" @click="editor.chain().focus().toggleBold().run()"><BoldIcon class="size-3.5" /></button>
      <button type="button" :class="['tb', editor.isActive('italic') && 'tb-on']" aria-label="Italic" @click="editor.chain().focus().toggleItalic().run()"><ItalicIcon class="size-3.5" /></button>
      <button type="button" :class="['tb', editor.isActive('link') && 'tb-on']" aria-label="Link selected text" title="Select text, then click to link it" @click="toggleLink"><LinkIcon class="size-3.5" /></button>
    </div>
    <EditorContent :editor="editor" class="compose-editor max-h-40 min-h-16 overflow-y-auto px-3 py-2 text-[13px]" />
  </div>
</template>

<style scoped>
@reference "../../style.css";
.tb { @apply text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 items-center justify-center rounded transition-colors; }
.tb-on { @apply bg-muted text-foreground; }
</style>
