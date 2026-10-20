// "This event / All events" prompt for recurring events, as a promise.
import { shallowRef } from 'vue'
import type { RecurrenceScope } from '@/lib/types'

export type ScopeAction = 'edit' | 'move' | 'delete'
interface Pending { action: ScopeAction; title: string; resolve: (s: RecurrenceScope | null) => void }

export const scopeRequest = shallowRef<Pending | null>(null)

export function askScope(action: ScopeAction, title: string): Promise<RecurrenceScope | null> {
  scopeRequest.value?.resolve(null)
  return new Promise((resolve) => {
    scopeRequest.value = { action, title, resolve }
  })
}

export function answerScope(s: RecurrenceScope | null) {
  const p = scopeRequest.value
  scopeRequest.value = null
  p?.resolve(s)
}
