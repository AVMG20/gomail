// Settings → a mailbox's rules → "N senders skip Junk…": remove one or all of them.
import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import NotJunkSendersDialog from '@/components/settings/NotJunkSendersDialog.vue'
import type { Settings } from '@/lib/types'
import { useSettingsStore } from '@/stores/settings'

describe('senders that skip Junk', () => {
  it('lists the mailbox’s senders and removes one, then all', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const settings = useSettingsStore()
    settings.settings = { notJunkSenders: { a: ['one@example.com', 'two@example.com'], b: ['other@example.com'] } } as unknown as Settings
    const update = vi.spyOn(settings, 'update').mockImplementation(async (p) => {
      settings.settings = { ...settings.settings!, ...p }
    })
    const el = document.createElement('div')
    document.body.appendChild(el)
    const app = createApp({ render: () => h(NotJunkSendersDialog, { accountId: 'a', open: true }) })
    app.use(pinia)
    app.mount(el)
    await nextTick()
    const list = () => document.querySelector('[data-testid="not-junk-senders"]')!
    expect(list().textContent).toContain('one@example.com')
    expect(list().textContent).not.toContain('other@example.com')

    ;(document.querySelector('[aria-label="Remove one@example.com"]') as HTMLElement).click()
    expect(update).toHaveBeenLastCalledWith({ notJunkSenders: { a: ['two@example.com'], b: ['other@example.com'] } }, 0)
    ;[...document.querySelectorAll('button')].find((b) => b.textContent?.includes('Remove all'))!.click()
    expect(update).toHaveBeenLastCalledWith({ notJunkSenders: { b: ['other@example.com'] } }, 0)
    app.unmount()
  })
})
