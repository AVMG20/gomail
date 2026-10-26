// Lines of one paragraph separated by hard breaks (<br>): text that arrived that way (a plain-text
// draft, pasted mail, Shift+Enter). Quote and the lists act on whole blocks, so they would wrap
// every line of the paragraph; splitLines first makes the selected lines paragraphs of their own.
import { Extension, type CommandProps } from '@tiptap/core'
import { TextSelection } from '@tiptap/pm/state'

/** Command (use it in a chain before toggleBlockquote / toggleBulletList): the selected lines (the
 *  caret's line when nothing is selected) become their own paragraphs. */
export function splitLines({ tr, dispatch }: CommandProps): boolean {
  const { from, to } = tr.selection
  if (!(tr.selection instanceof TextSelection)) return true
  // A selection ending right after a break (a whole line) ends on that line.
  const end = to > from && tr.doc.resolve(to).nodeBefore?.type.name === 'hardBreak' ? to - 1 : to
  const breaks: number[] = []
  tr.doc.nodesBetween(from, Math.max(from, end), (node, pos) => {
    if (!node.isTextblock) return true
    const inner: number[] = []
    node.forEach((child, offset) => {
      if (child.type.name === 'hardBreak') inner.push(pos + 1 + offset)
    })
    const before = inner.filter((b) => b < from).pop()
    const after = inner.find((b) => b >= end)
    for (const b of inner) if (b === before || b === after || (b >= from && b < end)) breaks.push(b)
    return false
  })
  if (!breaks.length || !dispatch) return true
  const steps = tr.steps.length
  for (const b of [...breaks].sort((x, y) => y - x)) tr.delete(b, b + 1).split(b)
  // A position right before a break stays on its line; one after it moves to the new paragraph.
  const map = (pos: number) => tr.mapping.slice(steps).map(pos, breaks.includes(pos) ? -1 : 1)
  const a = map(from)
  tr.setSelection(TextSelection.create(tr.doc, a, Math.max(a, map(end))))
  return true
}

/** The quote / list shortcuts with splitLines first (like the toolbar buttons). */
export const LineBlocks = Extension.create({
  name: 'lineBlocks',
  priority: 110, // before the blockquote / list extensions' own shortcuts
  addKeyboardShortcuts() {
    return {
      'Mod-Shift-b': () => this.editor.chain().command(splitLines).toggleBlockquote().run(),
      'Mod-Shift-8': () => this.editor.chain().command(splitLines).toggleBulletList().run(),
      'Mod-Shift-7': () => this.editor.chain().command(splitLines).toggleOrderedList().run(),
    }
  },
})
