import { onBeforeUnmount, onMounted } from 'vue'
import { hasMod, isEditableTarget } from '@/lib/platform'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'
import { useSettingsStore } from '@/stores/settings'

/** Global keyboard shortcuts. Single keys never fire while typing or in dialogs. */
export function useShortcuts() {
  const mail = useMailStore()
  const ui = useUiStore()
  const compose = useComposeStore()
  const accounts = useAccountsStore()
  const settings = useSettingsStore()
  let gPending = false
  let gTimer: ReturnType<typeof setTimeout> | null = null

  function targets(): string[] {
    if (mail.selection.size) return [...mail.selection]
    return mail.openId ? [mail.openId] : []
  }

  async function respond(mode: 'reply' | 'replyAll' | 'forward') {
    const id = mail.openId
    if (!id) return
    const thread = await mail.loadThread(id)
    const m = thread[thread.length - 1]
    if (m) compose.respond(mode, m)
  }

  async function bulk(fn: (ids: string[]) => unknown) {
    const ids = mail.selectAllQuery ? await mail.targetIds() : targets()
    if (!ids.length) return
    if (mail.selection.size || mail.selectAllQuery) mail.clearSelection()
    await fn(ids)
  }

  function onKey(e: KeyboardEvent) {
    if (e.defaultPrevented) return
    const mod = hasMod(e)
    const key = e.key

    // ---- global, also while typing
    if (mod && key.toLowerCase() === 'k') {
      e.preventDefault()
      ui.paletteOpen = !ui.paletteOpen
      return
    }
    if (mod && key === ',') {
      e.preventDefault()
      ui.openSettings()
      return
    }
    // Text size: mod + / - / 0 (like browsers); All inboxes moved to mod+shift+0.
    if (mod && !e.altKey && (key === '=' || key === '+' || key === '-' || (key === '0' && !e.shiftKey))) {
      e.preventDefault()
      const z = settings.settings?.zoom ?? 1
      const next = key === '0' ? 1 : Math.min(1.25, Math.max(0.9, Math.round((z + (key === '-' ? -0.05 : 0.05)) * 100) / 100))
      void settings.update({ zoom: next })
      return
    }
    if (mod && e.shiftKey && e.code === 'Digit0') {
      e.preventDefault()
      ui.mainView = 'mail'
      mail.switchAccount('')
      return
    }
    if (mod && !e.shiftKey && !e.altKey && /^[1-9]$/.test(key)) {
      e.preventDefault()
      const acc = accounts.accounts[Number(key) - 1]
      if (acc) {
        ui.mainView = 'mail'
        mail.switchAccount(acc.id)
      }
      return
    }

    const typing = isEditableTarget(e.target)
    // Focus inside the compose window, an open menu or a popover owns the keyboard.
    const inWidget = !!(e.target as Element | null)?.closest?.('[role="dialog"], [role="alertdialog"], [role="menu"], [role="menubar"], [data-no-shortcuts]')
    if (typing || inWidget || ui.anyDialogOpen()) return
    if (ui.keyGuarded(key)) {
      // Right after a compose window closed: never trash/act on the open mail by accident.
      e.preventDefault()
      return
    }

    // The calendar owns mod+B (its own sidebar) and has nothing to select-all.
    if (ui.mainView === 'calendar' && mod && (key.toLowerCase() === 'b' || key.toLowerCase() === 'a')) return
    if (mod && key.toLowerCase() === 'b') {
      e.preventDefault()
      ui.folderSidebarOpen = !ui.folderSidebarOpen
      return
    }
    if (mod && key.toLowerCase() === 'a') {
      e.preventDefault()
      mail.selectAllLoaded()
      return
    }
    if (mod || e.altKey) return

    if (gPending) {
      gPending = false
      if (gTimer) clearTimeout(gTimer)
      if (key === 'c' || key === 'm') {
        e.preventDefault()
        ui.mainView = key === 'c' ? 'calendar' : 'mail'
        return
      }
      const map: Record<string, string> = { i: 'inbox', s: 'starred', t: 'sent', d: 'drafts', a: 'archive' }
      // The second key of a g… sequence is always consumed, so it never also
      // triggers a calendar key (g d = Drafts, not Day view).
      e.preventDefault()
      if (map[key]) {
        ui.mainView = 'mail'
        mail.goToKind(map[key])
      }
      return
    }

    // The calendar handles its own single keys (t, d, w, m, a, n/c = new event, arrows); only g… and ? stay global.
    if (ui.mainView === 'calendar' && key !== 'g' && key !== '?') return

    switch (key) {
      case 'g':
        gPending = true
        gTimer = setTimeout(() => (gPending = false), 1200)
        break
      case 'j':
      case 'ArrowDown':
        e.preventDefault()
        mail.move(1)
        break
      case 'k':
      case 'ArrowUp':
        e.preventDefault()
        mail.move(-1)
        break
      case 'Enter':
        if (!mail.openId && mail.messages.length) mail.open(mail.messages[0].id)
        break
      case 'Escape':
        if (mail.selection.size || mail.selectAllQuery) mail.clearSelection()
        else if (mail.openId) mail.open(null)
        break
      case 'e':
        void bulk(mail.archive)
        break
      case '#':
      case 'Delete':
      case 'Backspace':
        e.preventDefault()
        void bulk(mail.trash)
        break
      case 's': {
        const ids = targets()
        const first = ids[0] ? mail.byId.get(ids[0]) : undefined
        if (first) void bulk((x) => mail.setStarred(x, !first.starred))
        break
      }
      case 'u':
        void bulk((x) => mail.setRead(x, false))
        break
      case 'I':
        void bulk((x) => mail.setRead(x, true))
        break
      case 'x':
        if (mail.openId) mail.toggleSelect(mail.openId)
        break
      case 'l':
        if (mail.openId) {
          e.preventDefault()
          ui.labelPopoverOpen = true
        }
        break
      case 'r':
        e.preventDefault() // the composer focuses its To field; don't type the key into it
        void respond('reply')
        break
      case 'a':
        e.preventDefault() // the composer focuses its To field; don't type the key into it
        void respond('replyAll')
        break
      case 'f':
        e.preventDefault() // the composer focuses its To field; don't type the key into it
        void respond('forward')
        break
      case 'c':
        e.preventDefault()
        compose.compose()
        break
      case '/':
        e.preventDefault()
        // Reading pane off + a message open: the list (and its search box) is hidden; go back to it.
        if (settings.settings?.readingPane === 'off' && mail.openId) mail.open(null)
        ui.focusSearchTick++
        break
      case '?':
        ui.shortcutsOpen = true
        break
    }
  }

  onMounted(() => window.addEventListener('keydown', onKey))
  onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
}
