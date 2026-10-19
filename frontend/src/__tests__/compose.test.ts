import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import { signatureBlock } from '@/lib/format'
import { draftHasContent, useComposeStore } from '@/stores/compose'
import { useMailStore } from '@/stores/mail'

const DRAFTS_KEY = 'gomail.drafts.v1'

function base(over: Record<string, unknown> = {}) {
  return { to: [], cc: [], bcc: [], subject: '', html: '', attachments: [], ...over } as Parameters<typeof draftHasContent>[0]
}

describe('draft content', () => {
  it('ignores the auto-inserted signature', () => {
    expect(draftHasContent(base({ html: `<p></p>${signatureBlock('<p>Alex</p>')}` }))).toBe(false)
    expect(draftHasContent(base({ html: `<p>Hi</p>${signatureBlock('<p>Alex</p>')}` }))).toBe(true)
    expect(draftHasContent(base({ subject: 'Lunch?' }))).toBe(true)
    expect(draftHasContent(base({ to: [{ name: '', email: 'a@b.io' }] }))).toBe(true)
  })
})

describe('startup draft restore', () => {
  beforeEach(() => localStorage.clear())

  it('restores only drafts with content, minimised, and purges empty ones', () => {
    const d = (id: string, over: Record<string, unknown>) => ({ id, mode: 'new', accountId: 'a', to: [], cc: [], bcc: [], showCc: false, subject: '', html: '', attachments: [], replyToId: '', minimized: false, expanded: true, sending: true, version: 0, ...over })
    localStorage.setItem(DRAFTS_KEY, JSON.stringify([
      d('empty', { html: `<p></p>${signatureBlock('<p>Alex</p>')}` }),
      d('real', { subject: 'Plans', queued: true }),
    ]))
    setActivePinia(createPinia())
    const compose = useComposeStore()
    expect(compose.drafts.map((x) => x.id)).toEqual(['real'])
    expect(compose.drafts[0]).toMatchObject({ minimized: true, expanded: false, sending: false, queued: false })
    expect(JSON.parse(localStorage.getItem(DRAFTS_KEY)!).map((x: { id: string }) => x.id)).toEqual(['real'])
  })

  it('survives corrupt storage', () => {
    localStorage.setItem(DRAFTS_KEY, '{not json')
    setActivePinia(createPinia())
    expect(useComposeStore().drafts).toEqual([])
  })
})

describe('undo send timing', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.spyOn(useMailStore(), 'softRefresh').mockResolvedValue(undefined as never)
  })
  afterEach(() => vi.useRealTimers())

  function draft() {
    const compose = useComposeStore()
    const d = compose.compose([{ name: 'Priya', email: 'priya@acme.example' }])
    d.subject = 'Roadmap'
    d.html = '<p>Slides attached</p>'
    return { compose, id: d.id }
  }

  it('sends only after the delay', async () => {
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const { compose, id } = draft()
    await compose.send(id, 5)
    expect(compose.drafts.find((d) => d.id === id)?.queued).toBe(true)
    await vi.advanceTimersByTimeAsync(4900)
    expect(send).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(200)
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0][0]).toMatchObject({ subject: 'Roadmap', to: [{ email: 'priya@acme.example' }] })
    expect(compose.drafts.find((d) => d.id === id)).toBeUndefined()
  })

  it('undo cancels the send and reopens the draft', async () => {
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const { compose, id } = draft()
    await compose.send(id, 5)
    await vi.advanceTimersByTimeAsync(3000)
    compose.undoSend(id)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(send).not.toHaveBeenCalled()
    expect(compose.drafts.find((d) => d.id === id)).toMatchObject({ queued: false, minimized: false })
  })

  it('sends immediately without a delay and validates recipients', async () => {
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const { compose, id } = draft()
    compose.drafts.find((d) => d.id === id)!.to = [{ name: '', email: 'nope' }]
    await compose.send(id, 0)
    expect(send).not.toHaveBeenCalled()
    compose.drafts.find((d) => d.id === id)!.to = [{ name: '', email: 'ok@x.io' }]
    await compose.send(id, 0)
    expect(send).toHaveBeenCalledTimes(1)
  })

  it('keeps the draft open when sending fails', async () => {
    vi.spyOn(api, 'sendMessage').mockRejectedValue(new Error('SMTP down'))
    const { compose, id } = draft()
    await compose.send(id, 2)
    await vi.advanceTimersByTimeAsync(2100)
    expect(compose.drafts.find((d) => d.id === id)).toMatchObject({ sending: false, queued: false, minimized: false })
  })
})
