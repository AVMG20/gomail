import { defineStore } from 'pinia'
import { computed, ref, shallowRef, watch } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { applyTheme, themeFromSettings } from '@/lib/theme'
import type { AIModel, AppInfo, DefaultPrompts, Label, Settings } from '@/lib/types'

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
  const aiReady = computed(() => !!settings.value?.openRouterKeySet)
  const grammarReady = computed(() => aiReady.value && hasMethod('ImproveGrammar'))

  async function load() {
    settings.value = await api.getSettings()
    void loadDefaults()
  }

  async function loadDefaults() {
    try {
      defaults.value = await api.getDefaultPrompts()
    } catch {
      /* optional binding */
    }
  }

  async function loadAppInfo() {
    try {
      appInfo.value = await api.getAppInfo()
    } catch {
      appInfo.value = null
    }
  }

  let saveTimer: ReturnType<typeof setTimeout> | null = null
  let pending: Promise<void> | null = null

  /** Merge a patch locally (instantly) and persist it with a short debounce. */
  function update(patch: Partial<Settings>, delay = 350): Promise<void> {
    if (!settings.value) return Promise.resolve()
    settings.value = { ...settings.value, ...patch }
    if (saveTimer) clearTimeout(saveTimer)
    pending = new Promise<void>((resolve) => {
      saveTimer = setTimeout(async () => {
        saveTimer = null
        saving.value = true
        try {
          await api.saveSettings(settings.value!)
        } catch (e) {
          toast.error('Could not save settings', { description: errorMessage(e) })
        } finally {
          saving.value = false
          resolve()
        }
      }, delay)
    })
    return pending
  }

  async function flush() {
    if (pending) await pending
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
      const list = await api.listAIModels()
      list.sort((a, b) => a.name.localeCompare(b.name))
      models.value = list
    } catch (e) {
      modelsError.value = errorMessage(e)
    } finally {
      modelsLoading.value = false
    }
  }

  // Live-apply appearance settings and cache them for the pre-paint script.
  watch(
    () => settings.value && [settings.value.theme, settings.value.density, settings.value.primaryColor, settings.value.radius],
    () => {
      if (settings.value) applyTheme(themeFromSettings(settings.value))
    },
  )

  return {
    settings, saving, models, modelsLoading, modelsError, defaults, appInfo,
    labels, labelMap, aiReady, grammarReady,
    load, loadDefaults, loadAppInfo, update, flush, setOpenRouterKey, loadModels,
  }
})
