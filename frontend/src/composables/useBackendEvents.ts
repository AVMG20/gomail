import { onBeforeUnmount } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { toast } from 'vue-sonner'
import { api } from '@/lib/api'
import { displayName, parseAddressList } from '@/lib/format'
import type { Address } from '@/lib/types'
import { useComposeStore } from '@/stores/compose'
import { useUiStore } from '@/stores/ui'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'

/** Sync errors that fix themselves (offline, timeouts, rate limits, server hiccups): the account's
 *  sync status shows them, no toast. An expired sign-in has its own banner. */
const TRANSIENT_SYNC = /offline|took too long|try again|can.t reach|limiting requests|rate limit|having problems|cancelled/i
const REAUTH_SYNC = /sign-in for .* (has expired|was revoked)|sign in again|rejected the credentials/i

/** The parts of a sync:finished error that need the user (the backend joins several with "; "). */
export function syncErrorsForUser(error: string): string[] {
  return error.split('; ').map((s) => s.trim()).filter((s) => s && !TRANSIENT_SYNC.test(s) && !REAUTH_SYNC.test(s))
}

/** Subscribe to backend events and refresh the affected state (debounced). */
export function useBackendEvents() {
  const accounts = useAccountsStore()
  const mail = useMailStore()
  const ai = useAiStore()
  const settings = useSettingsStore()
  const compose = useComposeStore()
  const ui = useUiStore()
  const addrs = (v: string | Address[] | null | undefined): Address[] => (Array.isArray(v) ? v : v ? parseAddressList(v) : [])
  const refreshLabels = useDebounceFn(() => {
    void settings.refreshLabels()
    void accounts.refresh({ labels: true })
  }, 300)

  // Only refetch calendar data when the calendar has been opened this session.
  const refreshCalendar = useDebounceFn(async () => {
    // Lazy: the calendar store (and its helpers) stay out of the mail bundle.
    const { useCalendarStore } = await import('@/stores/calendar')
    const cal = useCalendarStore()
    if (cal.loaded) void cal.refresh()
  }, 250)

  const refreshList = useDebounceFn(() => void mail.softRefresh(), 250)

  // Accounts with a "Sync now" (accounts.sync) running: its outcome gets a toast, background rounds' don't.
  const manual = new Set<string>()
  const offSyncAction = accounts.$onAction(({ name, args, after }) => {
    if (name !== 'sync') return
    const ids = args[0] ? [args[0] as string] : accounts.accounts.map((a) => a.id)
    ids.forEach((id) => manual.add(id))
    // A failed call: mailboxes whose round finished have said how it went (sync:finished); the
    // others never got that far (offline, removed meanwhile) and get the call's error.
    after((error) => {
      if (!error) return
      const left = ids.filter((id) => manual.delete(id))
      if (left.length) reportManual(left, error as string)
    })
  })
  /** Per account: the last sync error that needed the user, so a repeat of it doesn't toast again. */
  const lastError = new Map<string, string>()

  /** The one toast of a failed "Sync now", whichever button, menu or command started it. */
  function reportManual(accountIds: string[], error: string) {
    const forUser = syncErrorsForUser(error)
    if (forUser.length) accountIds.forEach((id) => lastError.set(id, forUser.join('; ')))
    if (REAUTH_SYNC.test(error) && !forUser.length) return // the sign-in banner says it
    // While offline every mailbox fails the same way: one toast for all of them.
    const offline = !forUser.length && /offline/i.test(error)
    const acc = accountIds.length === 1 ? accounts.accountMap.get(accountIds[0]) : undefined
    toast.error(offline || !acc ? 'Couldn’t sync' : `Sync failed for ${acc.email}`, {
      id: offline ? 'sync-offline' : `sync-failed:${accountIds.join(',')}`,
      description: forUser.length ? forUser.join('; ') : error,
    })
  }

  function onSyncError(accountId: string, error: string, wasManual: boolean) {
    if (wasManual) return reportManual([accountId], error)
    const acc = accounts.accountMap.get(accountId)
    const forUser = syncErrorsForUser(error)
    const known = lastError.get(accountId)
    if (forUser.length) lastError.set(accountId, forUser.join('; '))
    if (forUser.length && forUser.join('; ') !== known) {
      toast.error(`Sync failed${acc ? ` for ${acc.email}` : ''}`, { id: `sync-failed:${accountId}`, description: forUser.join('; ') })
    }
  }

  const offs = [
    offSyncAction,
    api.on('sync:started', (p) => p?.accountId && accounts.setSyncing(p.accountId, true)),
    api.on('sync:finished', (p) => {
      if (!p) return
      accounts.setSyncing(p.accountId, false)
      const wasManual = manual.delete(p.accountId)
      if (p.error) onSyncError(p.accountId, p.error, wasManual)
      else lastError.delete(p.accountId)
      if (p.newMessages > 0) {
        accounts.refreshCounts(p.accountId)
        mail.threadsChanged({ accountId: p.accountId })
        refreshList()
      } else {
        // Mail the round changed comes with messages:changed (its counts refresh from there); the
        // event has no "changed" flag. Only the sync status (last synced, error) is new here.
        void accounts.refresh({ accounts: true })
      }
    }),
    api.on('messages:changed', (p) => {
      // Cached conversations go only where mail changed (the event's ids, else checked against the refreshed list).
      const ids = (p as { ids?: string[] } | undefined)?.ids
      mail.threadsChanged({ accountId: p?.accountId, ids })
      // Only refresh the list when the change can affect the current view, and isn't just the
      // backend confirming a read/star change the list already shows.
      const echo = mail.takeEcho(p?.accountId ?? '')
      if (!echo && (!p?.accountId || !mail.mailbox.accountId || p.accountId === mail.mailbox.accountId)) refreshList()
      accounts.refreshCounts(p?.accountId ?? '')
    }),
    // The account list and its status; with an account id, that mailbox's folders (a round may
    // have found new ones) — its mail's counts follow messages:changed.
    api.on('accounts:changed', (p) => {
      void accounts.refresh({ accounts: true, folders: p?.accountId ?? '', labels: !p?.accountId }).then(() => mail.ensureValidMailbox())
    }),
    api.on('labels:changed', () => refreshLabels()),
    api.on('settings:changed', () => void settings.reload()),
    api.on('network:status', (p) => {
      if (!p) return
      const was = accounts.online
      accounts.online = p.online
      if (was && !p.online) toast('You’re offline', { description: 'Changes to your mail sync when you reconnect, as long as gomail stays open. Mail can’t be sent until then.' })
      if (!was && p.online) accounts.refreshCounts()
    }),
    api.on('search:updated', (p) => {
      // Server-side results arrived: refresh the list search and any open palette query.
      if (mail.search && (!p?.query || p.query.trim() === mail.search.trim())) void mail.softRefresh()
      window.dispatchEvent(new CustomEvent('gomail:search-updated', { detail: p }))
    }),
    api.on('notification:open', async (p) => {
      if (p?.eventId) {
        const { useCalendarStore } = await import('@/stores/calendar')
        useCalendarStore().openEventId = p.eventId
        ui.mainView = 'calendar'
        return
      }
      if (!p?.messageId) return
      ui.mainView = 'mail'
      void mail.revealNew(p.accountId, p.messageId)
    }),
    api.on('ai:budget', (p) => {
      if (!p) return
      const spent = typeof p.todayUsd === 'number' && p.budgetUsd ? ` ($${p.todayUsd.toFixed(2)} of $${p.budgetUsd.toFixed(2)})` : ''
      if (p.paused) toast('AI labeling paused for today', { description: `The daily AI budget was reached${spent}. Change it in Settings → AI.` })
      else toast('AI labeling resumed')
    }),
    // mailto: links handed to gomail by the OS.
    api.on('compose:open', (p) => {
      if (!p) return
      compose.composeWith({ to: addrs(p.to), cc: addrs(p.cc), bcc: addrs(p.bcc), subject: p.subject ?? '', body: p.body ?? '' })
    }),
    api.on('update:available', (p) => {
      if (!p?.available) return
      settings.updateInfo = p
    }),
    api.on('calendar:changed', () => refreshCalendar()),
    api.on('calendar:open', async (p) => {
      if (!p?.eventId) return
      const { useCalendarStore } = await import('@/stores/calendar')
      useCalendarStore().openEventId = p.eventId
      ui.mainView = 'calendar'
    }),
    api.on('database:status', (p) => {
      if (p && !p.ok) toast.error('Mail database problem', { description: p.message || 'gomail is repairing its local database.', duration: 10000 })
    }),
    api.on('sync:progress', (p) => {
      if (!p?.accountId) return
      if (p.phase === 'backfill') {
        // Background download of older mail: no spinner, cleared when complete.
        if (p.total && p.done >= p.total) accounts.setProgress(null, p.accountId)
        else accounts.setProgress(p)
        return
      }
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
            ui.mainView = 'mail' // also from the calendar
            void mail.revealNew(p.accountId, p.latest.id, p.latest.folderIds)
          },
        },
      })
    }),
  ]

  onBeforeUnmount(() => offs.forEach((off) => off()))
}
