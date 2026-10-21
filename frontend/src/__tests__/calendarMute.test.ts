// Muting a calendar: shown, no reminders; put back when the backend refuses.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('vue-sonner', () => ({ toast: Object.assign(() => {}, { error: () => {}, success: () => {}, warning: () => {}, dismiss: () => {} }) }))

import { api } from '@/lib/api'
import { useCalendarStore } from '@/stores/calendar'

afterEach(() => vi.restoreAllMocks())

describe('calendar mute', () => {
  const setup = () => {
    setActivePinia(createPinia())
    const cal = useCalendarStore()
    cal.calendars = [{ id: 'c', accountId: 'a', name: 'Lisanne', color: '#a855f7', primary: false, canEdit: false, visible: true }]
    return cal
  }

  it('mutes without hiding', async () => {
    const set = vi.spyOn(api, 'setCalendarMuted').mockResolvedValue()
    const cal = setup()
    await cal.setCalendarMuted('c', true)
    expect(set).toHaveBeenCalledWith('c', true)
    expect(cal.calendars[0]).toMatchObject({ muted: true, visible: true })
  })

  it('puts it back when saving fails', async () => {
    vi.spyOn(api, 'setCalendarMuted').mockRejectedValue(new Error('offline'))
    const cal = setup()
    await cal.setCalendarMuted('c', true)
    expect(cal.calendars[0].muted).toBeFalsy()
  })
})
