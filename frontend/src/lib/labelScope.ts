// Every mailbox has its own labels (Label.accountIds holds its one account id; Junk, the built-in
// system label, belongs to every mailbox). The unified "All inboxes" view groups labels with the
// same name, so "Newsletters" shows once and stands for the Newsletters label of every mailbox.
// The backend applies the same rules (config.LabelInAccount, config.LabelNamed).
import type { Label, LabelCount } from './types'
import { useUiStore } from '@/stores/ui'

/** Whether a label belongs to a mailbox. Junk (system) belongs to every mailbox; accountId ""
 *  (the unified view) matches every label. A label without a mailbox (only in settings the
 *  backend hasn't split yet) counts as every mailbox's. */
export function labelInAccount(l: Label, accountId: string): boolean {
  return !accountId || l.source === 'system' || !l.accountIds?.length || l.accountIds.includes(accountId)
}

/** The grouping key of a label: its name, case-insensitive. */
export const labelKey = (l: Pick<Label, 'name'>) => l.name.trim().toLowerCase()

/** One label per name (the first one), for views that span mailboxes. */
export function groupByName(labels: Label[]): Label[] {
  const seen = new Set<string>()
  return labels.filter((l) => {
    const k = labelKey(l)
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
}

/** The labels of one mailbox; "" = the unified view: one label per name. */
export function labelsForAccount(labels: Label[], accountId: string): Label[] {
  return accountId ? labels.filter((l) => labelInAccount(l, accountId)) : groupByName(labels)
}

/** The labels a selection offers: one mailbox's own labels, or one per name for mail of several
 *  mailboxes (none known = every mailbox). Adding one gives each message its own mailbox's label
 *  of that name. */
export function labelsForAccounts(labels: Label[], accountIds: Iterable<string>): Label[] {
  const ids = [...new Set(accountIds)].filter(Boolean)
  if (ids.length === 1) return labelsForAccount(labels, ids[0])
  if (!ids.length) return groupByName(labels)
  return groupByName(labels.filter((l) => ids.some((a) => labelInAccount(l, a))))
}

/** A mailbox's label with the same name as `l` (l itself when it is the mailbox's), if any. */
export function labelInMailbox(labels: Label[], l: Label, accountId: string): Label | undefined {
  if (labelInAccount(l, accountId)) return l
  const k = labelKey(l)
  return labels.find((x) => labelKey(x) === k && labelInAccount(x, accountId))
}

/** The ids of every label with the same name as one of these (the unified view's groups). */
export function sameNameIds(labels: Label[], ids: string[]): string[] {
  if (!ids.length) return ids
  const byId = new Map(labels.map((l) => [l.id, l]))
  const keys = new Set(ids.map((id) => byId.get(id)).filter((l): l is Label => !!l).map(labelKey))
  const out = new Set(ids)
  for (const l of labels) if (keys.has(labelKey(l))) out.add(l.id)
  return [...out]
}

/** The id a view's label filter keeps for a label id: the mailbox's label with that name (labels
 *  of other mailboxes, or old ids from before labels were per mailbox, follow by name), or the
 *  group's first label in the unified view. undefined = no such label (any more). */
export function viewLabelId(labels: Label[], id: string, accountId: string): string | undefined {
  const l = labels.find((x) => x.id === id)
  if (!l) return undefined
  if (!accountId) return groupByName(labels).find((x) => labelKey(x) === labelKey(l))?.id
  return labelInMailbox(labels, l, accountId)?.id
}

/** A label's count summed over the labels with its name (counts are per label id; in one
 *  mailbox's view the other mailboxes' labels count 0, so this is that mailbox's count). */
export function groupCount(labels: Label[], counts: Map<string, LabelCount>, l: Label): { total: number; unread: number } | undefined {
  let found = false
  let total = 0
  let unread = 0
  for (const id of sameNameIds(labels, [l.id])) {
    const c = counts.get(id)
    if (!c) continue
    found = true
    total += c.total
    unread += c.unread
  }
  return found ? { total, unread } : undefined
}

/** A label id that isn't taken yet, made from a name (like the backend's slugs). */
export function newLabelId(labels: Label[], name: string): string {
  const base =
    name
      .toLowerCase()
      .replace(/[^\p{L}\p{N}]+/gu, '-')
      .replace(/^-+|-+$/g, '') || 'label'
  const taken = new Set(labels.map((l) => l.id))
  let id = base === 'junk' ? 'junk-label' : base
  for (let n = 2; taken.has(id); n++) id = `${base}-${n}`
  return id
}

/** Whether a mailbox already has a label with this name (Junk counts for every mailbox). */
export function nameTaken(labels: Label[], accountId: string, name: string, exceptId = ''): boolean {
  const k = name.trim().toLowerCase()
  return labels.some((l) => l.id !== exceptId && labelKey(l) === k && (l.source === 'system' || labelInAccount(l, accountId)))
}

/** "Manage labels": the mailbox's Labels tab in Settings, or the list of mailboxes in the unified
 *  view (every mailbox has its own labels). */
export function openLabelSettings(accountId: string) {
  const ui = useUiStore()
  if (accountId) ui.openSettings(`mailbox:${accountId}`, 'labels')
  else ui.openSettings('accounts')
}

/** Label ids worth showing as chips: Junk is left out for mail in a Junk folder (the folder says it already). */
export function chipLabelIds(labels: string[], folderIds: string[] | undefined, kindOf: (folderId: string) => string | undefined): string[] {
  if (!labels.includes('junk') || !(folderIds ?? []).some((f) => kindOf(f) === 'spam')) return labels
  return labels.filter((id) => id !== 'junk')
}

/** Copies a mailbox's own labels (name, color, description, action) to another mailbox, skipping
 *  names it already has. Returns the new label list and how many were copied. */
export function copyLabelsTo(labels: Label[], fromId: string, toId: string): { labels: Label[]; copied: number } {
  const next = [...labels]
  let copied = 0
  for (const l of labels) {
    if (l.source && l.source !== 'user') continue
    if (!labelInAccount(l, fromId) || nameTaken(next, toId, l.name)) continue
    next.push({ ...l, id: newLabelId(next, l.name), accountIds: [toId] })
    copied++
  }
  return { labels: next, copied }
}
