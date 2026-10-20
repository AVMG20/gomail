import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'

/** "Clean up by sender" (components/mail/SenderCleanup.vue): which mailbox it shows, and the rows
 *  the user chose to keep this session. */
export const useCleanupStore = defineStore('cleanup', () => {
  const open = ref(false)
  /** "" = every mailbox. */
  const accountId = ref('')
  /** Row keys (senderRowKey) dismissed with "Keep"; they stay hidden until the app restarts. */
  const kept = shallowRef<Set<string>>(new Set())

  function show(account: string) {
    accountId.value = account
    open.value = true
  }
  function keep(key: string) {
    kept.value = new Set(kept.value).add(key)
  }
  function unkeep(key: string) {
    const next = new Set(kept.value)
    next.delete(key)
    kept.value = next
  }

  return { open, accountId, kept, show, keep, unkeep }
})
