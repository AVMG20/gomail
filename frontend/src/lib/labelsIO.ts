// Label export/import (share Jev label setups). Imported files are untrusted:
// every field is type-checked, trimmed, length-capped and colors must be hex.
import { LABEL_COLORS, safeColor } from './color'
import type { Label, Settings } from './types'

export interface LabelExport {
  app: 'gomail'
  kind: 'labels'
  version: 1
  labels: { name: string; description: string; color: string }[]
  classifyInstructions?: string
  grammarSystemPrompt?: string
}

export const MAX_IMPORT_BYTES = 1_000_000
const MAX_LABELS = 200
const MAX_NAME = 64
const MAX_DESCRIPTION = 1_000
const MAX_PROMPT = 10_000

export function buildLabelExport(labels: Label[], s: Pick<Settings, 'classifyInstructions' | 'grammarSystemPrompt'>): LabelExport {
  return {
    app: 'gomail', kind: 'labels', version: 1,
    labels: labels.map(({ name, description, color }) => ({ name, description, color })),
    classifyInstructions: s.classifyInstructions || undefined,
    grammarSystemPrompt: s.grammarSystemPrompt || undefined,
  }
}

export type ImportResult =
  | { ok: false; error: string }
  | { ok: true; patch: Partial<Settings>; added: number; updated: number; skipped: number }

/** Merge an export file into the current labels by name (case-insensitive). With accountId, into
 *  that mailbox's labels: new labels belong to it, and only its labels are matched by name. */
export function mergeLabelImport(text: string, current: Label[], newId: (i: number) => string, accountId = ''): ImportResult {
  if (text.length > MAX_IMPORT_BYTES) return { ok: false, error: 'That file is too large to be a label export' }
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    return { ok: false, error: 'That file isn’t valid JSON' }
  }
  const obj = data && typeof data === 'object' ? (data as Record<string, unknown>) : null
  const list = obj && Array.isArray(obj.labels) ? obj.labels : null
  if (!list) return { ok: false, error: 'No labels found in that file' }

  const next = [...current]
  const seen = new Set<string>()
  let added = 0
  let updated = 0
  let skipped = 0
  for (const raw of list.slice(0, MAX_LABELS)) {
    const l = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
    const name = typeof l.name === 'string' ? l.name.trim().replace(/\s+/g, ' ').slice(0, MAX_NAME) : ''
    const key = name.toLowerCase()
    if (!name || seen.has(key)) {
      skipped++
      continue
    }
    seen.add(key)
    const description = typeof l.description === 'string' ? l.description.slice(0, MAX_DESCRIPTION) : undefined
    const color = safeColor(l.color, LABEL_COLORS[next.length % LABEL_COLORS.length])
    const i = next.findIndex((x) => x.name.trim().toLowerCase() === key && (!accountId || x.source === 'system' || !!x.accountIds?.includes(accountId)))
    if (i >= 0) {
      next[i] = { ...next[i], description: description ?? next[i].description, color }
      updated++
    } else {
      next.push({ id: newId(added), name, description: description ?? '', color, source: 'user', ...(accountId ? { accountIds: [accountId] } : {}) })
      added++
    }
  }
  skipped += Math.max(0, list.length - MAX_LABELS)
  const patch: Partial<Settings> = { labels: next }
  if (obj && typeof obj.classifyInstructions === 'string') patch.classifyInstructions = obj.classifyInstructions.slice(0, MAX_PROMPT)
  if (obj && typeof obj.grammarSystemPrompt === 'string') patch.grammarSystemPrompt = obj.grammarSystemPrompt.slice(0, MAX_PROMPT)
  return { ok: true, patch, added, updated, skipped }
}
