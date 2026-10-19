export interface ShortcutDef { keys: string; label: string }
export const SHORTCUT_GROUPS: { title: string; items: ShortcutDef[] }[] = [
  {
    title: 'Navigation',
    items: [
      { keys: 'j', label: 'Next message' }, { keys: 'k', label: 'Previous message' }, { keys: 'enter', label: 'Open message' },
      { keys: 'esc', label: 'Close / clear selection' }, { keys: '/', label: 'Search this mailbox' }, { keys: 'mod+k', label: 'Command palette & global search' },
      { keys: 'mod+1', label: 'Switch to account 1…9' }, { keys: 'mod+shift+0', label: 'All inboxes' }, { keys: 'mod+=', label: 'Larger text (mod+- smaller, mod+0 reset)' }, { keys: 'g i', label: 'Go to inbox' },
      { keys: 'g s', label: 'Go to starred' }, { keys: 'g t', label: 'Go to sent' }, { keys: 'g d', label: 'Go to drafts' }, { keys: 'mod+b', label: 'Toggle folders' },
    ],
  },
  {
    title: 'Actions',
    items: [
      { keys: 'e', label: 'Archive' }, { keys: '#', label: 'Delete' }, { keys: 's', label: 'Star / unstar' }, { keys: 'u', label: 'Mark unread' },
      { keys: 'shift+i', label: 'Mark read' }, { keys: 'l', label: 'Labels…' }, { keys: 'x', label: 'Select message' }, { keys: 'mod+a', label: 'Select all loaded' },
    ],
  },
  {
    title: 'Compose',
    items: [
      { keys: 'c', label: 'New message' }, { keys: 'r', label: 'Reply' }, { keys: 'a', label: 'Reply all' }, { keys: 'f', label: 'Forward' },
      { keys: 'mod+enter', label: 'Send' }, { keys: '?', label: 'Show shortcuts' }, { keys: 'mod+,', label: 'Settings' },
    ],
  },
]
