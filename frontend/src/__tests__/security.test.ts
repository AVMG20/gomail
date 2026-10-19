import { describe, expect, it } from 'vitest'
import { safeColor } from '@/lib/color'
import { isSafeExternalUrl, linkify } from '@/lib/format'
import { mergeLabelImport } from '@/lib/labelsIO'
import type { Label } from '@/lib/types'

describe('external links', () => {
  it('only lets http(s) out', () => {
    expect(isSafeExternalUrl('https://example.com/a?b=1')).toBe(true)
    expect(isSafeExternalUrl('http://example.com')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', ' javascript:alert(1)', 'file:///etc/passwd', 'data:text/html,<script>', 'vbscript:x', 'mailto:a@b.c', 'gomail://x', '//evil.com', 'https://', 'not a url']) {
      expect(isSafeExternalUrl(bad), bad).toBe(false)
    }
  })

  it('linkify escapes text and only emits http/mailto anchors', () => {
    const html = linkify('<img src=x onerror=alert(1)> see https://ex.com/?a=<b>&c="d" or mail bob@ex.com javascript:alert(1)')
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;img')
    const doc = new DOMParser().parseFromString(html, 'text/html')
    const hrefs = [...doc.querySelectorAll('a')].map((a) => a.getAttribute('href'))
    expect(hrefs.every((h) => /^(https?:|mailto:)/.test(h ?? ''))).toBe(true)
    expect(doc.querySelectorAll('*[onerror]').length).toBe(0)
  })
})

describe('colors in inline styles', () => {
  it('accepts hex only', () => {
    expect(safeColor('#abc', '#000')).toBe('#abc')
    expect(safeColor(' #A1B2C3 ', '#000')).toBe('#A1B2C3')
    expect(safeColor('#a1b2c3d4', '#000')).toBe('#a1b2c3d4')
    for (const bad of ['red', '#abcd5', 'url(https://x)', '#fff;background:url(x)', 'var(--x)', '', null, 42, {}]) expect(safeColor(bad, '#000')).toBe('#000')
  })
})

describe('label import', () => {
  const current: Label[] = [{ id: 'l1', name: 'Work', description: 'old', color: '#3b82f6', source: 'user' }]
  const id = (i: number) => `new${i}`

  it('rejects non-JSON, missing labels and huge files', () => {
    expect(mergeLabelImport('nope', current, id)).toMatchObject({ ok: false })
    expect(mergeLabelImport('{"foo":1}', current, id)).toMatchObject({ ok: false, error: 'No labels found in that file' })
    expect(mergeLabelImport('null', current, id)).toMatchObject({ ok: false })
    expect(mergeLabelImport(' '.repeat(1_000_001), current, id)).toMatchObject({ ok: false })
  })

  it('merges by name, validates fields and skips junk', () => {
    const r = mergeLabelImport(JSON.stringify({
      labels: [
        { name: ' work ', description: 'new desc', color: 'url(https://evil)' },
        { name: 'Finance', description: { evil: true }, color: '#22c55e' },
        { name: 'finance', description: 'dupe' },
        { name: '' },
        null,
        'string',
        { name: 'x'.repeat(500), description: 'y'.repeat(5000) },
      ],
      classifyInstructions: 42,
      grammarSystemPrompt: 'Be nice',
    }), current, id)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.added).toBe(2)
    expect(r.updated).toBe(1)
    expect(r.skipped).toBe(4)
    const labels = r.patch.labels!
    expect(labels[0]).toMatchObject({ id: 'l1', name: 'Work', description: 'new desc' })
    expect(labels[0].color).toMatch(/^#[0-9a-f]{6}$/i)
    expect(labels[1]).toMatchObject({ id: 'new0', name: 'Finance', description: '', color: '#22c55e', source: 'user' })
    expect(labels[2].name.length).toBe(64)
    expect(labels[2].description.length).toBe(1000)
    expect(r.patch.classifyInstructions).toBeUndefined()
    expect(r.patch.grammarSystemPrompt).toBe('Be nice')
  })
})
