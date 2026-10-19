// Tiptap block node for the per-account signature, so it can be swapped when
// the From account changes without touching the rest of the message.
import { mergeAttributes, Node } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import { signatureBlock } from '@/lib/format'

export const Signature = Node.create({
  name: 'signature',
  group: 'block',
  content: 'block+',
  defining: true,
  parseHTML: () => [{ tag: 'div[data-signature]' }],
  renderHTML: ({ HTMLAttributes }) => ['div', mergeAttributes(HTMLAttributes, { 'data-signature': '', class: 'gm-signature' }), 0],
})


/** Replace (or insert / remove) the signature block in the editor. */
export function setSignature(editor: Editor, html: string) {
  const { state } = editor
  let found: { pos: number; size: number } | null = null
  let quotePos: number | null = null
  state.doc.descendants((node, pos) => {
    if (node.type.name === 'signature' && !found) found = { pos, size: node.nodeSize }
    if (node.type.name === 'blockquote' && quotePos === null) quotePos = pos
    return !found
  })
  const block = signatureBlock(html)
  const f = found as { pos: number; size: number } | null
  if (f) {
    if (block) editor.chain().insertContentAt({ from: f.pos, to: f.pos + f.size }, block).run()
    else editor.chain().deleteRange({ from: f.pos, to: f.pos + f.size }).run()
  } else if (block) {
    // Above the quoted text (minus the "On … wrote:" line before it), else at the end.
    let at = state.doc.content.size
    if (quotePos !== null) {
      const $q = state.doc.resolve(quotePos)
      const before = $q.nodeBefore
      at = before && /wrote:|Forwarded message/i.test(before.textContent) ? quotePos - before.nodeSize : quotePos
    }
    editor.chain().insertContentAt(at, block).run()
  }
}
