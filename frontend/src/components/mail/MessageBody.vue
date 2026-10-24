<script setup lang="ts">
// Renders an email body safely:
//  * HTML is sanitised with DOMPurify, then shown in a sandboxed iframe
//    (sandbox="allow-same-origin allow-scripts": no forms, popups or top navigation;
//    same-origin lets us measure height and intercept link clicks from here).
//    allow-scripts is needed because WebKit (macOS, Linux) refuses to run even the
//    PARENT's event listeners in a frame without it: link clicks then weren't
//    intercepted and the email frame navigated to the website inside the app.
//    The frame's own scripts still can't run: DOMPurify strips them and the CSP
//    below says script-src 'none' (inline scripts, handlers and javascript: URLs).
//  * A CSP meta tag inside the frame blocks remote loads unless images are allowed.
//  * Remote images are stripped unless allowed; a banner offers to load them.
//  * Links go through followMailLink(): confirm, then the system browser.
//  * Junk that looks like phishing gets a warning first; images stay off until it is dismissed.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import DOMPurify from 'dompurify'
import { ImageOffIcon, LoaderIcon, ShieldAlertIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { shade } from '@/lib/color'
import { darkenDocument } from '@/lib/darkMail'
import { isSimpleHtml } from '@/lib/emailTheme'
import { linkify } from '@/lib/format'
import { hasMod } from '@/lib/platform'
import { checkPhishing } from '@/lib/phishing'
import { followMailLink } from '@/composables/useLinkPrompt'
import { themeState } from '@/lib/theme'
import type { Message } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useMailStore } from '@/stores/mail'
import { useSettingsStore } from '@/stores/settings'
import { backendBlocked, placeholder } from './blockedImages'

const props = defineProps<{ message: Message; allowRemote: boolean }>()
const emit = defineEmits<{ loadImages: []; trustSender: [] }>()

const settings = useSettingsStore()
const accounts = useAccountsStore()
const mail = useMailStore()

const phishing = computed(() => checkPhishing(
  props.message,
  accounts.accountMap.get(props.message.accountId)?.email ?? '',
  accounts.accounts.map((a) => a.email),
))
/** The warning is up: it replaces the images banner, and images stay off until it is dismissed. */
const warned = computed(() => phishing.value.suspicious && !mail.phishingDismissed.has(props.message.id))
const allowRemote = computed(() => props.allowRemote && !warned.value)
function dismissWarning() {
  mail.phishingDismissed = new Set([...mail.phishingDismissed, props.message.id])
}
/** Email background setting: "light" (always light), "auto" (plain mail dark) or "dark" (all mail dark). */
const emailBackground = computed(() => settings.settings?.emailBackground ?? 'auto')
/** Dark theme and not "Always light": simple mail follows the app theme. */
const darkAuto = computed(() => themeState.value.dark && emailBackground.value !== 'light')
const forceLight = computed(() => themeState.value.dark && !darkAuto.value)
const frame = ref<HTMLIFrameElement | null>(null)
const height = ref(80)
const blockedCount = ref(0)

const REMOTE = /^(https?:)?\/\//i

function sanitize(html: string, allowRemote: boolean): { html: string; blocked: number } {
  let blocked = 0
  const purify = DOMPurify()
  purify.addHook('uponSanitizeElement', (node, data) => {
    if (data.tagName === 'link' || data.tagName === 'meta' || data.tagName === 'base') (node as Element).remove?.()
  })
  purify.addHook('afterSanitizeAttributes', (node) => {
    const el = node as Element
    if (el.tagName === 'IMG' && /^cid:/i.test(el.getAttribute('src') ?? '')) {
      // Inline part the backend could not embed: hide instead of showing a broken image.
      el.setAttribute('style', 'display:none')
    }
    if (el.tagName === 'A') {
      el.setAttribute('rel', 'noopener noreferrer')
      el.removeAttribute('target')
    }
    // Blocked by the backend already (no source left): the same placeholder, not a broken image.
    backendBlocked(el)
    if (!allowRemote) {
      for (const attr of ['src', 'srcset', 'background', 'poster']) {
        const v = el.getAttribute(attr)
        if (v && (REMOTE.test(v.trim()) || (attr === 'srcset' && /https?:/i.test(v)))) {
          el.removeAttribute(attr)
          if (el.tagName === 'IMG') placeholder(el)
          blocked++
        }
      }
      const style = el.getAttribute('style')
      if (style && /url\(\s*['"]?\s*(https?:)?\/\//i.test(style)) {
        el.setAttribute('style', style.replace(/url\(\s*['"]?\s*(https?:)?\/\/[^)]*\)/gi, 'none'))
        blocked++
      }
    }
  })
  const clean = purify.sanitize(html, {
    WHOLE_DOCUMENT: false,
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'input', 'button', 'textarea', 'select', 'frame', 'frameset'],
    FORBID_ATTR: ['srcdoc', 'formaction'],
    ALLOW_UNKNOWN_PROTOCOLS: false,
    ADD_ATTR: ['target'],
  }) as unknown as string
  let out = collapseQuotes(clean)
  if (!allowRemote) {
    // <style> blocks may also reference remote resources.
    out = out.replace(/url\(\s*['"]?\s*(https?:)?\/\/[^)]*\)/gi, () => {
      blocked++
      return 'none'
    })
  }
  return { html: out, blocked }
}

const QUOTE_SELECTORS = ['.gmail_quote', 'blockquote[type="cite"]', '.yahoo_quoted', '#divRplyFwdMsg', '#appendonsend', '.moz-cite-prefix']

/** Wrap trailing quoted history in a native <details> (works without scripts in the sandbox). */
function collapseQuotes(html: string): string {
  if (!/gmail_quote|type="cite"|yahoo_quoted|divRplyFwdMsg|appendonsend|moz-cite-prefix/i.test(html)) return html
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
  const first = doc.body.querySelector(QUOTE_SELECTORS.join(','))
  if (!first) return html
  // The first marker and everything after it is history (Gmail, Apple Mail, Outlook, Thunderbird).
  const start = first
  const before = doc.createRange()
  before.setStartBefore(doc.body.firstChild ?? doc.body)
  before.setEndBefore(start)
  if (!before.toString().trim()) return html // the quote is the whole message
  const range = doc.createRange()
  range.setStartBefore(start)
  range.setEndAfter(doc.body.lastChild ?? start)
  const details = doc.createElement('details')
  details.className = 'gm-quote'
  const summary = doc.createElement('summary')
  summary.setAttribute('title', 'Show trimmed content')
  summary.textContent = '•••'
  details.appendChild(summary)
  details.appendChild(range.extractContents())
  doc.body.appendChild(details)
  return doc.body.innerHTML
}

const senderEmail = computed(() => props.message.from?.email?.trim() ?? '')
const isHtml = computed(() => !!props.message.bodyHtml.trim())

/** Remote content allowed and present: keep the stripped render until the full body arrives (one reload, not two). */
const remoteOn = computed(() => allowRemote.value && !props.message.remoteContentBlocked)
const sanitized = computed(() => (isHtml.value ? sanitize(props.message.bodyHtml, remoteOn.value) : { html: '', blocked: 0 }))
// Judged on the original HTML (DOMParser documents are inert), so loading remote images never flips the theme.
const simple = computed(() => isHtml.value && isSimpleHtml(props.message.bodyHtml))
/** Render this HTML mail with theme colors (no white card). */
const themed = computed(() => darkAuto.value && simple.value)
/** Experimental "All mail dark": designed mail recolored dark (unless "show original colors" for this conversation). */
const darkened = computed(() => darkAuto.value && emailBackground.value === 'dark' && isHtml.value && !simple.value && !mail.originalColors.has(props.message.threadId))
/** Darkened mail is hidden until the frame has been recolored (no white flash). */
const recolored = ref(false)

function themedCss(): string {
  const cs = getComputedStyle(document.documentElement)
  const fg = cs.getPropertyValue('--foreground').trim() || '#f5f5f5'
  const muted = cs.getPropertyValue('--muted-foreground').trim() || '#a3a3a3'
  const border = cs.getPropertyValue('--border').trim() || 'rgba(255,255,255,.1)'
  const muteBg = cs.getPropertyValue('--muted').trim() || '#262626'
  const link = shade(themeState.value.primary, 0.35)
  return `html,body{background:transparent;color:${fg}}
  body{padding:2px 0}
  body *{color:inherit !important;background-color:transparent !important;border-color:${border} !important}
  a,a *{color:${link} !important}
  blockquote{border-left-color:${border} !important;color:${muted} !important}
  img[data-blocked]{background:${muteBg} !important}
  details.gm-quote>summary{background:${muteBg} !important;color:${muted} !important}`
}

const srcdoc = computed(() => {
  if (!isHtml.value) return ''
  const { html, blocked } = sanitized.value
  blockedCount.value = blocked
  const dark = themed.value
  // Track theme/accent so the frame re-renders when they change.
  void themeState.value
  const img = remoteOn.value ? 'img-src data: cid: https: http:;' : 'img-src data: cid:;'
  // A different document when darkened, so "show original colors" gets a fresh frame.
  return `<!doctype html><html${darkened.value ? ' class="gm-dark"' : ''}><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; ${img} style-src 'unsafe-inline'${remoteOn.value ? ' https:' : ''}; font-src data:${remoteOn.value ? ' https:' : ''};">
<meta name="color-scheme" content="${dark ? 'dark' : 'light'}">
<style>
  html,body{margin:0;padding:0;background:#fff;color:#1f2328}
  body{padding:20px 22px;font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,Ubuntu,Roboto,"Helvetica Neue",Arial,sans-serif;word-wrap:break-word;overflow-wrap:anywhere;overflow:hidden}
  img{max-width:100%;height:auto}
  img[data-blocked]{background:#f1f3f5;min-height:48px;min-width:24px;border-radius:4px}
  table{max-width:100%}
  pre{white-space:pre-wrap}
  blockquote{margin:0 0 0 .8ex;border-left:2px solid #d0d7de;padding-left:1ex;color:#57606a}
  a{color:#0b57d0}
  details.gm-quote{margin-top:12px}
  details.gm-quote>summary{list-style:none;display:inline-block;cursor:pointer;padding:0 8px;border-radius:6px;background:#eef0f3;color:#5f6368;font-size:12px;line-height:18px;letter-spacing:1px;user-select:none}
  details.gm-quote>summary::-webkit-details-marker{display:none}
  details.gm-quote[open]>summary{margin-bottom:8px}
  ${dark ? themedCss() : ''}
</style></head><body>${html}</body></html>`
})

/** Plain text: linkify, and fold a trailing "> quoted" block behind a ••• toggle. */
const textHtml = computed(() => {
  if (isHtml.value) return ''
  const text = props.message.bodyText || props.message.snippet || ''
  const lines = text.split('\n')
  let start = -1
  for (let i = lines.length - 1; i >= 0; i--) {
    const l = lines[i].trim()
    if (l === '' || l.startsWith('>')) {
      if (l.startsWith('>')) start = i
    } else break
  }
  if (start > 0 && /wrote:\s*$|^-{2,}\s*Original Message/i.test(lines[start - 1] ?? '')) start--
  if (start <= 0 || !lines.slice(0, start).join('').trim()) return linkify(text)
  return `${linkify(lines.slice(0, start).join('\n').replace(/\s+$/, ''))}<details class="gm-quote mt-2"><summary title="Show trimmed content">•••</summary>${linkify(lines.slice(start).join('\n'))}</details>`
})

let ro: ResizeObserver | null = null

let fitScale = 1
function measure() {
  const f = frame.value
  const doc = f?.contentDocument
  if (!doc?.body || !f) return
  // Fit wide newsletters to the pane (like Apple Mail) instead of scrolling sideways.
  // Always measure the unscaled layout so the result never depends on the previous scale:
  // feeding a zoomed measurement back in made narrow panes flip between two scales forever.
  const root = doc.documentElement
  root.style.transform = ''
  root.style.width = ''
  const avail = f.clientWidth
  const natural = doc.body.scrollWidth
  fitScale = avail > 0 && natural > avail + 2 ? Math.max(0.55, avail / natural) : 1
  // Lay out at the content's natural width, then shrink it visually to the pane.
  if (fitScale < 1) root.style.width = `${natural}px`
  // offsetHeight (not scrollHeight) so the frame's current height is no floor and it can shrink again.
  const h = Math.ceil(Math.max(root.offsetHeight, doc.body.scrollHeight) * fitScale)
  if (fitScale < 1) {
    root.style.transformOrigin = '0 0'
    root.style.transform = `scale(${fitScale})`
  }
  if (h && Math.abs(h - height.value) > 1) height.value = h
}

type LinkEl = HTMLAnchorElement | HTMLAreaElement

function linkFrom(e: Event): LinkEl | null {
  // <area> (image maps) navigates like a link: never inside the frame itself.
  return ((e.target as Element | null)?.closest?.('a[href], area[href]') as LinkEl | null) ?? null
}

function linkText(a: LinkEl): string {
  return a.tagName === 'AREA' ? (a as HTMLAreaElement).alt : a.textContent ?? ''
}

/**
 * Whether a key pressed in the frame goes on to the app's shortcuts. Combos with ⌘/Ctrl or
 * Alt belong to the frame (select all, copy, find), except the app-wide ones: the palette
 * (mod+K), settings (mod+,), switching mailbox (mod+1…9, mod+shift+0) and text size (mod+= - 0).
 */
function forwardFromFrame(e: KeyboardEvent): boolean {
  if (!e.metaKey && !e.ctrlKey && !e.altKey) return true
  if (!hasMod(e) || e.altKey) return false
  return ['k', 'K', ',', '=', '+', '-'].includes(e.key) || /^[0-9]$/.test(e.key) || e.code === 'Digit0'
}

/** Every activation (click, middle click, ⌘/Ctrl-click) is handled here; the frame never navigates. */
function onFrameClick(e: MouseEvent) {
  const a = linkFrom(e)
  if (!a) return
  const href = a.getAttribute('href') ?? ''
  if (href.startsWith('#')) return // in-page anchor: let the frame scroll
  e.preventDefault()
  e.stopPropagation()
  if (e.type === 'auxclick' && e.button !== 1) return
  followMailLink(href, linkText(a), warned.value)
}

function onLoad() {
  const doc = frame.value?.contentDocument
  if (!doc) return
  if (darkened.value) {
    darkenDocument(doc)
    recolored.value = true
  }
  measure()
  ro?.disconnect()
  if (typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => measure())
    ro.observe(doc.body)
    // A scaled body keeps its natural width, so watch the frame itself for pane resizes.
    if (frame.value) ro.observe(frame.value)
  }
  doc.addEventListener('load', measure, true) // images
  doc.addEventListener('click', onFrameClick, true)
  doc.addEventListener('auxclick', onFrameClick, true)
  // Dragging a link out would hand the URL to the webview (it may navigate the app).
  doc.addEventListener('dragstart', (e) => linkFrom(e) && e.preventDefault(), true)
  // Let keyboard shortcuts work while the frame has focus.
  doc.addEventListener('keydown', (e) => {
    if (!forwardFromFrame(e)) return
    window.dispatchEvent(new KeyboardEvent('keydown', { key: e.key, code: e.code, shiftKey: e.shiftKey, metaKey: e.metaKey, ctrlKey: e.ctrlKey, altKey: e.altKey }))
  })
  setTimeout(measure, 200)
  setTimeout(measure, 1000)
}

function onTextClick(e: MouseEvent) {
  const a = (e.target as Element | null)?.closest('a[href]') as HTMLAnchorElement | null
  if (!a) return
  e.preventDefault()
  if (e.type === 'auxclick' && e.button !== 1) return
  followMailLink(a.getAttribute('href') ?? '', a.textContent ?? '', warned.value)
}

// A new document gets a new <iframe>: WebKit doesn't reliably reload a frame (or fire
// "load") when only its srcdoc changes, so after "Load images" the frame kept the stripped
// body, or stayed at the reset height, until the mail was reopened.
const frameKey = ref(0)
watch(srcdoc, () => {
  recolored.value = false
  height.value = 80
  fitScale = 1
  ro?.disconnect()
  frameKey.value++
})
onBeforeUnmount(() => ro?.disconnect())
</script>

<template>
  <div>
    <div v-if="warned" class="bg-destructive/10 text-destructive border-destructive/25 mb-3 flex gap-2.5 rounded-md border px-3 py-2.5 text-xs" role="alert" data-testid="phishing-warning">
      <ShieldAlertIcon class="mt-px size-4 shrink-0" />
      <div class="min-w-0 flex-1 space-y-1.5">
        <p class="text-[13px] font-semibold">This looks like phishing</p>
        <ul class="text-foreground/80 list-disc space-y-0.5 pl-4 leading-relaxed">
          <li v-for="r in phishing.reasons" :key="r" class="break-words">{{ r }}</li>
        </ul>
        <p class="text-foreground/80 leading-relaxed">Don’t reply, and don’t enter passwords or personal details through its links. Images stay hidden.</p>
      </div>
      <Button variant="ghost" size="xs" class="text-foreground shrink-0 self-start" @click="dismissWarning">I trust this message</Button>
    </div>
    <div
      v-else-if="isHtml && (blockedCount > 0 || message.remoteContentBlocked || (message.remoteContentBlocked === undefined && message.hasRemoteContent)) && !allowRemote"
      class="bg-muted/60 text-muted-foreground mb-3 flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-3 py-1.5 text-xs"
    >
      <ImageOffIcon class="size-3.5 shrink-0" />
      <span class="min-w-40 flex-1">Remote images are hidden to protect your privacy.</span>
      <Button variant="ghost" size="xs" class="text-foreground" @click="emit('loadImages')">Load images</Button>
      <Button v-if="senderEmail" variant="ghost" size="xs" class="text-foreground max-w-full truncate" :title="`Always load images in mail from ${senderEmail}`" @click="emit('trustSender')">Always load images from {{ senderEmail }}</Button>
    </div>
    <div v-else-if="isHtml && allowRemote && message.remoteContentBlocked" class="text-muted-foreground mb-3 flex items-center gap-2 px-3 py-1.5 text-xs" role="status">
      <LoaderIcon class="size-3.5 shrink-0 animate-spin" /> Loading images…
    </div>
    <div v-if="isHtml" :class="themed ? '' : darkened ? 'overflow-hidden rounded-lg border border-white/10' : 'overflow-hidden rounded-lg border border-black/5 bg-white dark:border-white/10'" :data-email-theme="themed || darkened ? 'dark' : 'light'">
      <iframe
        :key="frameKey"
        ref="frame"
        :srcdoc="srcdoc"
        sandbox="allow-same-origin allow-scripts"
        referrerpolicy="no-referrer"
        title="Message content"
        class="block w-full border-0"
        :style="{ height: `${height}px`, visibility: darkened && !recolored ? 'hidden' : undefined }"
        @load="onLoad"
      />
    </div>
    <!-- eslint-disable-next-line vue/no-v-html -- linkify() escapes all text and only emits <a> tags -->
    <div v-else :class="['mail-text selectable', forceLight ? 'gm-light-card rounded-lg border border-white/10 bg-white px-[22px] py-5 text-[#1f2328]' : 'text-foreground/90']" @click="onTextClick" @auxclick="onTextClick" v-html="textHtml" />
  </div>
</template>
