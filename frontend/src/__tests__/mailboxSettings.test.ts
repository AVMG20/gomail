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
    await api.saveSettings({ ...s, autoLabelAccounts: ['acc_side', 'acc_work'], aiCorrectsOff: ['acc_side'], mutedMailboxes: ['acc_side'], quietLabels: { acc_side: ['junk'] }, syncIntervals: { acc_side: 1, acc_work: 15 } })
    await api.removeAccount('acc_side')
    const next = await api.getSettings()
    expect(next.autoLabelAccounts).toEqual(['acc_work'])
    expect(next.aiCorrectsOff).toEqual([])
    expect(next.mutedMailboxes).toEqual([])
    expect(next.quietLabels?.acc_side).toBeUndefined()
    expect(next.syncIntervals).toEqual({ acc_work: 15 })
  })
})

describe('sync interval per mailbox', () => {
  it('defaults to every 5 minutes and stores only the mailboxes that differ', async () => {
    const { setActivePinia, createPinia } = await import('pinia')
    setActivePinia(createPinia())
    const { useSettingsStore } = await import('@/stores/settings')
    const { useMailboxSettings } = await import('@/components/settings/mailboxSettings')
    const settings = useSettingsStore()
    settings.settings = { syncIntervals: {} } as never
    const calls: unknown[] = []
    settings.update = (async (p: unknown) => {
      calls.push(p)
      settings.settings = { ...settings.settings!, ...(p as object) }
    }) as never
    const mb = useMailboxSettings()
    expect(mb.syncInterval('a')).toBe(5)
    mb.setSyncInterval('a', 1)
    expect(mb.syncInterval('a')).toBe(1)
    mb.setSyncInterval('a', 5)
    expect(calls).toEqual([{ syncIntervals: { a: 1 } }, { syncIntervals: {} }])
  })
})

describe('mute per mailbox', () => {
  it('is one setting: muting adds the mailbox to mutedMailboxes, unmuting takes it out', async () => {
    const { setActivePinia, createPinia } = await import('pinia')
    setActivePinia(createPinia())
    const { useSettingsStore } = await import('@/stores/settings')
    const { useMailboxSettings } = await import('@/components/settings/mailboxSettings')
    const settings = useSettingsStore()
    settings.settings = { mutedMailboxes: ['b'] } as never
    settings.update = (async (p: unknown) => {
      settings.settings = { ...settings.settings!, ...(p as object) }
    }) as never
    const mb = useMailboxSettings()
    const muted = () => useSettingsStore().settings?.mutedMailboxes
    expect(mb.isMuted('a')).toBe(false)
    mb.setMuted('a', true)
    expect(muted()).toEqual(['b', 'a'])
    mb.setMuted('b', false)
    expect(muted()).toEqual(['a'])
    expect(mb.isMuted('a')).toBe(true)
  })
})
