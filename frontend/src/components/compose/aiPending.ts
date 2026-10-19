// Tiptap extension that marks a range as "AI working on it": an inline
// shimmer decoration, the range is read-only while pending, and a short
// highlight decoration when the new text arrives.
import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import type { EditorState, Transaction } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'

export const aiPendingKey = new PluginKey<AiState>('aiPending')

interface AiState {
  pending: DecorationSet
  arrived: DecorationSet
}

type Meta =
  | { type: 'pending'; from: number; to: number }
  | { type: 'clear' }
  | { type: 'arrived'; from: number; to: number }
  | { type: 'clearArrived' }

export function pendingRange(state: EditorState): { from: number; to: number } | null {
  const set = aiPendingKey.getState(state)?.pending
  const found = set?.find()
  if (!found?.length) return null
  return { from: Math.min(...found.map((d) => d.from)), to: Math.max(...found.map((d) => d.to)) }
}

export function setPending(tr: Transaction, from: number, to: number) {
  return tr.setMeta(aiPendingKey, { type: 'pending', from, to } satisfies Meta)
}
export function clearPending(tr: Transaction) {
  return tr.setMeta(aiPendingKey, { type: 'clear' } satisfies Meta)
}
export function markArrived(tr: Transaction, from: number, to: number) {
  return tr.setMeta(aiPendingKey, { type: 'arrived', from, to } satisfies Meta)
}
export function clearArrived(tr: Transaction) {
  return tr.setMeta(aiPendingKey, { type: 'clearArrived' } satisfies Meta)
}

export const AiPending = Extension.create({
  name: 'aiPending',
  addProseMirrorPlugins() {
    return [
      new Plugin<AiState>({
        key: aiPendingKey,
        state: {
          init: () => ({ pending: DecorationSet.empty, arrived: DecorationSet.empty }),
          apply(tr, value) {
            let pending = value.pending.map(tr.mapping, tr.doc)
            let arrived = value.arrived.map(tr.mapping, tr.doc)
            const meta = tr.getMeta(aiPendingKey) as Meta | undefined
            if (meta?.type === 'pending') {
              const from = Math.max(0, meta.from)
              const to = Math.min(tr.doc.content.size, meta.to)
              pending = DecorationSet.create(tr.doc, [Decoration.inline(from, to, { class: 'ai-shimmer' }, { inclusiveStart: false, inclusiveEnd: false })])
            } else if (meta?.type === 'clear') {
              pending = DecorationSet.empty
            } else if (meta?.type === 'arrived') {
              arrived = DecorationSet.create(tr.doc, [Decoration.inline(meta.from, meta.to, { class: 'ai-arrive' })])
            } else if (meta?.type === 'clearArrived') {
              arrived = DecorationSet.empty
            }
            return { pending, arrived }
          },
        },
        // Block edits that touch the pending range (the rest of the doc stays editable).
        filterTransaction(tr, state) {
          if (!tr.docChanged || tr.getMeta(aiPendingKey)) return true
          const range = pendingRange(state)
          if (!range) return true
          let ok = true
          tr.mapping.maps.forEach((map) => {
            map.forEach((oldStart, oldEnd) => {
              if (oldStart < range.to && oldEnd > range.from) ok = false
            })
          })
          return ok
        },
        props: {
          decorations(state) {
            const s = aiPendingKey.getState(state)
            if (!s) return null
            return s.pending.add(state.doc, s.arrived.find())
          },
        },
      }),
    ]
  },
})
