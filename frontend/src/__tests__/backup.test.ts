import { describe, expect, it } from 'vitest'
import { backupResultText, restorePendingText, restoreSummaryText } from '@/lib/backup'
import { mock } from '@/lib/mock/backend'
import { OPTIONAL_METHODS } from '@/lib/api'
import type { RestoreSummary } from '@/lib/types'

const base: RestoreSummary = {
  cancelled: false, path: '/tmp/x.gomailbackup', createdAt: '2026-10-20T10:00:00Z', labels: 0, rules: 0, accountsAdded: 0, accountsReady: 0,
  accountsMerged: 0, accountsSkipped: 0, labelAssignments: 0, appliedNow: 0, calendars: 0, secretsRestored: false,
}

describe('backup texts', () => {
  it('summarizes a restore', () => {
    const s = { ...base, labels: 12, rules: 3, accountsAdded: 2, labelAssignments: 4210, appliedNow: 10 }
    expect(restoreSummaryText(s)).toBe('Restored 12 labels, 3 rules, 2 accounts (sign in to finish), 4,210 label assignments.')
    expect(restorePendingText(s)).toBe('4,200 label assignments will be applied as your mail downloads again.')
  })

  it('uses singulars and mentions merged and skipped accounts', () => {
    const s = { ...base, labels: 1, accountsReady: 1, accountsMerged: 1, accountsSkipped: 2, labelAssignments: 1, appliedNow: 1, secretsRestored: true }
    expect(restoreSummaryText(s)).toBe(
      'Restored 1 label, 1 account (already signed in), 1 label assignment, API keys. 1 account already here was kept as is, with the backup’s labels added. 2 accounts in the backup couldn’t be restored.',
    )
    expect(restorePendingText(s)).toBe('')
  })

  it('falls back when there is nothing to count', () => {
    expect(restoreSummaryText(base)).toBe('Restored your settings.')
  })

  it('describes an export', () => {
    expect(backupResultText({ cancelled: false, path: '/b.gomailbackup', accounts: 2, labels: 9, labelAssignments: 1500, includesSecrets: true }))
      .toBe('2 accounts, 9 labels, 1,500 label assignments, including API keys. Saved to /b.gomailbackup')
  })
})

describe('backup in the browser mock', () => {
  it('is optional and round-trips', async () => {
    expect(OPTIONAL_METHODS).toContain('ExportBackup')
    expect(OPTIONAL_METHODS).toContain('ImportBackup')
    const r = await mock.ExportBackup(false)
    expect(r.cancelled).toBe(false)
    expect(r.path).toMatch(/gomail-backup-\d{4}-\d{2}-\d{2}\.gomailbackup$/)
    const s = await mock.ImportBackup()
    expect(s.accountsMerged).toBe(r.accounts)
    expect(s.labelAssignments).toBe(r.labelAssignments)
  })
})
