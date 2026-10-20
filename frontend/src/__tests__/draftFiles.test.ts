// Draft attachments survive a restart (bytes in IndexedDB, not localStorage), and a draft
// whose files can't be restored is never autosaved or sent without them.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const idb = vi.hoisted(() => ({ files: new Map<string, unknown[]>(), available: true }))
vi.mock('@/lib/draftFiles', () => ({
  saveDraftFiles: async (id: string, files: unknown[]) => {
    if (!idb.available) return false
    idb.files.set(id, JSON.parse(JSON.stringify(files)))
    return true
  },
  loadDraftFiles: async (id: string) => (idb.available ? idb.files.get(id) ?? null : null),
  deleteDraftFiles: async (id: string) => void idb.files.delete(id),
  pruneDraftFiles: async (keep: Set<string>) => {
    for (const k of [...idb.files.keys()]) if (!keep.has(k)) idb.files.delete(k)
  },
}))

const toasts: { kind: string; msg: string }[] = []
vi.mock('vue-sonner', () => {
  const rec = (kind: string) => (msg: string) => void toasts.push({ kind, msg })
  return { toast: Object.assign(rec('info'), { error: rec('error'), success: rec('success'), warning: rec('warning'), dismiss: () => {} }) }
})

import { api } from '@/lib/api'
import { useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'

const DRAFTS_KEY = 'gomail.drafts.v1'
const big = 'A'.repeat(2_000_000) // more than localStorage used to keep

async function restart() {
  setActivePinia(createPinia())
  const compose = useComposeStore()
  await compose.restoreFiles()
  return compose
}

beforeEach(() => {
  localStorage.clear()
  idb.files.clear()
  idb.available = true
  toasts.length = 0
  setActivePinia(createPinia())
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('draft attachments across restarts', () => {
  async function draftWithFile() {
    vi.useFakeTimers()
    vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:1')
    const compose = useComposeStore()
    const d = compose.compose([{ name: '', email: 'to@x.io' }])
    d.subject = 'Report'
    d.attachments.push({ id: 'att1', filename: 'report.pdf', mimeType: 'application/pdf', size: big.length, dataBase64: big })
    await vi.advanceTimersByTimeAsync(5000) // persisted locally and on the server
    vi.useRealTimers()
    return { compose, id: d.id }
  }

  it('keeps bytes out of localStorage and restores them', async () => {
    const { id } = await draftWithFile()
    const stored = JSON.parse(localStorage.getItem(DRAFTS_KEY)!)
    expect(stored[0].attachments).toEqual([{ id: 'att1', filename: 'report.pdf', mimeType: 'application/pdf', size: big.length }])
    const compose = await restart()
    const d = compose.drafts.find((x) => x.id === id)!
    expect(d.attachments[0].dataBase64).toBe(big)
    expect(d.missingFiles).toBeUndefined()
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    await compose.send(id, 0)
    expect(send.mock.calls[0][0]).toMatchObject({ attachments: [{ filename: 'report.pdf', dataBase64: big }], attachmentsComplete: true })
  })

  it('blocks autosave and send when files could not be restored', async () => {
    const { id } = await draftWithFile()
    idb.files.clear() // e.g. storage cleared or unavailable
    vi.useFakeTimers()
    const save = vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:1')
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    setActivePinia(createPinia())
    const compose = useComposeStore()
    await compose.restoreFiles()
    const d = compose.drafts.find((x) => x.id === id)!
    expect(d.missingFiles).toEqual(['report.pdf'])
    d.subject = 'Report v2' // an edit would normally autosave
    await vi.advanceTimersByTimeAsync(5000)
    expect(save).not.toHaveBeenCalled()
    await compose.send(id, 0)
    expect(send).not.toHaveBeenCalled()
    expect(toasts.some((t) => t.kind === 'error' && t.msg.includes('report.pdf'))).toBe(true)
    // Re-adding the file (or choosing to go on without it) unblocks.
    d.attachments.push({ id: 'att2', filename: 'report.pdf', mimeType: 'application/pdf', size: 3, dataBase64: 'UERG' })
    compose.resolveMissing(id, 'report.pdf')
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
    await compose.send(id, 0)
    expect(send).toHaveBeenCalledTimes(1)
  })

  it('save & close keeps a draft with missing files in the dock instead of overwriting the server copy', async () => {
    const { id } = await draftWithFile()
    idb.available = false
    const compose = await restart()
    const save = vi.spyOn(api, 'saveDraft')
    expect(await compose.saveAndClose(id)).toBe(true)
    expect(save).not.toHaveBeenCalled()
    expect(compose.drafts.some((x) => x.id === id)).toBe(true)
  })

  it('forgets stored files when the draft is closed', async () => {
    const { compose, id } = await draftWithFile()
    expect(idb.files.has(id)).toBe(true)
    await compose.discard(id)
    expect(idb.files.has(id)).toBe(false)
  })
})
