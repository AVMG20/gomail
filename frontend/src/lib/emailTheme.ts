// Decides whether a (sanitised) HTML email is "simple" enough to render with the
// app's theme colors in dark mode. Simple = it sets no meaningful colors of its own:
// only near-black text, white/transparent backgrounds and link colors are allowed.
// Branded/complex mail (custom backgrounds, colored text, bgcolor tables…) keeps the light card.

const NEUTRAL_KEYWORDS = new Set(['inherit', 'initial', 'unset', 'currentcolor', 'windowtext', 'auto', 'none', 'transparent', 'window', 'canvastext', 'canvas', 'revert'])

const NAMED: Record<string, [number, number, number]> = {
  black: [0, 0, 0], white: [255, 255, 255], gray: [128, 128, 128], grey: [128, 128, 128],
  dimgray: [105, 105, 105], dimgrey: [105, 105, 105], darkgray: [169, 169, 169], darkgrey: [169, 169, 169],
  silver: [192, 192, 192], gainsboro: [220, 220, 220], whitesmoke: [245, 245, 245], snow: [255, 250, 250],
  navy: [0, 0, 128], blue: [0, 0, 255], red: [255, 0, 0], green: [0, 128, 0],
}

/** Parse a CSS color into RGB (0–255) with alpha, or null when unknown. */
export function parseCssColor(input: string): { r: number; g: number; b: number; a: number } | null {
  const v = input.trim().toLowerCase().replace(/\s*!important\s*$/, '')
  if (NAMED[v]) {
    const [r, g, b] = NAMED[v]
    return { r, g, b, a: 1 }
  }
  let m = /^#([0-9a-f]{3,8})$/.exec(v)
  if (m) {
    let h = m[1]
    if (h.length === 3 || h.length === 4) h = h.split('').map((c) => c + c).join('')
    if (h.length !== 6 && h.length !== 8) return null
    const n = (i: number) => parseInt(h.slice(i, i + 2), 16)
    return { r: n(0), g: n(2), b: n(4), a: h.length === 8 ? n(6) / 255 : 1 }
  }
  m = /^rgba?\(([^)]+)\)$/.exec(v)
  if (m) {
    const parts = m[1].split(/[\s,/]+/).filter(Boolean)
    if (parts.length < 3) return null
    const ch = (p: string) => (p.endsWith('%') ? (parseFloat(p) * 255) / 100 : parseFloat(p))
    const a = parts[3] === undefined ? 1 : parts[3].endsWith('%') ? parseFloat(parts[3]) / 100 : parseFloat(parts[3])
    const [r, g, b] = parts.slice(0, 3).map(ch)
    if ([r, g, b, a].some((x) => Number.isNaN(x))) return null
    return { r, g, b, a }
  }
  return null
}

function luminance({ r, g, b }: { r: number; g: number; b: number }): number {
  const f = (c: number) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
}

/** Text colors that are effectively "default dark text" (safe to replace with the theme foreground). */
export function isNeutralTextColor(value: string): boolean {
  const v = value.trim().toLowerCase().replace(/\s*!important\s*$/, '')
  if (!v || NEUTRAL_KEYWORDS.has(v)) return true
  const c = parseCssColor(v)
  if (!c) return false
  if (c.a === 0) return true
  const max = Math.max(c.r, c.g, c.b)
  const min = Math.min(c.r, c.g, c.b)
  // Dark and unsaturated: black, #222, #333, Outlook's #1f1f1f, grey signatures…
  return luminance(c) < 0.2 && max - min < 40
}

/** Backgrounds that are effectively "no background" (white, near-white, transparent). */
export function isNeutralBackground(value: string): boolean {
  const v = value.trim().toLowerCase().replace(/\s*!important\s*$/, '')
  if (!v || NEUTRAL_KEYWORDS.has(v)) return true
  // background shorthand: every color token in it must be neutral; any image makes it complex.
  if (/url\(|gradient\(/.test(v)) return false
  const tokens = v.match(/#[0-9a-f]{3,8}\b|rgba?\([^)]*\)|[a-z]+/g) ?? []
  const colors = tokens.map((t) => ({ t, c: parseCssColor(t) })).filter((x) => x.c || NAMED[x.t])
  return colors.every(({ c }) => !!c && (c.a === 0 || luminance(c) > 0.9))
}

function declarations(style: string): Array<[string, string]> {
  return style
    .split(';')
    .map((d) => {
      const i = d.indexOf(':')
      return i < 0 ? null : ([d.slice(0, i).trim().toLowerCase(), d.slice(i + 1).trim()] as [string, string])
    })
    .filter((x): x is [string, string] => !!x && !!x[0])
}

function styleIsNeutral(style: string, isLinkRule = false): boolean {
  for (const [prop, value] of declarations(style)) {
    if (prop === 'color' && !isLinkRule && !isNeutralTextColor(value)) return false
    if ((prop === 'background' || prop === 'background-color') && !isNeutralBackground(value)) return false
    if (prop === 'background-image' && value.trim().toLowerCase() !== 'none') return false
  }
  return true
}

/**
 * True when the HTML body sets no colors of its own (after sanitising), so it can follow the app theme.
 * Links may have any color (they get themed); images are fine (photos read well on dark).
 */
export function isSimpleHtml(html: string): boolean {
  if (!html.trim()) return true
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
  // <style> blocks: check every rule; link rules (a, a:link, MsoHyperlink) may set color.
  for (const s of Array.from(doc.querySelectorAll('style'))) {
    const css = (s.textContent ?? '').replace(/\/\*[\s\S]*?\*\//g, '')
    const rule = /([^{}]+)\{([^{}]*)\}/g
    let m: RegExpExecArray | null
    while ((m = rule.exec(css))) {
      const selector = m[1].trim().replace(/^@media[^{]*/i, '')
      const isLink = /(^|[\s,>+~])a(:[\w-]+)?\s*($|,)|hyperlink/i.test(selector)
      if (!styleIsNeutral(m[2], isLink)) return false
    }
  }
  // Include <body> itself: a full document's <body bgcolor/style> merges into it.
  for (const el of [doc.body, ...Array.from(doc.body.querySelectorAll('*'))]) {
    const bg = el.getAttribute('bgcolor')
    if (bg && !isNeutralBackground(bg)) return false
    if (el.hasAttribute('background')) return false
    const fc = el.tagName === 'FONT' ? el.getAttribute('color') : null
    if (fc && !isNeutralTextColor(fc)) return false
    const style = el.getAttribute('style')
    if (style && !styleIsNeutral(style, el.tagName === 'A')) return false
  }
  return true
}
