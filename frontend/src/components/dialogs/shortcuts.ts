export interface ShortcutDef { keys: string; label: string }
export const SHORTCUT_GROUPS: { title: string; items: ShortcutDef[] }[] = [
  {
    title: 'Navigation',
    items: [
      { keys: 'j', label: 'Next message' }, { keys: 'k', label: 'Previous message' }, { keys: 'enter', label: 'Open message' },
      { keys: 'esc', label: 'Close / clear selection' }, { keys: '/', label: 'Search this mailbox' }, { keys: 'mod+k', label: 'Command palette & global search' },
      { keys: 'mod+1', label: 'Switch to account 1…9' }, { keys: 'mod+shift+0', label: 'All inboxes' }, { keys: 'mod+=', label: 'Larger text' }, { keys: 'mod+-', label: 'Smaller text' }, { keys: 'mod+0', label: 'Reset text size' }, { keys: 'g i', label: 'Go to inbox' },
      { keys: 'g s', label: 'Go to starred' }, { keys: 'g t', label: 'Go to sent' }, { keys: 'g d', label: 'Go to drafts' }, { keys: 'g a', label: 'Go to archive' }, { keys: 'mod+b', label: 'Toggle folders' },
    ],
  },
  {
    title: 'Actions',
    items: [
      { keys: 'e', label: 'Archive (the hovered message, else the open one)' }, { keys: '#', label: 'Delete' }, { keys: '!', label: 'Move to Junk (in Junk: not junk)' }, { keys: 's', label: 'Star / unstar' }, { keys: 'u', label: 'Mark unread' },
      { keys: 'shift+i', label: 'Mark read' }, { keys: 'l', label: 'Labels… (of the selection when mail is selected)' }, { keys: 'x', label: 'Select message' }, { keys: 'mod+a', label: 'Select all loaded' },
    ],
  },
  {
    title: 'Compose',
    items: [
      { keys: 'c', label: 'New message' }, { keys: 'r', label: 'Reply' }, { keys: 'a', label: 'Reply all' }, { keys: 'f', label: 'Forward' },
      { keys: 'mod+enter', label: 'Send' }, { keys: 'mod+k', label: 'Insert link' }, { keys: '?', label: 'Show shortcuts' }, { keys: 'mod+,', label: 'Settings' },
    ],
  },
  {
    title: 'Calendar',
    items: [
      { keys: 'g c', label: 'Open calendar' }, { keys: 'g m', label: 'Back to mail' }, { keys: 'n', label: 'New event (also c)' }, { keys: 't', label: 'Today' },
      { keys: 'd', label: 'Day view' }, { keys: 'w', label: 'Week view' }, { keys: 'm', label: 'Month view' }, { keys: 'a', label: 'Agenda' },
      { keys: 'left', label: 'Previous period' }, { keys: 'right', label: 'Next period' },
      { keys: 'down', label: 'Next event (also j)' }, { keys: 'up', label: 'Previous event (also k)' }, { keys: 'enter', label: 'Open event' },
      { keys: 'mod+b', label: 'Toggle calendar sidebar' },
    ],
  },
  {
    title: 'Event details',
    items: [
      { keys: 'e', label: 'Edit event' }, { keys: 'delete', label: 'Delete event' }, { keys: 'y', label: 'Going' }, { keys: 'm', label: 'Maybe' },
      { keys: 'n', label: 'Not going' }, { keys: 'tab', label: 'Next button' }, { keys: 'esc', label: 'Close' },
    ],
  },
]
