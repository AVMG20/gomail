import { defineStore } from 'pinia'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage } from '@/lib/api'
import type { ClassifyJobProgress, ClassifyJobState, ListQuery } from '@/lib/types'
import { useMailStore } from './mail'
import { useSettingsStore } from './settings'
import { useAccountsStore } from './accounts'
import { useUiStore } from './ui'

export interface BulkRequest {
  /** null = explicit ids (selection) */
  query: Partial<ListQuery> | null
  ids: string[]
  title: string
}

export const useAiStore = defineStore('ai', () => {
  const mail = useMailStore()
  const settings = useSettingsStore()
  const accounts = useAccountsStore()

  /** Most recent AI scores per message, shown in the label popover. */
  const scores = ref<Record<string, Record<string, number>>>({})
  const labeling = ref<Set<string>>(new Set())
  const job = ref<ClassifyJobState | null>(null)
  const bulkRequest = ref<BulkRequest | null>(null)

  function labelName(id: string) {
    return settings.labelMap.get(id)?.name ?? id
  }

  /** "Label with AI" on one or a few messages. */
  async function labelMessages(ids: string[]) {
    if (!ids.length) return
    if (!settings.aiReady) return needsSetup()
    labeling.value = new Set([...labeling.value, ...ids])
    try {
      const results = await api.classifyMessages(ids)
      const nextScores = { ...scores.value }
      const before = new Map(ids.map((id) => [id, mail.byId.get(id)?.labels ?? []]))
      for (const r of results) {
        nextScores[r.messageId] = r.scores
        mail.patchLocal([r.messageId], (m) => ({
          labels: [...m.labels.filter((l) => !r.removed.includes(l)), ...r.added.filter((l) => !m.labels.includes(l))],
          labelDetails: [
            ...(m.labelDetails ?? []).filter((d) => !r.removed.includes(d.labelId) && !r.added.includes(d.labelId)),
            ...r.added.map((l) => ({ labelId: l, source: 'ai', confidence: r.scores[l] ?? 0 })),
          ],
        }))
        mail.invalidateThread(r.messageId)
      }
      scores.value = nextScores
      const added = results.flatMap((r) => r.added)
      const removed = results.flatMap((r) => r.removed)
      if (!added.length && !removed.length && results.length && results.every((r) => r.skipped)) {
        toast('Already up to date', { description: 'Nothing changed since the last AI run, so this was free.' })
      } else if (!added.length && !removed.length) {
        toast('No label matched', { description: ids.length === 1 ? 'Jev was not confident about any label for this mail.' : undefined })
      } else {
        const parts: string[] = []
        if (ids.length === 1) {
          const r = results[0]
          if (r?.added.length) parts.push(`Added ${r.added.map((l) => `${labelName(l)} · ${Math.round((r.scores[l] ?? 0) * 100)}%`).join(', ')}`)
          if (r?.removed.length) parts.push(`Removed ${r.removed.map(labelName).join(', ')}`)
        } else {
          if (added.length) parts.push(`Added ${added.length} label${added.length === 1 ? '' : 's'}`)
          if (removed.length) parts.push(`removed ${removed.length}`)
        }
        toast(parts.join(' · '), {
          action: {
            label: 'Undo',
            onClick: async () => {
              for (const r of results) {
                const prev = before.get(r.messageId) ?? []
                for (const l of r.added) if (!prev.includes(l)) await api.removeLabel([r.messageId], l)
                for (const l of r.removed) await api.addLabel([r.messageId], l)
              }
              void mail.softRefresh()
            },
          },
        })
      }
      accounts.refreshCounts()
    } catch (e) {
      toast.error('Labeling failed', { description: errorMessage(e) })
    } finally {
      const next = new Set(labeling.value)
      ids.forEach((id) => next.delete(id))
      labeling.value = next
    }
  }

  /** Every "Label with AI" entry stays visible; without a key it explains what's needed. */
  function needsSetup() {
    toast('AI labeling needs an OpenRouter key', { description: 'Add one in Settings → AI (it takes a minute).', action: { label: 'Set up AI', onClick: () => useUiStore().openSettings('ai') } })
  }

  function requestBulk(req: BulkRequest) {
    if (!settings.aiReady) return needsSetup()
    bulkRequest.value = req
  }

  async function startBulk(req: BulkRequest, onlyUnlabeled: boolean, includeTrashDrafts: boolean) {
    bulkRequest.value = null
    try {
      if (req.query) {
        const started = await api.classifyQuery(req.query, onlyUnlabeled, includeTrashDrafts)
        // Progress events may already have arrived for this job; keep them.
        if (job.value?.jobId !== started.jobId) job.value = { jobId: started.jobId, done: 0, total: started.total, labeled: 0, skipped: 0, cost: 0 }
      } else {
        // Explicit selection: classify in place, in small batches, with local progress.
        const ids = req.ids
        const id = `local_${Date.now()}`
        job.value = { jobId: id, done: 0, total: ids.length, labeled: 0, skipped: 0, cost: 0 }
        for (let i = 0; i < ids.length; i += 10) {
          if (job.value?.jobId !== id || job.value.cancelled) break
          const batch = ids.slice(i, i + 10)
          const results = await api.classifyMessages(batch)
          const j = job.value
          if (!j) break
          for (const r of results) {
            if (r.skipped) j.skipped++
            else if (r.added.length || r.removed.length) j.labeled++
            j.cost += r.cost
            scores.value[r.messageId] = r.scores
          }
          j.done = Math.min(ids.length, i + batch.length)
          void mail.softRefresh()
        }
        if (job.value?.jobId === id) job.value = { ...job.value, finished: true }
        void mail.softRefresh()
        accounts.refreshCounts()
      }
    } catch (e) {
      toast.error('Could not start labeling', { description: errorMessage(e) })
      job.value = null
    }
  }

  async function cancelJob() {
    const j = job.value
    if (!j) return
    j.cancelled = true
    if (!j.jobId.startsWith('local_')) await api.cancelClassify(j.jobId).catch(() => {})
  }

  /** Events for another job while one is running, e.g. the per-batch "selection" events of a local job. */
  function isForeign(p: Partial<ClassifyJobProgress>) {
    const cur = job.value
    return !!(cur && p.jobId && cur.jobId !== p.jobId && !cur.finished)
  }

  function onProgress(p: Partial<ClassifyJobProgress>) {
    if (isForeign(p)) return
    const cur = job.value
    job.value = {
      jobId: p.jobId ?? cur?.jobId ?? 'job',
      done: p.done ?? cur?.done ?? 0,
      total: p.total ?? cur?.total ?? 0,
      labeled: p.labeled ?? cur?.labeled ?? 0,
      skipped: p.skipped ?? cur?.skipped ?? 0,
      failed: p.failed ?? cur?.failed ?? 0,
      cost: p.cost ?? cur?.cost ?? 0,
      finished: false,
      cancelled: cur?.cancelled,
    }
  }

  function onFinished(p: Partial<ClassifyJobProgress>) {
    if (isForeign(p)) return
    onProgress(p)
    if (job.value) job.value = { ...job.value, finished: true, cancelled: p.cancelled ?? job.value.cancelled, error: p.error }
    void mail.softRefresh()
    accounts.refreshCounts()
  }

  function dismissJob() {
    job.value = null
  }

  return { scores, labeling, job, bulkRequest, labelMessages, requestBulk, startBulk, cancelJob, onProgress, onFinished, dismissJob }
})
