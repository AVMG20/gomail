import { describe, expect, it } from 'vitest'
import { useDragSort } from '@/composables/useDragSort'

function drag(from: number, onto: number, after: boolean) {
  const moves: [number, number][] = []
  const sort = useDragSort((f, t) => moves.push([f, t]))
  const el = { getBoundingClientRect: () => ({ top: 0, height: 10 }) }
  const ev = (y = 0) => ({ preventDefault() {}, dataTransfer: { setData() {} }, currentTarget: el, clientY: y }) as unknown as DragEvent
  sort.bind(from).onDragstart(ev())
  sort.bind(onto).onDragover(ev(after ? 9 : 1))
  const line = sort.lineAt(onto)
  sort.bind(onto).onDrop(ev())
  return { moves, line }
}

describe('useDragSort', () => {
  it('moves down: dropping after item 2 puts item 0 at index 2', () => {
    expect(drag(0, 2, true)).toEqual({ moves: [[0, 2]], line: 'after' })
  })
  it('moves up: dropping before item 0 puts item 3 first', () => {
    expect(drag(3, 0, false)).toEqual({ moves: [[3, 0]], line: 'before' })
  })
  it('dropping next to itself is no move and shows no line', () => {
    expect(drag(1, 1, true)).toEqual({ moves: [], line: null })
    expect(drag(1, 2, false)).toEqual({ moves: [], line: null })
  })
})
