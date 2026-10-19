import { onBeforeUnmount, onMounted } from 'vue'
import { hasMod, isEditableTarget } from '@/lib/platform'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'

/** Global keyboard shortcuts. Single keys never fire while typing or in dialogs. */
export function useShortcuts() {
  const mail = useMailStore()
  const ui = useUiStore()
  const compose = useComposeStore()
  const accounts = useAccountsStore()
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
    if (mod && !e.shiftKey && !e.altKey && /^[0-9]$/.test(key)) {
      e.preventDefault()
      if (key === '0') mail.switchAccount('')
      else {
        const acc = accounts.accounts[Number(key) - 1]
        if (acc) mail.switchAccount(acc.id)
      }
      return
    }

    const typing = isEditableTarget(e.target)
    if (typing || ui.anyDialogOpen()) return

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
      const map: Record<string, string> = { i: 'inbox', s: 'starred', t: 'sent', d: 'drafts', a: 'archive' }
      if (map[key]) {
        e.preventDefault()
        mail.goToKind(map[key])
      }
      return
    }

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
        void respond('reply')
        break
      case 'a':
        void respond('replyAll')
        break
      case 'f':
        void respond('forward')
        break
      case 'c':
        e.preventDefault()
        compose.compose()
        break
      case '/':
        e.preventDefault()
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
