<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { Toaster } from '@/components/ui/sonner'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import { Skeleton } from '@/components/ui/skeleton'
import AccountRail from '@/components/mail/AccountRail.vue'
import FolderSidebar from '@/components/mail/FolderSidebar.vue'
import MessageList from '@/components/mail/MessageList.vue'
import Onboarding from '@/components/dialogs/Onboarding.vue'
import LinkConfirmDialog from '@/components/dialogs/LinkConfirmDialog.vue'
import TooltipLayer from '@/components/common/TooltipLayer.vue'
import { useBackendEvents } from '@/composables/useBackendEvents'
import { useShortcuts } from '@/composables/useShortcuts'
import { api, errorMessage, hasMethod, isWails, missingMethods, readLocal } from '@/lib/api'
import { useAccountsStore } from '@/stores/accounts'
import { useAiStore } from '@/stores/ai'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'
import { useCleanupStore } from '@/stores/cleanup'
import ErrorBoundary from '@/components/common/ErrorBoundary.vue'

// Heavier, not-always-needed UI is code-split.
const ReadingPane = defineAsyncComponent(() => import('@/components/mail/ReadingPane.vue'))
const ComposeDock = defineAsyncComponent(() => import('@/components/compose/ComposeDock.vue'))
const SettingsDialog = defineAsyncComponent(() => import('@/components/settings/SettingsDialog.vue'))
const CalendarView = defineAsyncComponent(() => import('@/components/calendar/CalendarView.vue'))
const CommandPalette = defineAsyncComponent(() => import('@/components/dialogs/CommandPalette.vue'))
const AddAccountDialog = defineAsyncComponent(() => import('@/components/dialogs/AddAccountDialog.vue'))
const ShortcutsDialog = defineAsyncComponent(() => import('@/components/dialogs/ShortcutsDialog.vue'))
const OnboardingWizard = defineAsyncComponent(() => import('@/components/dialogs/OnboardingWizard.vue'))
const ClassifyProgress = defineAsyncComponent(() => import('@/components/dialogs/ClassifyProgress.vue'))
const ClassifyConfirmDialog = defineAsyncComponent(() => import('@/components/dialogs/ClassifyConfirmDialog.vue'))
const SenderCleanup = defineAsyncComponent(() => import('@/components/mail/SenderCleanup.vue'))

const settings = useSettingsStore()
const accounts = useAccountsStore()
const mail = useMailStore()
const ui = useUiStore()
const compose = useComposeStore()
const ai = useAiStore()
const cleanup = useCleanupStore()

const booted = ref(false)
const onboarded = ref(readLocal('onboarded', false))
watch(() => ui.welcomeTick, () => {
  ui.settingsOpen = false
  onboarded.value = false
})
const bootError = ref('')

useShortcuts()
useBackendEvents()

// Mount lazily-loaded dialogs on first use, then keep them mounted for instant reopen.
const used = ref({ settings: false, palette: false, addAccount: false, shortcuts: false, classify: false, cleanup: false })
watch(() => ui.settingsOpen, (v) => v && (used.value.settings = true))
watch(() => ui.paletteOpen, (v) => v && (used.value.palette = true))
watch(() => ui.addAccountOpen, (v) => v && (used.value.addAccount = true))
watch(() => ui.shortcutsOpen, (v) => v && (used.value.shortcuts = true))
watch(() => ai.bulkRequest, (v) => v && (used.value.classify = true))
watch(() => cleanup.open, (v) => v && (used.value.cleanup = true))

onMounted(async () => {
  if (isWails()) {
    const missing = missingMethods()
    if (missing.length) console.warn(`[gomail] backend is missing bound methods: ${missing.join(', ')}`)
  }
  try {
    await Promise.all([settings.load(), accounts.load()])
    mail.ensureValidMailbox()
    await accounts.loadLabelCounts(labelScope())
    void mail.reload()
    // mailto: link that started the app (compose:open fired before we were listening).
    if (hasMethod('GetPendingCompose')) {
      void api.getPendingCompose().then((p) => {
        if (!p) return
        const list = (v: unknown) => (Array.isArray(v) ? v : [])
        compose.composeWith({ to: list(p.to), cc: list(p.cc), bcc: list(p.bcc), subject: p.subject ?? '', body: p.body ?? '' })
      }).catch(() => {})
    }
  } catch (e) {
    bootError.value = errorMessage(e)
  } finally {
    booted.value = true
  }
  // Warm the lazily loaded chunks once the UI is idle (WebKit has no requestIdleCallback).
  void import('@/components/mail/ReadingPane.vue')
  setTimeout(() => {
    void import('@/components/mail/LabelFilter.vue')
    void import('@/components/mail/AccountMenu.vue')
    void import('@/components/mail/RowContextMenu.vue')
    void import('@/components/mail/BulkBar.vue')
  }, 1200)
  setTimeout(() => void settings.checkForUpdate(), 4000)
})

/** Sidebar label counts follow the box on screen (Inbox, All Mail, …), like the label filter does. */
function labelScope() {
  const { accountId, folderId, folderKind } = mail.query
  return { accountId, folderId, folderKind }
}
watch(() => JSON.stringify(labelScope()), () => void accounts.loadLabelCounts(labelScope()))
watch(() => settings.settings?.zoom, (z) => z && void api.setZoom(z).catch(() => {}))

// Narrow windows: collapse the folder sidebar first (the rail stays).
watch(() => ui.narrow, (n) => (ui.folderSidebarOpen = !n), { immediate: true })

const paneOff = computed(() => settings.settings?.readingPane === 'off')
const showCalendar = computed(() => ui.mainView === 'calendar' && hasMethod('ListCalendars'))
const accountColor = computed(() => mail.mailboxAccount?.color ?? 'var(--muted-foreground)')
const toasterTheme = computed(() => (settings.settings?.theme === 'light' || settings.settings?.theme === 'dark' ? settings.settings.theme : 'system') as 'light' | 'dark' | 'system')

const windowTitle = computed(() => {
  if (showCalendar.value) return 'Calendar'
  const a = mail.mailboxAccount
  return [mail.mailboxName, a?.email].filter(Boolean).join(' — ') || 'gomail'
})
watch([() => mail.mailboxName, showCalendar], ([name, cal]) => {
  document.title = cal ? 'Calendar — gomail' : name ? `${name} — gomail` : 'gomail'
}, { immediate: true })
</script>

<template>
  <div class="flex h-full w-full flex-col" :style="{ '--account': accountColor }">
    <!-- macOS: a real title bar for the traffic lights, so pane headers line up below it. -->
    <header
      data-drag
      class="titlebar bg-sidebar border-sidebar-border text-muted-foreground relative hidden h-[var(--titlebar)] shrink-0 items-center justify-center border-b px-20 text-xs font-medium select-none"
      @dblclick="api.toggleMaximise()"
    >
      <span class="truncate">{{ windowTitle }}</span>
    </header>
    <div class="relative flex min-h-0 w-full flex-1 overflow-hidden">
      <AccountRail />

      <div v-if="!booted" class="flex flex-1">
        <div class="bg-sidebar border-sidebar-border w-60 space-y-2 border-r p-4">
          <Skeleton v-for="i in 7" :key="i" class="h-7 w-full" />
        </div>
        <div class="flex-1" />
      </div>

      <div v-else-if="bootError" class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
        <p class="font-medium">gomail couldn’t start</p>
        <p class="text-muted-foreground max-w-md text-sm">{{ bootError }}</p>
      </div>

      <OnboardingWizard v-else-if="!accounts.accounts.length && !onboarded" embedded @done="onboarded = true" />
      <Onboarding v-else-if="!accounts.accounts.length" />

      <template v-else>
        <!-- The unified calendar (lazy chunk: FullCalendar never loads for mail-only use). -->
        <ErrorBoundary v-if="showCalendar" name="calendar"><CalendarView /></ErrorBoundary>
        <div v-show="!showCalendar" class="contents">
        <div
          v-if="ui.folderSidebarOpen"
          :class="ui.narrow ? 'absolute inset-y-0 left-14 z-30 w-60 shadow-2xl' : 'w-60 shrink-0'"
        >
          <FolderSidebar />
        </div>
        <div v-if="ui.narrow && ui.folderSidebarOpen" class="absolute inset-y-0 right-0 left-14 z-20 bg-black/10" @click="ui.folderSidebarOpen = false" />

        <!-- Reading pane off: full-width list; the message opens in place with a back button. -->
        <div v-if="paneOff" class="min-w-0 flex-1">
          <MessageList v-show="!mail.openId" />
          <ErrorBoundary v-if="mail.openId" name="reading pane" :reset-key="mail.openId"><ReadingPane show-back /></ErrorBoundary>
        </div>
        <!-- Pixel limits keep the list toolbar (and the bulk bar) from clipping its buttons. -->
        <ResizablePanelGroup v-else direction="horizontal" auto-save-id="gomail.panes.v2" class="min-w-0 flex-1">
          <ResizablePanel size-unit="px" :default-size="480" :min-size="400" :max-size="760" class="min-w-0">
            <MessageList />
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel size-unit="px" :min-size="380" class="min-w-0">
            <ErrorBoundary name="reading pane" :reset-key="mail.openId ?? ''"><ReadingPane /></ErrorBoundary>
          </ResizablePanel>
        </ResizablePanelGroup>
        </div>
      </template>
    </div>

    <ComposeDock v-if="compose.drafts.length" />
    <ClassifyProgress v-if="ai.job" />
    <OnboardingWizard v-if="booted && !bootError && accounts.accounts.length && !onboarded" @done="onboarded = true" />
    <SettingsDialog v-if="used.settings" />
    <CommandPalette v-if="used.palette" />
    <AddAccountDialog v-if="used.addAccount" />
    <ShortcutsDialog v-if="used.shortcuts" />
    <ClassifyConfirmDialog v-if="used.classify" />
    <SenderCleanup v-if="used.cleanup" />
    <LinkConfirmDialog />
    <Toaster position="bottom-center" :theme="toasterTheme" close-button />
    <TooltipLayer />
  </div>
</template>
