// Labels belong to mailboxes (Label.accountIds). These helpers decide which labels a view,
// a message or a selection offers; the backend applies the same rules (config.LabelInAccount).
import type { Label } from './types'

/** Whether a label belongs to a mailbox. Junk (system) and labels without a mailbox list belong to
 *  every mailbox; accountId "" (the unified "All inboxes" view) matches every label. */
export function labelInAccount(l: Label, accountId: string): boolean {
  return !accountId || l.source === 'system' || !l.accountIds?.length || l.accountIds.includes(accountId)
}

/** The labels of one mailbox ("" = all labels). */
export function labelsForAccount(labels: Label[], accountId: string): Label[] {
  return accountId ? labels.filter((l) => labelInAccount(l, accountId)) : labels
}

/** The labels of any of these mailboxes (a mixed selection); no mailbox known = all labels. */
export function labelsForAccounts(labels: Label[], accountIds: Iterable<string>): Label[] {
  const ids = [...new Set(accountIds)].filter(Boolean)
  if (!ids.length) return labels
  return labels.filter((l) => ids.some((a) => labelInAccount(l, a)))
}

/** The mailboxes a new label gets: the one being viewed, or every mailbox in the unified view. */
export function newLabelAccounts(viewingAccountId: string, allAccountIds: string[]): string[] {
  return viewingAccountId && allAccountIds.includes(viewingAccountId) ? [viewingAccountId] : [...allAccountIds]
}

/** A label's mailboxes among the current ones (an empty list means all of them). */
export function labelAccounts(l: Label, allAccountIds: string[]): string[] {
  if (l.source === 'system' || !l.accountIds?.length) return [...allAccountIds]
  return allAccountIds.filter((a) => l.accountIds!.includes(a))
}

/** Toggle one mailbox of a label; at least one mailbox stays. Returns the new list. */
export function toggleLabelAccount(l: Label, accountId: string, on: boolean, allAccountIds: string[]): string[] {
  const cur = labelAccounts(l, allAccountIds)
  const next = on ? [...new Set([...cur, accountId])] : cur.filter((a) => a !== accountId)
  return next.length ? allAccountIds.filter((a) => next.includes(a)) : cur
}
