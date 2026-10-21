import { describe, expect, it } from 'vitest'
import { pastedSignature } from '@/components/compose/signature'

describe('pastedSignature', () => {
  it('keeps a copied signature with tables and images, minus the clipboard wrapper', () => {
    const clip = `<meta charset="utf-8"><html><body><!--StartFragment--><table style="border-collapse:collapse"><tbody><tr>` +
      `<td><img src="https://example.com/logo.png" height="85" style="display:block"></td><td><p style="margin:1px">Arno</p></td>` +
      `</tr></tbody></table><!--EndFragment--></body></html>`
    const html = pastedSignature(clip)
    expect(html.startsWith('<table style="border-collapse:collapse">')).toBe(true)
    expect(html).toContain('src="https://example.com/logo.png"')
    expect(html).not.toMatch(/meta|Fragment/)
  })

  it('leaves plain or lightly styled text to the rich editor', () => {
    expect(pastedSignature('<p><b>Sam Example</b><br>Example Co</p>')).toBe('')
    expect(pastedSignature('<span style="font-family:Arial">Sam Example</span>')).toBe('')
    expect(pastedSignature('')).toBe('')
  })
})
