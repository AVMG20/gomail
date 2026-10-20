import { describe, expect, it } from 'vitest'
import { api } from '@/lib/api'

// Jev auto-labels the mailboxes listed in autoLabelAccounts (the old app-wide switch is kept
// in sync), and a removed mailbox takes its per-mailbox settings with it, like the backend.
describe('per-mailbox settings in the mock backend', () => {
  it('keeps autoClassify in sync with the auto-labeled mailboxes', async () => {
    const s = await api.getSettings()
    expect(s.autoLabelAccounts?.length).toBeGreaterThan(0)
    await api.saveSettings({ ...s, autoLabelAccounts: [] })
    expect((await api.getSettings()).autoClassify).toBe(false)
    await api.saveSettings({ ...(await api.getSettings()), autoLabelAccounts: ['acc_work', 'acc_work'] })
    const next = await api.getSettings()
    expect(next.autoLabelAccounts).toEqual(['acc_work'])
    expect(next.autoClassify).toBe(true)
  })

  it('drops a removed mailbox from the per-mailbox settings', async () => {
    const s = await api.getSettings()
    await api.saveSettings({ ...s, autoLabelAccounts: ['acc_side', 'acc_work'], aiCorrectsOff: ['acc_side'], mutedMailboxes: ['acc_side'], quietLabels: { acc_side: ['junk'] } })
    await api.removeAccount('acc_side')
    const next = await api.getSettings()
    expect(next.autoLabelAccounts).toEqual(['acc_work'])
    expect(next.aiCorrectsOff).toEqual([])
    expect(next.mutedMailboxes).toEqual([])
    expect(next.quietLabels?.acc_side).toBeUndefined()
  })
})
