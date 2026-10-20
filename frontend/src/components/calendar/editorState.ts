// Which event editor / popover is open (shared by the calendar view, its popovers and the palette).
import { shallowRef } from 'vue'
import type { CalendarEvent, EventInput } from '@/lib/types'

export interface EditorRequest {
  /** Editing an existing event (occurrence for recurring events). */
  event?: CalendarEvent
  /** Prefill for a new event. */
  draft?: Partial<EventInput>
}

export const editorRequest = shallowRef<EditorRequest | null>(null)

export function openEditor(req: EditorRequest) {
  editorRequest.value = req
}

export function closeEditor() {
  editorRequest.value = null
}
