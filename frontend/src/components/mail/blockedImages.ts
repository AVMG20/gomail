/** A transparent pixel: an image without a source shows the broken-image icon and its alt text. */
export const BLANK_GIF = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

/** A blocked remote image: tracking pixels go, real images keep a soft placeholder box (the
 *  frame's CSS draws img[data-blocked]) at their height when known. */
export function placeholder(el: Element) {
  const w = el.getAttribute('width')
  const h = el.getAttribute('height')
  if ((w && +w <= 2) || (h && +h <= 2)) return el.setAttribute('style', 'display:none')
  el.setAttribute('data-blocked', '1')
  const hh = h && +h > 0 ? Math.min(+h, 240) : 48
  el.setAttribute('style', `${el.getAttribute('style') ?? ''};height:${hh}px !important`)
  el.setAttribute('src', BLANK_GIF)
  el.removeAttribute('alt')
}

/** The backend already took the image's remote source out (images not allowed for the sender:
 *  mailmime.BlockRemoteContent marks it data-gomail-blocked). Reports whether el was one. */
export function backendBlocked(el: Element): boolean {
  if (el.tagName !== 'IMG' || !el.hasAttribute('data-gomail-blocked') || el.getAttribute('src')) return false
  placeholder(el)
  return true
}
