const nav = typeof navigator !== 'undefined' ? navigator : undefined
const platformString = `${(nav as { userAgentData?: { platform?: string } } | undefined)?.userAgentData?.platform ?? ''} ${nav?.platform ?? ''} ${nav?.userAgent ?? ''}`

export const isMac = /Mac|iPhone|iPad|iPod/i.test(platformString)
export const isWindows = /Win/i.test(platformString)

/** Label for the primary modifier key. */
export const modLabel = isMac ? '⌘' : 'Ctrl'

const KEY_LABELS: Record<string, string> = isMac
  ? { mod: '⌘', shift: '⇧', alt: '⌥', enter: '↩', backspace: '⌫', delete: '⌦', esc: 'Esc', tab: '⇥', up: '↑', down: '↓', left: '←', right: '→' }
  : { mod: 'Ctrl', shift: 'Shift', alt: 'Alt', enter: 'Enter', backspace: 'Backspace', delete: 'Del', esc: 'Esc', tab: 'Tab', up: '↑', down: '↓', left: '←', right: '→' }

/** Split a shortcut like "mod+shift+k" into display key caps. */
export function shortcutKeys(combo: string): string[] {
  return combo.split('+').map((k) => KEY_LABELS[k.toLowerCase()] ?? (k.length === 1 ? k.toUpperCase() : k))
}

/** One-string label, e.g. "⌘K" on macOS and "Ctrl+K" elsewhere. */
export function shortcutLabel(combo: string): string {
  return shortcutKeys(combo).join(isMac ? '' : '+')
}

/** True when the event carries the platform's primary modifier. */
export function hasMod(e: KeyboardEvent | MouseEvent): boolean {
  return isMac ? e.metaKey : e.ctrlKey
}

export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  if (tag === 'INPUT') {
    const type = (el as HTMLInputElement).type
    return !['checkbox', 'radio', 'button', 'submit', 'range', 'color'].includes(type)
  }
  return tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

let idCounter = 0
/** Small unique id; avoids crypto.randomUUID which needs a secure context. */
export function uid(prefix = 'id'): string {
  idCounter += 1
  return `${prefix}_${Date.now().toString(36)}${idCounter.toString(36)}${Math.random().toString(36).slice(2, 7)}`
}
