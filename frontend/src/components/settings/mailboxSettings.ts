// Per-mailbox settings that live in the global Settings object, keyed by account id:
// new-mail notifications (mutedMailboxes: a muted mailbox also shows no unread count), quiet
// labels (quietLabels), Jev auto-labeling (autoLabelAccounts), AI corrections of imported labels (aiCorrectsOff)
// and which labels send mail to Junk (junkStaysPut for the Junk label, Label.then for the others).
// Shared by the overview lists and each mailbox's own settings page.
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { labelsForAccount } from '@/lib/labelScope'
import type { Label } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'

const JUNK = 'junk'
/** How often a mailbox is checked when it has no interval of its own (Go: config.DefaultSyncInterval). */
export const DEFAULT_SYNC_INTERVAL = 5
/** The background check intervals offered (minutes; Go: config.SyncIntervalChoices). */
export const SYNC_INTERVALS = [1, 5, 15, 30, 60]
export const intervalLabel = (m: number) => `Every ${m === 1 ? 'minute' : m === 60 ? 'hour' : `${m} minutes`}`
const THEN_PLACE: Record<string, string> = { archive: 'the archive', junk: 'Junk', trash: 'Trash' }
/** Labels whose "When added" is being applied to existing mail right now. */
const applying = ref(new Set<string>())

export function useMailboxSettings() {
  const settings = useSettingsStore()
  const accounts = useAccountsStore()
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

  // ---- background sync: each mailbox on its own interval (minutes), every 5 minutes unless changed
  const syncInterval = (accountId: string) => settings.settings?.syncIntervals?.[accountId] ?? DEFAULT_SYNC_INTERVAL
  function setSyncInterval(accountId: string, minutes: number) {
    const next = { ...(settings.settings?.syncIntervals ?? {}) }
    if (minutes === DEFAULT_SYNC_INTERVAL) delete next[accountId]
    else next[accountId] = minutes
    void settings.update({ syncIntervals: next }, 0)
  }

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

  // ---- what a label does to mail Jev or a rule gives it (Label.then), and offering to do it
  // for the mail that already has it.
  async function setThen(l: Label, then: string) {
    void settings.update({ labels: settings.labels.map((x) => (x.id === l.id ? { ...x, then } : x)) }, 500)
    if (!then || !hasMethod('ApplyLabelAction') || !l.accountIds?.[0]) return
    // Only offer it when mail has the label (counts leave out labels nothing has).
    try {
      const counts = await api.getLabelCounts(l.accountIds[0])
      if (!counts.some((c) => c.labelId === l.id && c.total > 0)) return
    } catch {
      return
    }
    toast(`“${l.name}” now goes to ${THEN_PLACE[then]}`, {
      description: 'Mail that already has it stays where it is.',
      action: { label: 'Move existing mail', onClick: () => void applyThen(l) },
      duration: 10000,
    })
  }
  async function applyThen(l: Label) {
    if (applying.value.has(l.id)) return
    applying.value = new Set([...applying.value, l.id])
    try {
      await settings.flush()
      const n = await api.applyLabelAction(l.id)
      toast(n ? `Moved ${n.toLocaleString()} message${n === 1 ? '' : 's'} to ${placeOf(l.then ?? '')}` : `Nothing to move: mail with “${l.name}” is already there`)
      accounts.refreshCounts()
    } catch (e) {
      toast.error('Couldn’t move the mail', { description: errorMessage(e) })
    } finally {
      const next = new Set(applying.value)
      next.delete(l.id)
      applying.value = next
    }
  }
  const isApplying = (labelId: string) => applying.value.has(labelId)
  const placeOf = (then: string) => THEN_PLACE[then] ?? then

  // ---- labels that send mail to Junk: the built-in Junk label (unless junkStaysPut) and the
  // mailbox's labels set to "→ Junk" (imported ones only while shown: hidden, they don't act).
  const junkOptions = (accountId: string) => [junkLabel.value as Label, ...settings.labelsForAccount(accountId).filter((l) => l.id !== JUNK)]
  function junkList(accountId: string): string[] {
    return junkOptions(accountId)
      .filter((l) => (l.id === JUNK ? !junkStays.value.has(accountId) : l.then === 'junk'))
      .map((l) => l.id)
  }
  function setGoesToJunk(accountId: string, l: Label, on: boolean) {
    if (l.id === JUNK) {
      const next = new Set(junkStays.value)
      if (on) next.delete(accountId)
      else next.add(accountId)
      void settings.update({ junkStaysPut: [...next] }, 0)
    } else if (on) setThen(l, 'junk')
    else if (l.then === 'junk') setThen(l, '')
  }

  return {
    notifies, setNotify, isMuted, setMuted, syncInterval, setSyncInterval, folderShown, setFolderShown, quietOptions, quietList, setQuiet, junkLabel,
    autoLabels, setAutoLabel, corrects, setCorrects, setThen, applyThen, isApplying, placeOf, junkOptions, junkList, setGoesToJunk,
  }
}
