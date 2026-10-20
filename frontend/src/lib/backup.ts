import type { BackupResult, RestoreSummary } from './types'

const fmt = (n: number) => n.toLocaleString('en-US')
const count = (n: number, one: string, many = `${one}s`) => `${fmt(n)} ${n === 1 ? one : many}`

/** "Restored 12 labels, 3 rules, 2 accounts (sign in to finish), 4,210 label assignments." */
export function restoreSummaryText(s: RestoreSummary): string {
  const parts: string[] = []
  if (s.labels) parts.push(count(s.labels, 'label'))
  if (s.rules) parts.push(count(s.rules, 'rule'))
  if (s.accountsAdded) parts.push(`${count(s.accountsAdded, 'account')} (sign in to finish)`)
  if (s.accountsReady) parts.push(`${count(s.accountsReady, 'account')} (already signed in)`)
  if (s.labelAssignments) parts.push(count(s.labelAssignments, 'label assignment'))
  if (s.calendars) parts.push(count(s.calendars, 'calendar setting'))
  if (s.secretsRestored) parts.push('API keys')
  let text = parts.length ? `Restored ${parts.join(', ')}.` : 'Restored your settings.'
  if (s.accountsMerged) text += ` ${count(s.accountsMerged, 'account')} already here ${s.accountsMerged === 1 ? 'was' : 'were'} kept as is, with the backup’s labels added.`
  if (s.accountsSkipped) text += ` ${count(s.accountsSkipped, 'account')} in the backup couldn’t be restored.`
  return text
}

/** What's left to do after an import: labels for mail that hasn't synced yet. */
export function restorePendingText(s: RestoreSummary): string {
  const waiting = Math.max(0, s.labelAssignments - s.appliedNow)
  return waiting ? `${count(waiting, 'label assignment')} will be applied as your mail downloads again.` : ''
}

/** Toast description after an export. */
export function backupResultText(r: BackupResult): string {
  const what = [count(r.accounts, 'account'), count(r.labels, 'label'), count(r.labelAssignments, 'label assignment')].join(', ')
  return `${what}${r.includesSecrets ? ', including API keys' : ''}. Saved to ${r.path}`
}
