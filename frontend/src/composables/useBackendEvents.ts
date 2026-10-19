import { onBeforeUnmount } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { toast } from 'vue-sonner'
import { api } from '@/lib/api'
import { displayName } from '@/lib/format'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

/** Subscribe to backend events and refresh the affected state (debounced). */
export function useBackendEvents() {
  const accounts = useAccountsStore()
  const mail = useMailStore()
  const ai = useAiStore()
  const settings = useSettingsStore()
  const refreshLabels = useDebounceFn(() => {
    void settings.refreshLabels()
    void accounts.loadLabelCounts()
  }, 300)

  const refreshList = useDebounceFn(() => {
    mail.invalidateThread()
    void mail.softRefresh()
  }, 250)
  const reloadAccounts = useDebounceFn(async () => {
    await accounts.load()
    mail.ensureValidMailbox()
  }, 200)

  const offs = [
    api.on('sync:started', (p) => p?.accountId && accounts.setSyncing(p.accountId, true)),
    api.on('sync:finished', (p) => {
      if (!p) return
      accounts.setSyncing(p.accountId, false)
      if (p.error) {
        const acc = accounts.accountMap.get(p.accountId)
        toast.error(`Sync failed${acc ? ` for ${acc.email}` : ''}`, { description: p.error })
      }
      accounts.refreshCounts()
      if (p.newMessages > 0) refreshList()
    }),
    api.on('messages:changed', (p) => {
      // Only refresh the list when the change can affect the current view.
      if (!p?.accountId || !mail.mailbox.accountId || p.accountId === mail.mailbox.accountId) refreshList()
      accounts.refreshCounts()
    }),
    api.on('accounts:changed', () => void reloadAccounts()),
    api.on('labels:changed', () => refreshLabels()),
    api.on('network:status', (p) => {
      if (!p) return
      const was = accounts.online
      accounts.online = p.online
      if (was && !p.online) toast('You’re offline', { description: 'Changes are saved and will sync when you reconnect.' })
      if (!was && p.online) accounts.refreshCounts()
    }),
    api.on('search:updated', (p) => {
      // Server-side results arrived: refresh the list search and any open palette query.
      if (mail.search && (!p?.query || p.query.trim() === mail.search.trim())) void mail.softRefresh()
      window.dispatchEvent(new CustomEvent('gomail:search-updated', { detail: p }))
    }),
    api.on('notification:open', (p) => {
      if (!p?.messageId) return
      if (p.accountId && p.accountId !== mail.mailbox.accountId) mail.switchAccount(p.accountId)
      setTimeout(() => mail.open(p.messageId), 250)
    }),
    api.on('ai:budget', (p) => {
      if (p?.paused) toast('AI labeling paused for today', { description: 'The daily AI budget was reached. Change it in Settings → AI.' })
    }),
    api.on('sync:progress', (p) => {
      if (!p?.accountId) return
      accounts.setProgress(p)
      if (!accounts.syncing.has(p.accountId)) accounts.setSyncing(p.accountId, true)
    }),
    api.on('classify:progress', (p) => p && ai.onProgress(p)),
    api.on('classify:finished', (p) => ai.onFinished(p ?? {})),
    api.on('mail:new', (p) => {
      if (!p?.latest) return
      const acc = accounts.accountMap.get(p.accountId)
      toast(`${p.count > 1 ? `${p.count} new messages` : `New mail from ${displayName(p.latest.from)}`}`, {
        description: p.latest.subject + (acc && accounts.accounts.length > 1 ? ` · ${acc.displayName || acc.email}` : ''),
        action: {
          label: 'Open',
          onClick: () => {
            mail.switchAccount(p.accountId)
            setTimeout(() => mail.open(p.latest.id), 300)
          },
        },
      })
    }),
  ]

  onBeforeUnmount(() => offs.forEach((off) => off()))
}
