// Settings → Sync: turn on sync (a 24-word code), join with another device's code, turn it off;
// how often it syncs is a per-computer setting; settings another computer changed are re-read.
import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import SyncTab from '@/components/settings/SyncTab.vue'
import { api, DEVICE_SYNC_METHODS, normSettings, normSyncStatus, OPTIONAL_METHODS, syncCodeWords } from '@/lib/api'
import type { Settings } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'

function mount() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const settings = useSettingsStore()
  settings.settings = { deviceSyncInterval: 1 } as unknown as Settings
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp({ render: () => h(SyncTab) })
  app.use(pinia)
  app.mount(el)
  return { app, el, settings }
}

const q = (el: HTMLElement, id: string) => el.querySelector<HTMLElement>(`[data-testid="${id}"]`)

describe('sync between computers', () => {
  it('is optional and normalizes the status', () => {
    for (const m of DEVICE_SYNC_METHODS) expect(OPTIONAL_METHODS).toContain(m)
    expect(normSyncStatus(null)).toMatchObject({ available: true, connected: false, defaultServerUrl: 'https://gomail.avmg.nl', lastSyncAt: null })
    expect(syncCodeWords('  zoo  zoo\nvote ')).toEqual(['zoo', 'zoo', 'vote'])
  })

  it('syncs every minute unless this computer chose otherwise', () => {
    const base = {} as Settings
    expect(normSettings(base).deviceSyncInterval).toBe(1)
    expect(normSettings({ ...base, deviceSyncInterval: 15 }).deviceSyncInterval).toBe(15)
    expect(normSettings({ ...base, deviceSyncInterval: -1 }).deviceSyncInterval).toBe(-1)
    expect(normSettings({ ...base, deviceSyncInterval: 7 }).deviceSyncInterval).toBe(1)
  })

  it('turns on with a new code, and another computer joins with it', async () => {
    const { app, el } = mount()
    await vi.waitFor(() => expect(q(el, 'sync-turn-on')).not.toBeNull())
    expect((q(el, 'sync-server') as HTMLInputElement).value).toBe('https://gomail.avmg.nl')

    q(el, 'sync-turn-on')!.click()
    await vi.waitFor(() => expect(q(el, 'sync-code')).not.toBeNull(), { timeout: 3000 })
    const words = [...q(el, 'sync-code')!.querySelectorAll('li')].map((li) => li.textContent!.replace(/^\d+/, '').trim())
    expect(words).toHaveLength(24)
    expect(q(el, 'sync-code')!.textContent).toContain('Keep it somewhere safe')
    await vi.waitFor(() => expect(q(el, 'sync-status')?.textContent).toContain('Last synced just now'), { timeout: 3000 })
    const code = await api.getDeviceSyncCode()
    expect(syncCodeWords(code)).toEqual(words)

    // "This computer" turns sync off, then joins again as if it were another one.
    await api.disconnectDeviceSync()
    await vi.waitFor(() => expect(q(el, 'sync-join')).not.toBeNull())
    const input = q(el, 'sync-code-input') as HTMLTextAreaElement
    input.value = words.map((w, i) => `${i + 1}. ${w.toUpperCase()}`).join('\n')
    input.dispatchEvent(new Event('input'))
    await nextTick()
    q(el, 'sync-join')!.click()
    await vi.waitFor(() => expect(q(el, 'sync-show-code')).not.toBeNull(), { timeout: 3000 })
    q(el, 'sync-show-code')!.click()
    await vi.waitFor(() => expect(q(el, 'sync-code')?.querySelectorAll('li')).toHaveLength(24))
    await api.disconnectDeviceSync()
    app.unmount()
  })

  it('names a mistyped word', async () => {
    await expect(api.connectDeviceSync({ serverUrl: '', create: false, code: 'zebra zebrra', includeSecrets: true })).rejects.toThrow(/word 2, “zebrra”/)
    await expect(api.connectDeviceSync({ serverUrl: '', create: false, code: 'zebra zebra', includeSecrets: true })).rejects.toThrow(/24 words; this has 2/)
  })

  it('re-reads settings another computer changed, keeping edits not saved yet', async () => {
    setActivePinia(createPinia())
    const settings = useSettingsStore()
    settings.settings = await api.getSettings()
    const fresh = { ...settings.settings, theme: 'dark', density: 'compact' } as Settings
    vi.spyOn(api, 'getSettings').mockResolvedValue(fresh)
    const save = vi.spyOn(api, 'saveSettings').mockResolvedValue()
    void settings.update({ density: 'comfortable' }, 50)
    await settings.reload()
    expect(settings.settings.theme).toBe('dark')
    expect(settings.settings.density).toBe('comfortable')
    await settings.flush()
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ theme: 'dark', density: 'comfortable' }))
  })
})
