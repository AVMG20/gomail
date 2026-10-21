// Links clicked in an email: mailto: opens the composer, http(s) opens the
// system browser after a small in-app confirmation (unless the domain is
// trusted), everything else is refused.
import { shallowRef } from 'vue'
import { toast } from 'vue-sonner'
import { api, errorMessage } from '@/lib/api'
import { parseMailto } from '@/lib/format'
import { decideLink, normalizeDomains } from '@/lib/trust'
import { useComposeStore } from '@/stores/compose'
import { useSettingsStore } from '@/stores/settings'

export interface LinkPrompt {
  url: string
  host: string
  textHost: string
  mismatch: boolean
  lookalike: boolean
  /** The link is in mail that looks like phishing: always ask, never offer "Always allow". */
  phishing?: boolean
}

/** The link waiting for "Open link?" (one at a time; a new click replaces it). */
export const linkPrompt = shallowRef<LinkPrompt | null>(null)

async function openInBrowser(url: string) {
  try {
    await api.openExternal(url)
  } catch (e) {
    toast.error('Couldn’t open the link', { description: errorMessage(e) })
  }
}

/** Handle a click on a link inside a message body. text: the link's visible text.
 *  phishing: the mail looks like phishing (even trusted domains ask first). */
export function followMailLink(href: string, text = '', phishing = false) {
  const settings = useSettingsStore()
  const d = decideLink(href, text, settings.settings?.trustedLinkDomains)
  switch (d.kind) {
    case 'ignore':
      return
    case 'mailto':
      void useComposeStore().composeWith(parseMailto(d.href))
      return
    case 'blocked':
      toast.error('This link can’t be opened', { description: 'Only web (http and https) and email links open from messages.' })
      return
    case 'open':
      if (phishing) {
        linkPrompt.value = { url: d.url, host: d.host, textHost: '', mismatch: false, lookalike: false, phishing }
        return
      }
      void openInBrowser(d.url)
      return
    case 'confirm':
      linkPrompt.value = { url: d.url, host: d.host, textHost: d.textHost, mismatch: d.mismatch, lookalike: d.lookalike, phishing }
  }
}

/** Dialog choices. */
export function confirmLink(always: boolean) {
  const p = linkPrompt.value
  linkPrompt.value = null
  if (!p) return
  if (always && !p.phishing) {
    const settings = useSettingsStore()
    const list = settings.settings?.trustedLinkDomains ?? []
    void settings.update({ trustedLinkDomains: normalizeDomains([...list, p.host]) }, 0)
  }
  void openInBrowser(p.url)
}

export function cancelLink() {
  linkPrompt.value = null
}
