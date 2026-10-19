<script setup lang="ts">
// Renders an email body safely:
//  * HTML is sanitised with DOMPurify, then shown in a sandboxed iframe
//    (sandbox="allow-same-origin" only: no scripts, forms, popups or top navigation;
//    same-origin lets us measure height and intercept link clicks from here).
//  * A CSP meta tag inside the frame blocks remote loads unless images are allowed.
//  * Remote images are stripped unless allowed; a banner offers to load them.
//  * Links open in the system browser via the backend.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import DOMPurify from 'dompurify'
import { ImageOffIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { shade } from '@/lib/color'
import { isSimpleHtml } from '@/lib/emailTheme'
import { linkify, parseMailto } from '@/lib/format'
import { themeState } from '@/lib/theme'
import type { Message } from '@/lib/types'
import { useComposeStore } from '@/stores/compose'
import { useSettingsStore } from '@/stores/settings'

const props = defineProps<{ message: Message; allowRemote: boolean }>()
const emit = defineEmits<{ loadImages: [] }>()

const compose = useComposeStore()
const settings = useSettingsStore()
/** Dark theme + "Auto" email background: simple mail follows the app theme. */
const darkAuto = computed(() => themeState.value.dark && (settings.settings?.emailBackground ?? 'auto') !== 'light')
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
    if (!allowRemote) {
      for (const attr of ['src', 'srcset', 'background', 'poster']) {
        const v = el.getAttribute(attr)
        if (v && (REMOTE.test(v.trim()) || (attr === 'srcset' && /https?:/i.test(v)))) {
          el.removeAttribute(attr)
          if (el.tagName === 'IMG') {
            const w = el.getAttribute('width')
            const h = el.getAttribute('height')
            // Hide tracking pixels entirely; keep a soft placeholder box for real images.
            if ((w && +w <= 2) || (h && +h <= 2)) el.setAttribute('style', 'display:none')
            else {
              el.setAttribute('data-blocked', '1')
              const hh = h && +h > 0 ? Math.min(+h, 240) : 48
              el.setAttribute('style', `${el.getAttribute('style') ?? ''};height:${hh}px !important`)
              // Transparent pixel avoids the broken-image icon; CSS draws a soft placeholder.
              el.setAttribute('src', 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7')
              el.removeAttribute('alt')
            }
          }
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

const isHtml = computed(() => !!props.message.bodyHtml.trim())

const sanitized = computed(() => (isHtml.value ? sanitize(props.message.bodyHtml, props.allowRemote) : { html: '', blocked: 0 }))
// Judged on the original HTML (DOMParser documents are inert), so loading remote images never flips the theme.
const simple = computed(() => isHtml.value && isSimpleHtml(props.message.bodyHtml))
/** Render this HTML mail with theme colors (no white card). */
const themed = computed(() => darkAuto.value && simple.value)

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
  const img = props.allowRemote ? 'img-src data: cid: https: http:;' : 'img-src data: cid:;'
  return `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; ${img} style-src 'unsafe-inline'${props.allowRemote ? ' https:' : ''}; font-src data:${props.allowRemote ? ' https:' : ''};">
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
  const doc = frame.value?.contentDocument
  if (!doc?.body || !frame.value) return
  // Fit wide newsletters to the pane (like Apple Mail) instead of scrolling sideways.
  const avail = frame.value.clientWidth
  const root = doc.documentElement as HTMLElement & { style: CSSStyleDeclaration & { zoom: string } }
  const natural = Math.ceil(doc.body.scrollWidth * fitScale)
  if (avail > 0 && natural > avail + 2) {
    fitScale = Math.max(0.55, avail / natural)
    root.style.zoom = String(fitScale)
  } else if (fitScale < 1 && natural * (1 / fitScale) <= avail) {
    fitScale = 1
    root.style.zoom = ''
  }
  const h = Math.ceil(Math.max(root.scrollHeight, doc.body.scrollHeight) * (root.style.zoom ? fitScale : 1))
  if (h && Math.abs(h - height.value) > 1) height.value = h
}

function handleLink(href: string) {
  if (href.startsWith('mailto:')) {
    compose.composeWith(parseMailto(href))
    return
  }
  if (/^https?:\/\//i.test(href)) void api.openExternal(href)
}

function onLoad() {
  const doc = frame.value?.contentDocument
  if (!doc) return
  measure()
  ro?.disconnect()
  if (typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => measure())
    ro.observe(doc.body)
  }
  doc.addEventListener('load', measure, true) // images
  doc.addEventListener('click', (e) => {
    const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null
    if (!a) return
    e.preventDefault()
    const href = a.getAttribute('href') ?? ''
    if (href.startsWith('#')) return
    handleLink(href)
  })
  // Let keyboard shortcuts work while the frame has focus.
  doc.addEventListener('keydown', (e) => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: e.key, code: e.code, shiftKey: e.shiftKey, metaKey: e.metaKey, ctrlKey: e.ctrlKey, altKey: e.altKey }))
  })
  setTimeout(measure, 200)
  setTimeout(measure, 1000)
}

function onTextClick(e: MouseEvent) {
  const a = (e.target as Element | null)?.closest('a[href]') as HTMLAnchorElement | null
  if (!a) return
  e.preventDefault()
  handleLink(a.getAttribute('href') ?? '')
}

watch(srcdoc, () => {
  height.value = 80
  fitScale = 1
})
onBeforeUnmount(() => ro?.disconnect())
</script>

<template>
  <div>
    <div
      v-if="isHtml && (blockedCount > 0 || message.remoteContentBlocked || (message.remoteContentBlocked === undefined && message.hasRemoteContent)) && !allowRemote"
      class="bg-muted/60 text-muted-foreground mb-3 flex items-center gap-2 rounded-md px-3 py-1.5 text-xs"
    >
      <ImageOffIcon class="size-3.5 shrink-0" />
      <span class="flex-1">Remote images are hidden to protect your privacy.</span>
      <Button variant="ghost" size="xs" class="text-foreground" @click="emit('loadImages')">Load images</Button>
    </div>
    <div v-if="isHtml" :class="themed ? '' : 'overflow-hidden rounded-lg border border-black/5 bg-white dark:border-white/10'" :data-email-theme="themed ? 'dark' : 'light'">
      <iframe
        ref="frame"
        :srcdoc="srcdoc"
        sandbox="allow-same-origin"
        referrerpolicy="no-referrer"
        title="Message content"
        class="block w-full border-0"
        :style="{ height: `${height}px` }"
        @load="onLoad"
      />
    </div>
    <!-- eslint-disable-next-line vue/no-v-html -- linkify() escapes all text and only emits <a> tags -->
    <div v-else :class="['mail-text selectable', forceLight ? 'gm-light-card rounded-lg border border-white/10 bg-white px-[22px] py-5 text-[#1f2328]' : 'text-foreground/90']" @click="onTextClick" v-html="textHtml" />
  </div>
</template>
