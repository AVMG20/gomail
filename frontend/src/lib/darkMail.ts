// Experimental "dark designed mail": recolors a rendered (white) email for dark mode,
// the way mail apps do it. Light backgrounds and borders turn dark, dark text turns
// light; dark or saturated colors (a purple header, a yellow button) and all images
// keep theirs. Mail that is already dark needs no change, so it stays as it is.
import { luminance, parseCssColor } from './emailTheme'

type Rgba = { r: number; g: number; b: number; a: number }

function toHsl({ r, g, b }: Rgba): [number, number, number] {
  const [x, y, z] = [r / 255, g / 255, b / 255]
  const max = Math.max(x, y, z)
  const min = Math.min(x, y, z)
  const l = (max + min) / 2
  if (max === min) return [0, 0, l]
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  const h = max === x ? (y - z) / d + (y < z ? 6 : 0) : max === y ? (z - x) / d + 2 : (x - y) / d + 4
  return [h * 60, s, l]
}

const hsla = (h: number, s: number, l: number, a: number) =>
  `hsla(${Math.round(h)}, ${Math.round(s * 100)}%, ${Math.round(l * 100)}%, ${Math.round(a * 1000) / 1000})`

/** A light, unsaturated background (white, light grey, pastel): these turn dark. */
export function isLightBackground(c: Rgba): boolean {
  const [, s, l] = toHsl(c)
  return l > 0.85 || (luminance(c) > 0.4 && s < 0.25)
}

/** White → near-black (7%), light grey a bit lighter; the hue stays, muted. */
export function darkBackground(c: Rgba): string {
  const [h, s, l] = toHsl(c)
  return hsla(h, s * 0.5, 0.07 + (1 - l) * 0.6, c.a)
}

/** Dark text → light text with the same hue (black → 93%, dark purple → light lavender). */
export function lightText(c: Rgba): string {
  const [h, s, l] = toHsl(c)
  return hsla(h, Math.min(s, 0.8), 0.93 - l * 0.5, c.a)
}

const MAX_ELEMENTS = 6000
type Backdrop = 'dark' | 'light' | 'image'

/** Recolors a rendered email in place (inline !important styles). Reads every color
 *  first and writes afterwards, so the changes never feed back into what is read
 *  (and the page is restyled once, not per element). */
export function darkenDocument(doc: Document): void {
  const view = doc.defaultView ?? globalThis.window
  if (!doc.body || !view) return
  const backdrop = new Map<Element, Backdrop>()
  const writes: [HTMLElement, string, string][] = []
  const els = [doc.documentElement, doc.body, ...Array.from(doc.body.querySelectorAll('*')).slice(0, MAX_ELEMENTS)]
  for (const node of els) {
    const el = node as HTMLElement
    if (!el.style || (el.tagName === 'IMG' && !el.hasAttribute('data-blocked'))) continue
    const cs = view.getComputedStyle(el)
    let under: Backdrop = (el.parentElement && backdrop.get(el.parentElement)) || 'light'
    const bg = parseCssColor(cs.backgroundColor || '')
    if (bg && bg.a > 0.05) {
      if (isLightBackground(bg)) {
        writes.push([el, 'background-color', darkBackground(bg)])
        under = 'dark'
      } else under = luminance(bg) < 0.4 ? 'dark' : 'light'
    }
    if (cs.backgroundImage && cs.backgroundImage !== 'none') under = 'image'
    backdrop.set(el, under)
    // Text: only where it now sits on a dark background, and only if it is dark itself.
    const fg = parseCssColor(cs.color || '')
    if (under === 'dark' && fg && toHsl(fg)[2] < 0.5) writes.push([el, 'color', lightText(fg)])
    for (const side of ['top', 'right', 'bottom', 'left']) {
      if (!(parseFloat(cs.getPropertyValue(`border-${side}-width`)) > 0)) continue
      const bc = parseCssColor(cs.getPropertyValue(`border-${side}-color`) || '')
      if (bc && bc.a > 0.05 && isLightBackground(bc)) writes.push([el, `border-${side}-color`, darkBackground(bc)])
    }
  }
  for (const [el, prop, value] of writes) el.style.setProperty(prop, value, 'important')
}
