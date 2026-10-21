// Tiptap node for the per-account signature, so it can be swapped when the From
// account changes without touching the rest of the message. It is an atom that
// keeps the signature's HTML verbatim: custom signatures (tables, images, inline
// styles) would otherwise be flattened by the editor's schema. Edit it in
// Settings → the mailbox's page.
import { Node } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import DOMPurify from 'dompurify'
import { signatureBlock } from '@/lib/format'

/** Signatures are the user's own HTML, but never let pasted markup run script in the app. */
export function cleanSignature(html: string): string {
  return DOMPurify.sanitize(html, { FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe'], FORBID_ATTR: ['id'] })
}

/** Markup the rich editor can't hold (it would flatten it): such signatures are kept as HTML. */
export const RICH_ONLY = /<(table|img|span|font|div|style|hr|h\d|pre)\b|\sstyle=/i

/** Pasted markup the rich editor would drop outright (styled text alone just loses its styling). */
const PASTE_KEEP = /<(table|img|font|hr)\b/i

/**
 * The signature in a copied page fragment (text/html on the clipboard), or "" when it has no
 * markup the rich editor would drop. The browser's <meta> wrapper and StartFragment comments
 * don't survive sanitizing.
 */
export function pastedSignature(clipboardHtml: string): string {
  const html = PASTE_KEEP.test(clipboardHtml) ? cleanSignature(clipboardHtml).trim() : ''
  return PASTE_KEEP.test(html) ? html : ''
}

function signatureDom(html: string): HTMLElement {
  const el = document.createElement('div')
  el.setAttribute('data-signature', '')
  el.className = 'gm-signature'
  el.innerHTML = cleanSignature(html)
  return el
}

export const Signature = Node.create({
  name: 'signature',
  group: 'block',
  atom: true,
  selectable: true,
  draggable: false,
  addAttributes: () => ({ html: { default: '' } }),
  parseHTML: () => [{ tag: 'div[data-signature]', getAttrs: (el) => ({ html: (el as HTMLElement).innerHTML }) }],
  renderHTML: ({ node }) => signatureDom(node.attrs.html as string),
  addNodeView: () => ({ node }) => {
    const dom = signatureDom(node.attrs.html as string)
    dom.contentEditable = 'false'
    dom.title = 'Signature · edit it in Settings → Mailboxes'
    return { dom }
  },
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
