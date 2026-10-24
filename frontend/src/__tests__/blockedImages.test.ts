// Images the backend blocked (no source left) show a placeholder box, not a broken image + alt text.
import { describe, expect, it } from 'vitest'
import { BLANK_GIF, backendBlocked } from '@/components/mail/blockedImages'

const img = (attrs: string) => {
  const d = document.createElement('div')
  d.innerHTML = `<img ${attrs}>`
  return d.firstElementChild!
}

describe('images blocked by the backend', () => {
  it('get a placeholder at their height, without the alt text', () => {
    const header = img('data-gomail-blocked="" width="600" alt="Header image" style="display:block;width:100%"')
    expect(backendBlocked(header)).toBe(true)
    expect(header.getAttribute('src')).toBe(BLANK_GIF)
    expect(header.getAttribute('data-blocked')).toBe('1')
    expect(header.hasAttribute('alt')).toBe(false)
    expect(header.getAttribute('style')).toBe('display:block;width:100%;height:48px !important')
    const tall = img('data-gomail-blocked="" width="300" height="400" alt=""')
    backendBlocked(tall)
    expect(tall.getAttribute('style')).toContain('height:240px') // capped
  })

  it('hides tracking pixels and leaves other images alone', () => {
    const pixel = img('data-gomail-blocked="" width="1" height="1" alt=""')
    expect(backendBlocked(pixel)).toBe(true)
    expect(pixel.getAttribute('style')).toBe('display:none')
    expect(pixel.hasAttribute('src')).toBe(false)
    const inline = img('src="data:image/png;base64,AAAA" alt="Logo"')
    expect(backendBlocked(inline)).toBe(false)
    expect(inline.getAttribute('alt')).toBe('Logo')
    const cell = document.createElement('td')
    cell.setAttribute('data-gomail-blocked', '')
    expect(backendBlocked(cell)).toBe(false)
  })
})
