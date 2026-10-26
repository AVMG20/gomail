// Images in the message body ("inline", like Gmail). In a draft they are cid: references to its
// attachments that have a contentId: their bytes live with the other files (IndexedDB, the server
// draft), and the editor shows them from those bytes. They go out as inline parts
// (multipart/related) that the HTML references by Content-ID.
import { uid } from './platform'

export interface InlineFile {
  id: string
  filename: string
  mimeType: string
  size: number
  dataBase64: string
  contentId?: string
}

/** Image types that go in the body; others (SVG, HEIC, TIFF) are attached. */
export const INLINE_IMAGE = /^image\/(png|jpe?g|gif|webp|bmp)$/i

/** Image sources the editor keeps (a file:// picture pasted from Word would be broken). */
export const IMAGE_SRC = /^(cid:|data:image\/|https?:)/i

/** A Content-ID for these bytes: the same image always gets the same one, so it is never added
 *  twice and a draft reopened from the server (whose images come back as data: URLs) keeps them. */
export function contentIdFor(base64: string): string {
  let a = 0x811c9dc5
  let b = 0x01000193
  for (let i = 0; i < base64.length; i++) {
    const c = base64.charCodeAt(i)
    a = Math.imul(a ^ c, 0x01000193)
    b = Math.imul(b ^ c, 0x5bd1e995)
  }
  const hex = (n: number) => (n >>> 0).toString(16).padStart(8, '0')
  return `img-${hex(a)}${hex(b)}-${base64.length.toString(36)}@gomail`
}

export function cidOf(src: string): string | null {
  const m = /^cid:(.+)$/i.exec(src.trim())
  return m ? m[1] : null
}

function byCid(files: InlineFile[], cid: string): InlineFile | undefined {
  const want = cid.toLowerCase()
  return files.find((f) => f.contentId?.toLowerCase() === want)
}

/** What the editor (or the optimistic sent copy) shows for an image: a cid: reference becomes a
 *  data: URL of its bytes ('' while they aren't loaded); other sources are kept. */
export function imageSrc(src: string, files: InlineFile[]): string {
  const cid = cidOf(src)
  if (!cid) return src
  const f = byCid(files, cid)
  return f?.dataBase64 ? `data:${f.mimeType};base64,${f.dataBase64}` : ''
}

const DATA_URL = /^data:(image\/[a-z0-9.+-]+);base64,([a-z0-9+/=\s]+)$/i
const EXT: Record<string, string> = { 'image/jpeg': 'jpg', 'image/jpg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp', 'image/bmp': 'bmp', 'image/svg+xml': 'svg' }

/** The inline file for a data: URL (null when it isn't a base64 image). */
export function fromDataUrl(url: string): InlineFile | null {
  const m = DATA_URL.exec(url.trim())
  if (!m) return null
  const mimeType = m[1].toLowerCase()
  const dataBase64 = m[2].replace(/\s+/g, '')
  return {
    id: uid('att'), filename: `image.${EXT[mimeType] ?? 'png'}`, mimeType, dataBase64,
    size: Math.floor((dataBase64.length * 3) / 4) - (dataBase64.endsWith('==') ? 2 : dataBase64.endsWith('=') ? 1 : 0),
    contentId: contentIdFor(dataBase64),
  }
}

function eachImage(html: string, fn: (img: HTMLImageElement) => void): string {
  if (!/<img\b/i.test(html) || typeof DOMParser === 'undefined') return html
  const doc = new DOMParser().parseFromString(html, 'text/html')
  doc.body.querySelectorAll('img').forEach(fn)
  return doc.body.innerHTML
}

/** data: images become cid: references to new inline files (a draft reopened from the server:
 *  the backend shows inline parts as data: URLs). */
export function extractDataImages(html: string): { html: string; images: InlineFile[] } {
  const images: InlineFile[] = []
  const out = eachImage(html, (img) => {
    const f = fromDataUrl(img.getAttribute('src') ?? '')
    if (!f) return
    if (!byCid(images, f.contentId!)) images.push(f)
    img.setAttribute('src', `cid:${f.contentId}`)
  })
  return { html: images.length ? out : html, images }
}

/** What goes to the backend: the body with its images as cid: references, and the inline parts
 *  they need. data: images (pasted HTML, a signature) become parts too; images whose bytes are
 *  missing are left out, and inline files no longer in the body (deleted) aren't sent. */
export function inlineParts(html: string, files: InlineFile[]): { html: string; parts: InlineFile[] } {
  const parts: InlineFile[] = []
  const out = eachImage(html, (img) => {
    const src = img.getAttribute('src') ?? ''
    const cid = cidOf(src)
    const f = cid ? byCid(files, cid) : fromDataUrl(src)
    if (!f?.dataBase64) {
      if (cid || /^data:/i.test(src)) img.remove()
      return
    }
    if (!byCid(parts, f.contentId!)) parts.push(f)
    img.setAttribute('src', `cid:${f.contentId}`)
  })
  return { html: out, parts }
}

/** The body with cid: images as data: URLs (the copy of a sent reply shown before it syncs). */
export function withDataImages(html: string, files: InlineFile[]): string {
  return eachImage(html, (img) => {
    const src = img.getAttribute('src') ?? ''
    if (cidOf(src)) img.setAttribute('src', imageSrc(src, files))
  })
}
