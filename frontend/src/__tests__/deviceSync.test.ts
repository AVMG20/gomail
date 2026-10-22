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

  it('keeps an edit whose save starts while the settings are re-read', async () => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    const settings = useSettingsStore()
    settings.settings = { theme: 'light', density: 'comfortable', primaryColor: 'blue' } as Settings
    let answer: (s: Settings) => void = () => {}
    // The backend as it was before the edit was saved, with another computer's color.
    vi.spyOn(api, 'getSettings').mockImplementation(() => new Promise((r) => (answer = r)))
    const save = vi.spyOn(api, 'saveSettings').mockResolvedValue()
    const edited = settings.update({ theme: 'dark' }, 50)
    const reloading = settings.reload()
    await vi.advanceTimersByTimeAsync(60) // the save is due while the re-read is out
    answer({ theme: 'light', density: 'comfortable', primaryColor: 'green' } as Settings)
    await reloading
    await edited
    expect(settings.settings?.theme).toBe('dark')
    expect(settings.settings?.primaryColor).toBe('green')
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ theme: 'dark', primaryColor: 'green' }))
    vi.useRealTimers()
  })

  it('reads what another computer changed before saving over it', async () => {
    setActivePinia(createPinia())
    const settings = useSettingsStore()
    settings.settings = { theme: 'light', density: 'comfortable' } as Settings
    const read = vi.spyOn(api, 'getSettings').mockRejectedValueOnce(new Error('busy')).mockResolvedValue({ theme: 'light', density: 'compact' } as Settings)
    const save = vi.spyOn(api, 'saveSettings').mockResolvedValue()
    await settings.reload() // settings:changed, but the re-read failed
    await settings.update({ theme: 'dark' }, 0)
    expect(read).toHaveBeenCalledTimes(2)
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ theme: 'dark', density: 'compact' }))
  })
})

describe('Settings → Sync', () => {
  const off = normSyncStatus({ available: true, connected: false })
  const on = normSyncStatus({ available: true, connected: true, serverUrl: 'https://gomail.avmg.nl' })
  const text = () => document.body.textContent ?? ''

  it('says why sync is off when this computer lost its code', async () => {
    vi.spyOn(api, 'getDeviceSyncStatus').mockResolvedValue({ ...off, error: 'This computer’s sync code is missing from the keychain.' })
    const { app, el } = mount()
    await vi.waitFor(() => expect(q(el, 'sync-off-error')?.textContent).toContain('missing from the keychain'))
    app.unmount()
  })

  it('says sync is on when only showing the new code failed', async () => {
    const { toast } = await import('vue-sonner')
    const error = vi.spyOn(toast, 'error')
    vi.spyOn(api, 'getDeviceSyncStatus').mockResolvedValue(off)
    vi.spyOn(api, 'connectDeviceSync').mockResolvedValue(on)
    vi.spyOn(api, 'getDeviceSyncCode').mockRejectedValueOnce(new Error('keychain locked')).mockResolvedValue('zoo zoo vote')
    const { app, el } = mount()
    await vi.waitFor(() => expect(q(el, 'sync-turn-on')).not.toBeNull())
    q(el, 'sync-turn-on')!.click()
    await vi.waitFor(() => expect(error).toHaveBeenCalled())
    const [title, opts] = error.mock.calls[0] as [string, { action: { label: string; onClick: () => void } }]
    expect(title).toMatch(/^Sync is on/)
    expect(opts.action.label).toBe('Show sync code')
    expect(q(el, 'sync-show-code')).not.toBeNull()
    opts.action.onClick()
    await vi.waitFor(() => expect(q(el, 'sync-code')?.querySelectorAll('li')).toHaveLength(3))
    app.unmount()
  })

  it('warns that turning off forgets the code, and offers to show it first', async () => {
    vi.spyOn(api, 'getDeviceSyncStatus').mockResolvedValue(on)
    vi.spyOn(api, 'getDeviceSyncCode').mockResolvedValue('zoo zoo vote')
    const off = vi.spyOn(api, 'disconnectDeviceSync').mockResolvedValue()
    const { app, el } = mount()
    await vi.waitFor(() => expect(q(el, 'sync-show-code')).not.toBeNull())
    ;[...el.querySelectorAll('button')].find((b) => b.textContent?.includes('Turn off'))!.click()
    await vi.waitFor(() => expect(text()).toContain('This computer forgets the sync code'))
    q(document.body, 'sync-off-show-code')!.click()
    await vi.waitFor(() => expect(q(el, 'sync-code')?.querySelectorAll('li')).toHaveLength(3))
    expect(off).not.toHaveBeenCalled()
    app.unmount()
  })
})

describe('removing a mailbox', () => {
  it('says the other computers remove it too while sync is on', async () => {
    const { default: MailboxPage } = await import('@/components/settings/MailboxPage.vue')
    const { useAccountsStore } = await import('@/stores/accounts')
    const { useUiStore } = await import('@/stores/ui')
    const pinia = createPinia()
    setActivePinia(pinia)
    const settings = useSettingsStore()
    await settings.load()
    const accounts = useAccountsStore()
    await accounts.load()
    const id = accounts.accounts[0].id
    vi.spyOn(api, 'getDeviceSyncStatus').mockResolvedValue(normSyncStatus({ available: true, connected: true }))
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp({ render: () => h(MailboxPage, { accountId: id }) })
    app.use(pinia)
    app.mount(el)
    useUiStore().focusAccount = { id, field: 'remove' }
    await vi.waitFor(() => expect(document.querySelector('[data-testid="remove-synced"]')?.textContent).toContain('other computers remove it too'))
    app.unmount()
  })
})
