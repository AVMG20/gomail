import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage, hasMethod } from '@/lib/api'
import { inChunks, useMailStore } from './mail'
import { useAccountsStore } from './accounts'
import { useSettingsStore } from './settings'
import { useUiStore } from './ui'
import { ruleTarget, sendersOf, type JunkSenderResult, type RuleRef, type SenderJunk, type SenderScope } from '@/lib/junk'
import type { LabelRule } from '@/lib/types'
import { announce } from '@/lib/announce'

/** Sender rules made from the mail: "Always send <sender> to Junk", "Always label <sender> as …",
 *  and "Stop junking" after "Not junk". The backend creates and applies the rules; the settings
 *  copy here is refreshed after each change so a later settings save can't drop them. */
export const useSenderRulesStore = defineStore('senderRules', () => {
  const mail = useMailStore()
  const accounts = useAccountsStore()
  const settings = useSettingsStore()
  const ui = useUiStore()

  const available = () => hasMethod('JunkSender') && hasMethod('RemoveRules')
  const ownEmail = (accountId: string) => accounts.accountMap.get(accountId)?.email ?? ''
  const editRules = (accountId: string) => ({ label: 'Edit rules', onClick: () => ui.openSettings(`mailbox:${accountId}`, 'labels') })

  /** Re-read the rules the backend changed (the settings store saves its whole copy). */
  async function refreshRules() {
    try {
      const fresh = await api.getSettings()
      if (settings.settings) settings.settings = { ...settings.settings, labelRules: fresh.labelRules ?? [] }
    } catch {
      /* the next settings load has them */
    }
  }

  /** Pending settings edits land first, so they can't overwrite the new rule afterwards. */
  async function beforeChange() {
    await settings.flush()
  }

  /** "Always send <sender> to Junk" for the picked rows: one rule per sender and mailbox, and the
   *  rows plus the sender's inbox mail to Junk, read. `sender` names it when the row isn't loaded
   *  (the reading pane). Undo removes new rules and puts the mail back. */
  async function junkSender(ids: string[], scope: SenderScope = 'address', sender?: { accountId: string; address: string }) {
    const rows = mail.snapshot(ids)
    const groups = sender ? [{ ...sender, ids }] : sendersOf(rows, ownEmail)
    if (!groups.length) {
      toast('No sender to send to Junk')
      return
    }
    const inJunk = mail.mailbox.kind === 'spam'
    const picked = groups.flatMap((g) => g.ids)
    const backend = groups.map((g) => ({ ...g, ids: mail.actionIds(g.ids) })) // before the rows leave the list
    const undoPicked = inJunk ? null : mail.undoMove(mail.snapshot(picked))
    if (!inJunk) mail.removeLocal(picked)
    await beforeChange()
    const results: JunkSenderResult[] = []
    try {
      for (const g of backend) results.push(await api.junkSender(g.accountId, g.address, scope, g.ids))
    } catch (e) {
      toast.error('Couldn’t send the sender to Junk', { description: errorMessage(e) })
    }
    await refreshRules()
    void mail.softRefresh()
    accounts.refreshCounts()
    if (!results.length) return
    const created = results.filter((r) => r.created).map((r) => r.rule.id)
    const moved = results.reduce((n, r) => n + r.count, 0)
    const title = results.length === 1 ? `${ruleTarget(results[0].rule.contains)} goes to Junk from now on` : `${results.length} senders go to Junk from now on`
    announce(title)
    mail.undoToast(
      title,
      async () => {
        try {
          if (created.length) await api.removeRules(created)
          await undoPicked?.()
          for (const r of results) {
            const inbox = accounts.folderOf(r.rule.accountId, 'inbox')?.id
            if (inbox && r.moved.length) await inChunks(r.moved, (c) => api.moveToFolder(c, inbox))
          }
          const unread = results.flatMap((r) => r.unread)
          if (unread.length) await inChunks(unread, (c) => api.markRead(c, false))
        } catch (e) {
          toast.error('Undo failed', { description: errorMessage(e) })
        }
        await refreshRules()
        void mail.softRefresh()
        accounts.refreshCounts()
      },
      {
        description: moved ? `${moved.toLocaleString()} ${moved === 1 ? 'message' : 'messages'} moved to Junk and marked read.` : undefined,
        cancel: editRules(results[0].rule.accountId),
      },
    )
  }

  /** "Always label <sender> as <label>": a rule for the mailbox, and the label on the sender's mail
   *  now. Undo removes a new rule and the labels it just added. */
  async function labelSender(accountId: string, address: string, labelId: string, scope: SenderScope = 'address') {
    await beforeChange()
    try {
      const r = await api.labelSender(accountId, address, scope, labelId)
      await refreshRules()
      const name = settings.labelMap.get(labelId)?.name ?? 'the label'
      const who = ruleTarget(r.rule.contains)
      const n = r.labeled.length
      mail.undoToast(
        `${who} is labeled “${name}” from now on`,
        async () => {
          try {
            if (r.created) await api.removeRules([r.rule.id])
            if (n) await inChunks(r.labeled, (c) => api.removeLabel(c, labelId))
          } catch (e) {
            toast.error('Undo failed', { description: errorMessage(e) })
          }
          await refreshRules()
          void mail.softRefresh()
        },
        { description: n ? `${n.toLocaleString()} ${n === 1 ? 'message' : 'messages'} labeled.` : undefined, cancel: editRules(accountId) },
      )
    } catch (e) {
      await refreshRules()
      toast.error('Couldn’t add the rule', { description: errorMessage(e) })
    }
    void mail.softRefresh()
  }

  /** Turn sender rules off (a checked "Always …" item clicked again, or "Stop junking"). */
  async function removeRules(rules: (LabelRule | RuleRef)[], what: string) {
    if (!rules.length) return
    await beforeChange()
    try {
      await api.removeRules(rules.map((r) => r.id))
      toast(what, { cancel: editRules(rules[0].accountId || mail.mailbox.accountId) })
    } catch (e) {
      toast.error('Couldn’t remove the rule', { description: errorMessage(e) })
    }
    await refreshRules()
  }

  /** After "Not junk": junk rules would keep sending the sender's new mail to Junk; offer to stop. */
  function offerStopJunking(rules: RuleRef[]) {
    if (!rules.length) return
    const who = rules.length === 1 ? ruleTarget(rules[0].contains) : `${rules.length} senders`
    toast(`A rule still sends ${who} to Junk`, {
      description: 'Only new mail is affected.',
      duration: 10_000,
      action: { label: rules.length === 1 ? `Stop junking ${who}` : 'Stop junking them', onClick: () => void removeRules(rules, `${who} no longer goes to Junk`) },
    })
  }

  /** After "Not junk": the senders' other mail still in Junk, for "Move all to Inbox?". */
  const junkLeft = shallowRef<SenderJunk[]>([])

  /** Re-read the Not junk senders the backend added (the settings store saves its whole copy). */
  async function refreshNotJunkSenders() {
    try {
      const fresh = await api.getSettings()
      if (settings.settings) settings.settings = { ...settings.settings, notJunkSenders: fresh.notJunkSenders ?? {} }
    } catch {
      /* the next settings load has them */
    }
  }

  /** "Not junk" went through: remember the senders here too, and offer to move the rest of their Junk. */
  async function afterNotJunk(more: SenderJunk[]) {
    await refreshNotJunkSenders()
    if (hasMethod('NotJunkSender')) junkLeft.value = more.filter((m) => m.count > 0)
  }

  /** "Move all to Inbox": every listed sender's mail in Junk. */
  async function moveJunkLeft() {
    const list = junkLeft.value
    junkLeft.value = []
    let moved = 0
    try {
      for (const s of list) moved += await api.notJunkSender(s.accountId, s.email)
    } catch (e) {
      toast.error('Couldn’t move the mail', { description: errorMessage(e) })
    }
    if (moved) {
      const text = `${moved} more ${moved === 1 ? 'message' : 'messages'} moved to Inbox`
      announce(text)
      toast(text)
    }
    void mail.softRefresh()
    accounts.refreshCounts()
  }

  return { available, refreshRules, junkSender, labelSender, removeRules, offerStopJunking, beforeChange, junkLeft, afterNotJunk, moveJunkLeft }
})
