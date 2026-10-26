import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/lib/api'
import type { Account, Message, MessageSummary } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useComposeStore } from '@/stores/compose'
import { OUTGOING_PREFIX, useMailStore } from '@/stores/mail'

const ME = 'me@acme.example'

function msg(id: string, over: Partial<Message> = {}): Message {
  return {
    id, accountId: 'a', remoteId: id, threadId: 't1', folderIds: ['a:inbox'], from: { name: 'Priya', email: 'priya@acme.example' }, to: [{ name: '', email: ME }],
    subject: 'Roadmap', snippet: '', date: '2026-10-20T10:00:00Z', unread: false, starred: false, hasAttachments: false, labels: [],
    cc: [], bcc: [], replyTo: [], bodyHtml: '<p>Hi</p>', bodyText: 'Hi', attachments: [], messageId: `<${id}@x>`, inReplyTo: '', references: '', ...over,
  }
}

function row(m: Message, threadCount: number): MessageSummary {
  return { ...m, threadCount, conversationId: `c:${m.id}`, participants: ['Priya'] }
}

describe('a reply shows in its conversation from Send on', () => {
  let thread: Message[]
  beforeEach(async () => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.useFakeTimers()
    useAccountsStore().accounts = [{ id: 'a', email: ME, displayName: 'Me' } as Account]
    thread = [msg('m1')]
    vi.spyOn(api, 'getThread').mockImplementation(async () => thread)
    vi.spyOn(api, 'listMessages').mockImplementation(async () => ({ messages: [row(thread[thread.length - 1], thread.length)], nextCursor: '', total: 1 }))
    const mail = useMailStore()
    mail.messages = [row(thread[0], 1)]
    await mail.loadThread('m1')
  })
  afterEach(() => vi.useRealTimers())

  async function reply() {
    const compose = useComposeStore()
    const d = (await compose.respond('reply', thread[0]))!
    d.html = '<p>Sounds good</p>'
    return { compose, mail: useMailStore(), id: d.id }
  }

  it('shows it right away and takes it back on undo', async () => {
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const { compose, mail, id } = await reply()
    await compose.send(id, 5)
    expect(mail.outbox.map((o) => o.message)).toMatchObject([{ id: `${OUTGOING_PREFIX}${id}`, threadId: 't1', from: { email: ME }, snippet: 'Sounds good' }])
    expect(mail.messages[0]).toMatchObject({ threadCount: 2, participants: ['Priya', 'me'] })
    compose.undoSend(id)
    expect(mail.outbox).toEqual([])
    await vi.advanceTimersByTimeAsync(10)
    expect(mail.messages[0].threadCount).toBe(1)
  })

  it('goes again with an error when the send fails', async () => {
    vi.spyOn(api, 'sendMessage').mockRejectedValue(new Error('offline'))
    const { compose, mail, id } = await reply()
    await compose.send(id, 0)
    expect(mail.outbox).toEqual([])
    expect(compose.drafts.find((d) => d.id === id)).toMatchObject({ sending: false, minimized: false })
  })

  it('stays until the real message syncs in, then the conversation has it', async () => {
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const { compose, mail, id } = await reply()
    await compose.send(id, 0)
    expect(mail.outbox[0]?.sent).toBeTruthy()
    // A refresh before sync: the row keeps counting it.
    await mail.softRefresh()
    expect(mail.messages[0].threadCount).toBe(2)
    // Synced: the account's mail changed, the conversation is fetched again and has the real one.
    thread = [...thread, msg('m2', { from: { name: 'Me', email: ME }, folderIds: ['a:sent'], date: new Date().toISOString() })]
    mail.threadsChanged({ accountId: 'a' })
    expect(mail.outbox[0]?.checks).toBe(1)
    expect(mail.cachedThread('m1')).toBeUndefined()
    await mail.loadThread('m1')
    expect(mail.outbox).toEqual([])
    await mail.softRefresh()
    expect(mail.messages[0].threadCount).toBe(2)
  })

  it('isn’t taken for a reply sent a minute earlier', async () => {
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const earlier = msg('m0', { from: { name: 'Me', email: ME }, folderIds: ['a:sent'], date: new Date(Date.now() - 60_000).toISOString() })
    thread = [thread[0], earlier]
    const mail = useMailStore()
    mail.invalidateThread('m1')
    await mail.loadThread('m1')
    const { compose, id } = await reply()
    await compose.send(id, 0)
    mail.threadsChanged({ accountId: 'a' })
    await mail.loadThread('m1')
    expect(mail.outbox).toHaveLength(1)
  })

  it('doesn’t count twice when its autosaved draft is in the conversation', async () => {
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:r9')
    const mail = useMailStore()
    const compose = useComposeStore()
    const d = (await compose.respond('reply', thread[0]))!
    d.html = '<p>Sounds good</p>'
    d.remoteId = 'draft:a:r9'
    // The autosave synced in: the row counts it.
    thread = [thread[0], msg('d9', { remoteId: 'r9', isDraft: true, folderIds: ['a:drafts'], from: { name: 'Me', email: ME }, date: new Date().toISOString() })]
    mail.invalidateThread('m1')
    await mail.loadThread('m1')
    mail.messages = [row(thread[0], 2)]
    await compose.send(d.id, 5)
    expect(mail.messages[0].threadCount).toBe(2)
  })
})

describe('Outlook keeps a sent draft’s id for the sent message', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    const accounts = useAccountsStore()
    accounts.accounts = [{ id: 'a', email: ME, displayName: 'Me' } as Account]
    accounts.folders = { a: [{ id: 'a:inbox', kind: 'inbox' }, { id: 'a:drafts', kind: 'drafts' }, { id: 'a:sent', kind: 'sent' }] as never }
  })

  it('shows the sent message under the draft’s id', async () => {
    const draft = msg('d1', { remoteId: 'R1', isDraft: true, folderIds: ['a:drafts'], from: { name: 'Me', email: ME }, inReplyTo: '<m1@x>', date: new Date().toISOString() })
    let thread = [msg('m1'), draft]
    vi.spyOn(api, 'getThread').mockImplementation(async () => thread)
    vi.spyOn(api, 'sendMessage').mockResolvedValue(undefined)
    const mail = useMailStore()
    const compose = useComposeStore()
    await mail.loadThread('m1')
    await compose.editDraft(draft)
    const d = compose.drafts[0]
    d.remoteId = 'draft:a:R1'
    await compose.send(d.id, 0)
    expect(mail.goneDrafts.has('d1')).toBe(true)
    // Synced: same local id, now in Sent and no longer a draft.
    thread = [msg('m1'), { ...draft, isDraft: false, folderIds: ['a:sent'] }]
    mail.invalidateThread('m1')
    expect((await mail.loadThread('m1')).map((m) => m.id)).toEqual(['m1', 'd1'])
    expect(mail.outbox).toEqual([])
  })
})

describe('discarding a draft opened from its thread', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('leaves the drafts of the conversation a forward came from alone', async () => {
    const other = msg('d5', { isDraft: true, folderIds: ['a:drafts'], date: new Date(Date.now() + 1000).toISOString() })
    vi.spyOn(api, 'getThread').mockResolvedValue([msg('m1'), other])
    const mail = useMailStore()
    const compose = useComposeStore()
    const [m1] = await mail.loadThread('m1')
    const d = (await compose.respond('forward', m1))!
    d.to = [{ name: '', email: 'x@y.io' }]
    d.remoteId = 'draft:a:f1'
    await compose.discard(d.id)
    expect(mail.goneDrafts.size).toBe(0)
    expect(mail.cachedThread('m1')?.map((m) => m.id)).toEqual(['m1', 'd5'])
  })

  it('shows it again, with an error, when the server delete fails', async () => {
    const draft = msg('d1', { isDraft: true, folderIds: ['a:drafts'] })
    vi.spyOn(api, 'getThread').mockResolvedValue([msg('m1'), draft])
    vi.spyOn(api, 'deleteDraft').mockRejectedValue(new Error('offline'))
    vi.spyOn(api, 'getAttachmentData').mockResolvedValue({ mimeType: 'x', dataBase64: '' })
    const mail = useMailStore()
    const compose = useComposeStore()
    await mail.loadThread('m1')
    await compose.editDraft(draft)
    await compose.discard(compose.drafts[0].id)
    expect(mail.goneDrafts.has('d1')).toBe(false)
  })

  it('takes it out of the thread right away, also its newer autosaves', async () => {
    const draft = msg('d1', { isDraft: true, folderIds: ['a:drafts'], from: { name: 'Me', email: ME }, inReplyTo: '<m1@x>', date: '2026-10-21T10:00:00Z' })
    let thread = [msg('m1'), draft]
    vi.spyOn(api, 'getThread').mockImplementation(async () => thread)
    const save = vi.spyOn(api, 'saveDraft').mockResolvedValue('draft:a:r1')
    const del = vi.spyOn(api, 'deleteDraft').mockResolvedValue(undefined)
    const mail = useMailStore()
    const compose = useComposeStore()
    await mail.loadThread('m1')
    await compose.editDraft(draft)
    const d = compose.drafts[0]
    // Still a reply: it is saved and sent threaded.
    expect(d).toMatchObject({ mode: 'reply', replyToId: 'm1', messageId: 'd1', threadId: 't1' })
    // An autosave went up and synced in under a new id (Gmail).
    d.remoteId = await api.saveDraft({} as never, 'd1')
    thread = [msg('m1'), { ...draft, id: 'd2', date: new Date().toISOString() }]
    mail.invalidateThread('m1')
    await mail.loadThread('m1')
    await compose.discard(d.id)
    expect([...mail.goneDrafts].sort()).toEqual(['d1', 'd2'])
    expect(mail.cachedThread('m1')?.map((m) => m.id)).toEqual(['m1'])
    expect(del).toHaveBeenCalledWith('draft:a:r1')
    // A refetch before the server delete has gone through doesn't bring it back.
    mail.invalidateThread('m1')
    expect((await mail.loadThread('m1')).map((m) => m.id)).toEqual(['m1'])
    expect(save).toHaveBeenCalledTimes(1)
  })
})
