import { describe, expect, it } from 'vitest'
import { api } from '@/lib/api'

// The browser mock mirrors the backend: the built-in Junk label has the same id ("junk"),
// and "Apply to existing mail" only adds labels (Junk rules act on new mail).
describe('label rules in the mock backend', () => {
  it('uses the real Junk label id', async () => {
    const s = await api.getSettings()
    expect(s.labels.find((l) => l.id === 'junk')?.name).toBe('Junk')
    expect(s.labels.some((l) => l.id === 'gmail-junk')).toBe(false)
    expect(s.mutedMailboxes).toEqual([])
  })

  it('applies label rules to existing mail once', async () => {
    const s = await api.getSettings()
    const label = s.labels.find((l) => l.source !== 'system')!
    await api.saveSettings({
      ...s,
      labelRules: [
        { id: 'r1', accountId: '', field: 'from', contains: '@', action: 'label', labelId: label.id },
        { id: 'r2', accountId: '', field: 'from', contains: '@', action: 'junk', labelId: '' },
      ],
    })
    const first = await api.applyLabelRules()
    expect(first).toBeGreaterThan(0)
    expect(await api.applyLabelRules()).toBe(0)
  })
})
