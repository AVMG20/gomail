import { shallowRef } from 'vue'
import { hexToRgb, luminance, readableForeground, shade } from './color'
import type { Settings } from './types'

export const THEME_CACHE_KEY = 'gomail.theme.v1'
export const DEFAULT_PRIMARY = '#2563eb'
export const DEFAULT_RADIUS = 0.625

export interface ThemeCache {
  theme: string
  density: string
  vars: Record<string, string>
  darkVars: Record<string, string>
}

function buildVars(primary: string, radius: number, dark: boolean): Record<string, string> {
  // In dark mode lift only very dark accents (navy, near-black) so they stay visible on
  // near-black surfaces; mid-tone accents keep white text at AA contrast.
  const rgb = hexToRgb(primary)
  const base = dark && rgb && luminance(rgb) < 0.06 ? shade(primary, 0.2) : primary
  const fg = readableForeground(base)
  return {
    '--primary': base,
    '--primary-foreground': fg,
    '--ring': base,
    '--sidebar-primary': base,
    '--sidebar-primary-foreground': fg,
    '--sidebar-ring': base,
    '--radius': `${radius}rem`,
  }
}

export function themeFromSettings(s: Pick<Settings, 'theme' | 'density' | 'primaryColor' | 'radius'>): ThemeCache {
  const primary = /^#[0-9a-f]{3,6}$/i.test(s.primaryColor ?? '') ? s.primaryColor : DEFAULT_PRIMARY
  const radius = typeof s.radius === 'number' && s.radius >= 0 && s.radius <= 1.5 ? s.radius : DEFAULT_RADIUS
  return {
    theme: s.theme || 'system',
    density: s.density || 'comfortable',
    vars: buildVars(primary, radius, false),
    darkVars: buildVars(primary, radius, true),
  }
}

const media = typeof window !== 'undefined' ? window.matchMedia('(prefers-color-scheme: dark)') : null

export function isDarkFor(theme: string): boolean {
  return theme === 'dark' || (theme !== 'light' && !!media?.matches)
}

let current: ThemeCache | null = null

/** Reactive: the resolved theme is dark, and the current accent (for code that needs literal colors, e.g. email frames). */
export const themeState = shallowRef({ dark: false, primary: DEFAULT_PRIMARY })

export function applyTheme(t: ThemeCache, persist = true): void {
  current = t
  const root = document.documentElement
  const dark = isDarkFor(t.theme)
  root.classList.toggle('dark', dark)
  root.style.colorScheme = dark ? 'dark' : 'light'
  root.dataset.density = t.density
  const vars = dark ? t.darkVars : t.vars
  for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v)
  themeState.value = { dark, primary: vars['--primary'] ?? DEFAULT_PRIMARY }
  if (persist) {
    try {
      localStorage.setItem(THEME_CACHE_KEY, JSON.stringify(t))
    } catch {
      /* storage unavailable */
    }
  }
}

// Follow OS theme changes when theme === "system". Safari < 14 only has addListener.
if (media) {
  const onChange = () => current && current.theme === 'system' && applyTheme(current, false)
  if (media.addEventListener) media.addEventListener('change', onChange)
  else media.addListener(onChange)
}

export function readCachedTheme(): ThemeCache | null {
  try {
    const raw = localStorage.getItem(THEME_CACHE_KEY)
    return raw ? (JSON.parse(raw) as ThemeCache) : null
  } catch {
    return null
  }
}

export function currentIsDark(): boolean {
  return document.documentElement.classList.contains('dark')
}
