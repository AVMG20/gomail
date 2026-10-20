import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'

export type SettingsTab = 'general' | 'appearance' | 'accounts' | 'labels' | 'ai' | 'calendar' | 'about'

function readMainView(): 'mail' | 'calendar' {
  try {
    return localStorage.getItem('gomail.mainView') === 'calendar' ? 'calendar' : 'mail'
  } catch {
    return 'mail'
  }
}

export const useUiStore = defineStore('ui', () => {
  const settingsOpen = ref(false)
  const settingsTab = ref<SettingsTab>('general')
  const paletteOpen = ref(false)
  const shortcutsOpen = ref(false)
  const addAccountOpen = ref(false)
  /** Opens the label checklist popover in the reading pane (shortcut "l"). */
  const labelPopoverOpen = ref(false)
  const folderSidebarOpen = ref(true)
  const narrow = useMediaQuery('(max-width: 1080px)')
  /** Focus the list search input (shortcut "/"). Incremented to trigger. */
  const focusSearchTick = ref(0)
  /** Show the first-run welcome guide again (Settings → About, command palette). */
  const welcomeTick = ref(0)
  /** Main area: mail (folders/list/reading pane) or the unified calendar. */
  const mainView = ref<'mail' | 'calendar'>(readMainView())
  watch(mainView, (v) => {
    try {
      localStorage.setItem('gomail.mainView', v)
    } catch {
      /* storage unavailable */
    }
  })
  /** Palette "New event": the calendar view opens the editor once it is mounted. */
  const newEventPending = ref(false)

  function openSettings(tab: SettingsTab = 'general') {
    settingsTab.value = tab
    settingsOpen.value = true
  }

  function anyDialogOpen(): boolean {
    return settingsOpen.value || paletteOpen.value || shortcutsOpen.value || addAccountOpen.value || !!document.querySelector('[role="dialog"][data-state="open"], [role="alertdialog"]')
  }

  return {
    settingsOpen, settingsTab, paletteOpen, shortcutsOpen, addAccountOpen, labelPopoverOpen, folderSidebarOpen, narrow, focusSearchTick,
    welcomeTick, mainView, newEventPending, openSettings, anyDialogOpen,
  }
})
