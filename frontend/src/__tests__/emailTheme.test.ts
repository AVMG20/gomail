import { describe, expect, it } from 'vitest'
import { isNeutralBackground, isNeutralTextColor, isSimpleHtml, parseCssColor } from '@/lib/emailTheme'

describe('color helpers', () => {
  it('parses hex, rgb(a) and names', () => {
    expect(parseCssColor('#fff')).toEqual({ r: 255, g: 255, b: 255, a: 1 })
    expect(parseCssColor('#11223380')).toMatchObject({ r: 17, g: 34, b: 51 })
    expect(parseCssColor('rgb(10, 20, 30)')).toEqual({ r: 10, g: 20, b: 30, a: 1 })
    expect(parseCssColor('rgba(0 0 0 / 50%)')).toEqual({ r: 0, g: 0, b: 0, a: 0.5 })
    expect(parseCssColor('black')).toEqual({ r: 0, g: 0, b: 0, a: 1 })
    expect(parseCssColor('papayawhip')).toBeNull()
  })
  it('treats dark greys as neutral text, colors not', () => {
    expect(isNeutralTextColor('#000')).toBe(true)
    expect(isNeutralTextColor('#1f1f1f !important')).toBe(true)
    expect(isNeutralTextColor('windowtext')).toBe(true)
    expect(isNeutralTextColor('#d93025')).toBe(false)
    expect(isNeutralTextColor('#ffffff')).toBe(false)
  })
  it('treats white/transparent backgrounds as neutral', () => {
    expect(isNeutralBackground('#ffffff')).toBe(true)
    expect(isNeutralBackground('white')).toBe(true)
    expect(isNeutralBackground('transparent')).toBe(true)
    expect(isNeutralBackground('#fafafa none repeat')).toBe(true)
    expect(isNeutralBackground('#f4f4f4')).toBe(true)
    expect(isNeutralBackground('#1a73e8')).toBe(false)
    expect(isNeutralBackground('url(x.png)')).toBe(false)
  })
})

describe('isSimpleHtml', () => {
  it('accepts plain Gmail/Apple-style mail', () => {
    expect(isSimpleHtml('<div dir="ltr">Hi Alex,<br><br>See you at 6!<div><br></div></div>')).toBe(true)
    expect(isSimpleHtml('<p>Hello <a href="https://x.io" style="color:#1155cc">link</a></p>')).toBe(true)
    expect(isSimpleHtml('<p style="color:#222;font-family:Arial">Thanks</p><img src="data:image/png;base64,AA" width="200">')).toBe(true)
  })
  it('accepts Outlook mail with neutral styles and link colors', () => {
    const outlook = `<style>p.MsoNormal{margin:0;font-size:11pt;color:windowtext}
      a:link, span.MsoHyperlink{color:#0563C1;text-decoration:underline}</style>
      <div class="WordSection1"><p class="MsoNormal">Hi team</p></div>`
    expect(isSimpleHtml(outlook)).toBe(true)
  })
  it('rejects branded/complex mail', () => {
    expect(isSimpleHtml('<table bgcolor="#1a73e8"><tr><td>Sale</td></tr></table>')).toBe(false)
    expect(isSimpleHtml('<div style="background:#000;color:#fff">Dark promo</div>')).toBe(false)
    expect(isSimpleHtml('<p><font color="red">Alert</font></p>')).toBe(false)
    expect(isSimpleHtml('<style>.hero{background-color:#ffe4c4}</style><div class="hero">x</div>')).toBe(false)
    expect(isSimpleHtml('<table><tr><td background="https://x/bg.png">x</td></tr></table>')).toBe(false)
    expect(isSimpleHtml('<span style="color: #34a853">green text</span>')).toBe(false)
    expect(isSimpleHtml('<html><head></head><body bgcolor="#eef2f7"><p>x</p></body></html>')).toBe(false)
  })
  it('treats empty bodies as simple', () => {
    expect(isSimpleHtml('  ')).toBe(true)
  })
})
