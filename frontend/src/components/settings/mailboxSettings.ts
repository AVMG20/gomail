// Per-mailbox settings that live in the global Settings object, keyed by account id:
// new-mail notifications (mutedMailboxes), quiet labels (quietLabels), Jev auto-labeling
// (autoLabelAccounts) and AI corrections of imported labels (aiCorrectsOff).
// Shared by the overview lists and each mailbox's own settings page.
import { computed } from 'vue'
import { useSettingsStore } from '@/stores/settings'

const JUNK = 'junk'

export function useMailboxSettings() {
  const settings = useSettingsStore()
  const quiet = computed(() => settings.settings?.quietLabels ?? {})
  const muted = computed(() => new Set(settings.settings?.mutedMailboxes ?? []))
  const autoOn = computed(() => new Set(settings.settings?.autoLabelAccounts ?? []))
  const correctsOff = computed(() => new Set(settings.settings?.aiCorrectsOff ?? []))

  // ---- notifications
  const notifies = (accountId: string) => !muted.value.has(accountId)
  function setNotify(accountId: string, on: boolean) {
    const next = new Set(muted.value)
    if (on) next.delete(accountId)
    else next.add(accountId)
    void settings.update({ mutedMailboxes: [...next] }, 0)
  }

  // ---- quiet labels. Junk never notifies, so it's left out of the editable list.
  /** Labels the user can make quiet in a mailbox: that mailbox's labels, without Junk. */
  const quietOptions = (accountId: string) => settings.labelsForAccount(accountId).filter((l) => l.id !== JUNK)
  /** The mailbox's quiet labels that still exist there, without Junk (shown separately). */
  function quietList(accountId: string): string[] {
    const valid = new Set(quietOptions(accountId).map((l) => l.id))
    return (quiet.value[accountId] ?? []).filter((id) => valid.has(id))
  }
  function setQuiet(accountId: string, labelId: string, on: boolean) {
    const cur = quietList(accountId)
    const next = on ? [...new Set([...cur, labelId])] : cur.filter((l) => l !== labelId)
    void settings.update({ quietLabels: { ...quiet.value, [accountId]: [JUNK, ...next] } }, 300)
  }
  const junkLabel = computed(() => settings.labelMap.get(JUNK) ?? { id: JUNK, name: 'Junk', color: 'var(--muted-foreground)' })

  // ---- Jev auto-labeling: the mailboxes whose new mail is labeled automatically.
  const autoLabels = (accountId: string) => autoOn.value.has(accountId)
  function setAutoLabel(accountId: string, on: boolean) {
    const next = new Set(autoOn.value)
    if (on) next.add(accountId)
    else next.delete(accountId)
    void settings.update({ autoLabelAccounts: [...next] }, 0)
  }

  // ---- AI corrections of imported labels: aiCorrectsProviderLabels is the switch for every
  // mailbox, aiCorrectsOff the mailboxes where the AI leaves imported labels alone.
  const corrects = (accountId: string) => !correctsOff.value.has(accountId)
  function setCorrects(accountId: string, on: boolean) {
    const next = new Set(correctsOff.value)
    if (on) next.delete(accountId)
    else next.add(accountId)
    void settings.update({ aiCorrectsOff: [...next] }, 0)
  }

  return { notifies, setNotify, quietOptions, quietList, setQuiet, junkLabel, autoLabels, setAutoLabel, corrects, setCorrects }
}
