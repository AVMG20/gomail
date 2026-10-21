import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod, isWails, setWindowChrome } from '@/lib/api'
import { setClockPreference } from '@/lib/calendar'
import { applyTheme, themeFromSettings } from '@/lib/theme'
import type { AIModel, AppInfo, DefaultPrompts, Label, Settings, UpdateInfo } from '@/lib/types'
import { labelInMailbox, labelsForAccount as forAccount, labelsForAccounts as forAccounts, nameTaken, sameNameIds, viewLabelId } from '@/lib/labelScope'

export const JEV_MODELS: AIModel[] = [
  { id: 'typesafe/jev-1.13', name: 'Jev 1.13 (pinned)', description: 'Decision model for labeling, pinned version', contextLength: 0, promptPrice: 0, completionPrice: 0 },
  { id: '~typesafe/jev-latest', name: 'Jev latest', description: 'Always the newest Jev release', contextLength: 0, promptPrice: 0, completionPrice: 0 },
]

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings | null>(null)
  const saving = ref(false)
  const models = shallowRef<AIModel[]>([])
  const modelsLoading = ref(false)
  const modelsError = ref('')
  const defaults = ref<DefaultPrompts>({ grammar: '', classify: '' })
  const appInfo = ref<AppInfo | null>(null)

  const labels = computed<Label[]>(() => settings.value?.labels ?? [])
  const labelMap = computed(() => new Map(labels.value.map((l) => [l.id, l])))
  /** Muted mailboxes (no new-mail notifications): their unread count shows on no badge and in no All inboxes total. */
  const mutedMailboxes = computed(() => new Set(settings.value?.mutedMailboxes ?? []))
  /** The labels that can be offered: imported ones (Gmail labels, Outlook categories) only while they are shown. */
  const offered = computed(() => (settings.value?.importProviderLabels === false ? labels.value.filter((l) => l.source !== 'provider') : labels.value))
  /** Labels of one mailbox ("" = the unified view: one label per name). Use this wherever labels are offered. */
  const labelsForAccount = (accountId: string) => forAccount(offered.value, accountId)
  /** Labels of a selection: its mailbox's, or one per name for mail of several mailboxes. */
  const labelsForAccounts = (accountIds: Iterable<string>) => forAccounts(offered.value, accountIds)
  /** A mailbox's label with the same name as label id (the label itself when it is the mailbox's). */
  const labelInAccountOf = (id: string, accountId: string) => {
    const l = labelMap.value.get(id)
    return l && labelInMailbox(labels.value, l, accountId)
  }
  /** The ids of every label with the same name as one of these (the unified view's groups). */
  const labelGroupIds = (ids: string[]) => sameNameIds(labels.value, ids)
  /** The id a view's label filter keeps for a label id (see lib/labelScope viewLabelId). */
  const viewLabel = (id: string, accountId: string) => viewLabelId(labels.value, id, accountId)
  const aiReady = computed(() => !!settings.value?.openRouterKeySet)
  const grammarReady = computed(() => aiReady.value && hasMethod('ImproveGrammar'))

  async function load() {
    settings.value = await api.getSettings()
    void loadDefaults()
  }

  /** Re-read label definitions after a labels:changed event, without clobbering pending edits. */
  async function refreshLabels() {
    if (saveTimer || saving.value || !settings.value) return
    try {
      const fresh = await api.getSettings()
      if (saveTimer || saving.value || !settings.value) return
      settings.value = { ...settings.value, labels: fresh.labels, importProviderLabels: fresh.importProviderLabels, labelsRev: fresh.labelsRev }
    } catch {
      /* ignore */
    }
  }

  /** Re-read every setting after another computer changed them (settings:changed). Edits not
   *  saved yet are kept: they are newer. */
  async function reload() {
    if (!settings.value) return
    await inflight
    try {
      const fresh = await api.getSettings()
      if (!settings.value) return
      const keep: Partial<Settings> = {}
      for (const k of pendingKeys) Object.assign(keep, { [k]: settings.value[k] })
      settings.value = { ...fresh, ...keep }
    } catch {
      /* ignore */
    }
  }

  async function loadDefaults() {
    try {
      defaults.value = await api.getDefaultPrompts()
    } catch {
      /* optional binding */
    }
  }

  /** Latest update check (next backend pass: CheckForUpdate + update:available). */
  const updateInfo = ref<UpdateInfo | null>(null)
  const checkingUpdate = ref(false)
  async function checkForUpdate(manual = false) {
    if (!hasMethod('CheckForUpdate')) return
    if (!manual && settings.value?.checkUpdates === false) return
    checkingUpdate.value = true
    try {
      updateInfo.value = await api.checkForUpdate()
    } catch {
      if (manual) throw new Error('Couldn’t check for updates')
    } finally {
      checkingUpdate.value = false
    }
  }

  async function loadAppInfo() {
    try {
      appInfo.value = await api.getAppInfo()
      if (isWails() && (appInfo.value?.os === 'darwin' || appInfo.value?.platform?.startsWith('darwin'))) setWindowChrome('mac')
    } catch {
      appInfo.value = null
    }
  }

  let saveTimer: ReturnType<typeof setTimeout> | null = null
  /** Settings changed here since the last save started. */
  let pendingKeys = new Set<keyof Settings>()
  /** Callers waiting for the next save (every debounced update resolves, not just the last). */
  let waiters: (() => void)[] = []
  /** Saves run one at a time and always send the latest state, so the last write wins. */
  let inflight: Promise<void> = Promise.resolve()

  async function save() {
    const done = waiters
    waiters = []
    pendingKeys = new Set()
    saving.value = true
    try {
      await api.saveSettings(settings.value!)
    } catch (e) {
      toast.error('Could not save settings', { description: errorMessage(e) })
    } finally {
      saving.value = !!saveTimer
      done.forEach((r) => r())
    }
  }

  /** Merge a patch locally (instantly) and persist it with a short debounce. */
  function update(patch: Partial<Settings>, delay = 350): Promise<void> {
    if (!settings.value) return Promise.resolve()
    settings.value = { ...settings.value, ...patch }
    for (const k of Object.keys(patch)) pendingKeys.add(k as keyof Settings)
    if (saveTimer) clearTimeout(saveTimer)
    const p = new Promise<void>((resolve) => waiters.push(resolve))
    saveTimer = setTimeout(() => {
      saveTimer = null
      inflight = inflight.then(save)
    }, delay)
    return p
  }

  /** Wait until every pending change is written. */
  async function flush() {
    if (saveTimer) {
      clearTimeout(saveTimer)
      saveTimer = null
      inflight = inflight.then(save)
    }
    await inflight
  }

  async function setOpenRouterKey(key: string) {
    await api.setOpenRouterKey(key)
    if (settings.value) settings.value = { ...settings.value, openRouterKeySet: !!key.trim() }
  }

  async function loadModels(force = false) {
    if (modelsLoading.value || (models.value.length && !force)) return
    modelsLoading.value = true
    modelsError.value = ''
    try {
      const list = await api.listAIModels(force)
      list.sort((a, b) => a.name.localeCompare(b.name))
      models.value = list
    } catch (e) {
      modelsError.value = errorMessage(e)
    } finally {
      modelsLoading.value = false
    }
  }

  watch(() => settings.value?.calendarTimeFormat, (f) => setClockPreference(f), { immediate: true })

  // Live-apply appearance settings and cache them for the pre-paint script.
  watch(
    () => settings.value && [settings.value.theme, settings.value.density, settings.value.primaryColor, settings.value.radius],
    () => {
      if (settings.value) applyTheme(themeFromSettings(settings.value))
    },
  )

  // ---- label edits shared by Settings → Labels and the sidebar's right-click menu
  function patchLabel(id: string, p: Partial<Label>) {
    void update({ labels: labels.value.map((l) => (l.id === id ? { ...l, ...p } : l)) }, 500)
  }
  /** Names must not be empty, and two labels of a mailbox with the same name would be
   *  indistinguishable: a taken name gets a number. Returns the name used and whether it had to change. */
  function renameLabel(id: string, raw: string): { name: string; deduped: boolean } {
    const name = raw.trim() || 'Untitled label'
    const account = labelMap.value.get(id)?.accountIds?.[0] ?? ''
    const taken = (n: string) => nameTaken(labels.value, account, n, id)
    let final = name
    for (let n = 2; taken(final); n++) final = `${name} ${n}`
    if (final !== labels.value.find((l) => l.id === id)?.name) patchLabel(id, { name: final })
    return { name: final, deduped: final !== name }
  }
  function removeLabel(id: string) {
    void update({ labels: labels.value.filter((l) => l.id !== id) }, 500)
  }

  return {
    patchLabel, renameLabel, removeLabel,
    settings, saving, models, modelsLoading, modelsError, defaults, appInfo, updateInfo, checkingUpdate, checkForUpdate,
    labels, labelMap, mutedMailboxes, labelsForAccount, labelsForAccounts, labelInAccountOf, labelGroupIds, viewLabel, aiReady, grammarReady,
    load, reload, refreshLabels, loadDefaults, loadAppInfo, update, flush, setOpenRouterKey, loadModels,
  }
})
