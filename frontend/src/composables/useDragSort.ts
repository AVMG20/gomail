import { ref } from 'vue'

/**
 * Drag-to-reorder for a vertical list with native drag and drop. bind(i) goes on each item;
 * the drop line shows where the item lands (overIndex/overAfter). onMove gets the item's old
 * index and its new index in the list without it.
 */
export function useDragSort(onMove: (from: number, to: number) => void) {
  const dragIndex = ref<number | null>(null)
  const over = ref<{ index: number; after: boolean } | null>(null)

  function reset() {
    dragIndex.value = null
    over.value = null
  }

  function bind(i: number) {
    return {
      draggable: true,
      onDragstart(e: DragEvent) {
        dragIndex.value = i
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = 'move'
          e.dataTransfer.setData('text/plain', String(i)) // WebKit needs data to start a drag
        }
      },
      onDragover(e: DragEvent) {
        if (dragIndex.value === null) return
        e.preventDefault()
        if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
        const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
        over.value = { index: i, after: e.clientY > r.top + r.height / 2 }
      },
      onDrop(e: DragEvent) {
        e.preventDefault()
        const from = dragIndex.value
        const o = over.value
        reset()
        if (from === null || !o) return
        let to = o.after ? o.index + 1 : o.index
        if (from < to) to--
        if (to !== from) onMove(from, to)
      },
      onDragend: reset,
    }
  }

  /** Where the drop line goes for item i: 'before', 'after' or null. */
  function lineAt(i: number): 'before' | 'after' | null {
    const o = over.value
    if (!o || dragIndex.value === null || o.index !== i) return null
    const to = o.after ? i + 1 : i
    if (to === dragIndex.value || to === dragIndex.value + 1) return null // no move
    return o.after ? 'after' : 'before'
  }

  return { bind, lineAt, dragIndex }
}
