// Per-mailbox settings that live in the global Settings object, keyed by account id:
// new-mail notifications (mutedMailboxes: a muted mailbox also shows no unread count), quiet
// labels (quietLabels), Jev auto-labeling (autoLabelAccounts), AI corrections of imported labels (aiCorrectsOff)
// and whether the Junk label moves mail to Junk (junkStaysPut).
// Shared by the overview lists and each mailbox's own settings page.
import { computed } from 'vue'
import { labelsForAccount } from '@/lib/labelScope'
import { useSettingsStore } from '@/stores/settings'

const JUNK = 'junk'

export function useMailboxSettings() {
  const settings = useSettingsStore()
  const quiet = computed(() => settings.settings?.quietLabels ?? {})
  const muted = computed(() => new Set(settings.settings?.mutedMailboxes ?? []))
  const autoOn = computed(() => new Set(settings.settings?.autoLabelAccounts ?? []))
  const correctsOff = computed(() => new Set(settings.settings?.aiCorrectsOff ?? []))
  const junkStays = computed(() => new Set(settings.settings?.junkStaysPut ?? []))

  // ---- notifications
  const notifies = (accountId: string) => !muted.value.has(accountId)
  function setNotify(accountId: string, on: boolean) {
    const next = new Set(muted.value)
    if (on) next.delete(accountId)
    else next.add(accountId)
    void settings.update({ mutedMailboxes: [...next] }, 0)
  }

  // ---- mute: no new-mail notifications and no unread count on badges or in All inboxes.
  const isMuted = (accountId: string) => muted.value.has(accountId)
  const setMuted = (accountId: string, on: boolean) => setNotify(accountId, !on)

  // ---- other folders (Gmail labels-as-folders aside: Outlook folders like an old "GitHub")
  const hiddenFolders = computed(() => new Set(settings.settings?.hiddenFolders ?? []))
  const folderShown = (folderId: string) => !hiddenFolders.value.has(folderId)
  function setFolderShown(folderId: string, on: boolean) {
    const next = new Set(hiddenFolders.value)
    if (on) next.delete(folderId)
    else next.add(folderId)
    void settings.update({ hiddenFolders: [...next] }, 0)
  }

  // ---- quiet labels. Junk never notifies, so it's left out of the editable list.
  /** Labels the user can make quiet in a mailbox: that mailbox's labels (hidden imported ones too: they still keep mail quiet), without Junk. */
  const quietOptions = (accountId: string) => labelsForAccount(settings.labels, accountId).filter((l) => l.id !== JUNK)
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

  // ---- the Junk label moves mail to Junk (default), or only labels it (junkStaysPut).
  const junkMoves = (accountId: string) => !junkStays.value.has(accountId)
  function setJunkMoves(accountId: string, on: boolean) {
    const next = new Set(junkStays.value)
    if (on) next.delete(accountId)
    else next.add(accountId)
    void settings.update({ junkStaysPut: [...next] }, 0)
  }

  return { notifies, setNotify, isMuted, setMuted, folderShown, setFolderShown, quietOptions, quietList, setQuiet, junkLabel, autoLabels, setAutoLabel, corrects, setCorrects, junkMoves, setJunkMoves }
}
