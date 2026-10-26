import { afterEach, describe, expect, it } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { contentIdFor, extractDataImages, imageSrc, inlineParts, withDataImages } from '@/lib/inlineImages'
import { InlineImage } from '@/components/compose/inlineImage'
import { splitLines } from '@/components/compose/lines'

const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='
const cid = contentIdFor(PNG)
const file = { id: 'att1', filename: 'dot.png', mimeType: 'image/png', size: 70, dataBase64: PNG, contentId: cid }

describe('inline images', () => {
  it('gives the same bytes the same Content-ID, usable in a header', () => {
    expect(contentIdFor(PNG)).toBe(cid)
    expect(contentIdFor(PNG + 'A')).not.toBe(cid)
    expect(cid).toMatch(/^[A-Za-z0-9._@-]+$/)
  })

  it('shows cid: images from their bytes', () => {
    expect(imageSrc(`cid:${cid}`, [file])).toBe(`data:image/png;base64,${PNG}`)
    expect(imageSrc('cid:unknown@x', [file])).toBe('')
    expect(imageSrc('https://x.io/a.png', [file])).toBe('https://x.io/a.png')
    expect(withDataImages(`<p><img src="cid:${cid}"></p>`, [file])).toContain(`data:image/png;base64,${PNG}`)
  })

  it('turns a reopened draft’s data: images back into inline files with the same id', () => {
    const { html, images } = extractDataImages(`<p>a<img src="data:image/png;base64,${PNG}"><img src="data:image/png;base64,${PNG}"></p>`)
    expect(images).toHaveLength(1)
    expect(images[0]).toMatchObject({ contentId: cid, mimeType: 'image/png', filename: 'image.png', size: 70 })
    expect(html).toBe(`<p>a<img src="cid:${cid}"><img src="cid:${cid}"></p>`)
  })

  it('sends the images in the body as parts, not deleted or missing ones', () => {
    const gone = { ...file, id: 'att2', contentId: 'deleted@gomail' }
    const missing = { ...file, id: 'att3', contentId: 'missing@gomail', dataBase64: '' }
    const { html, parts } = inlineParts(`<p><img src="cid:${cid}"><img src="cid:missing@gomail"><img src="https://x.io/logo.png"></p>`, [file, gone, missing])
    expect(parts.map((p) => p.contentId)).toEqual([cid])
    expect(html).toBe(`<p><img src="cid:${cid}"><img src="https://x.io/logo.png"></p>`)
    // A data: image (pasted HTML) goes as a part too.
    const pasted = inlineParts(`<p><img src="data:image/png;base64,${PNG}"></p>`, [])
    expect(pasted.parts).toHaveLength(1)
    expect(pasted.html).toBe(`<p><img src="cid:${cid}"></p>`)
  })
})

describe('editor', () => {
  let ed: Editor | null = null
  afterEach(() => ed?.destroy())
  const make = (html: string) => (ed = new Editor({ extensions: [StarterKit, InlineImage], content: html }))
  const select = (e: Editor, text: string, collapse = false) => {
    let from = 0
    e.state.doc.descendants((n, pos) => {
      if (n.isText && n.text?.includes(text)) from = pos + n.text.indexOf(text)
    })
    e.commands.setTextSelection(collapse ? from + 1 : { from, to: from + text.length })
  }

  it('quotes only the selected line of a <br> paragraph', () => {
    const e = make('<p>line one<br>ewqeqwee<br>line three</p>')
    select(e, 'ewqeqwee')
    e.chain().command(splitLines).toggleBlockquote().run()
    expect(e.getHTML()).toBe('<p>line one</p><blockquote><p>ewqeqwee</p></blockquote><p>line three</p>')
  })

  it('lists only the caret’s line, and every selected line', () => {
    const e = make('<p>one<br>two<br>three</p>')
    select(e, 'three', true)
    e.chain().command(splitLines).toggleBulletList().run()
    expect(e.getHTML()).toMatch(/^<p>one<br>two<\/p><ul><li><p>three<\/p><\/li><\/ul>/)
    const f = make('<p>one<br>two<br>three<br>four</p>')
    let from = 0
    let to = 0
    f.state.doc.descendants((n, pos) => {
      if (n.text === 'two') from = pos
      if (n.text === 'three') to = pos + 5
    })
    f.commands.setTextSelection({ from, to })
    f.chain().command(splitLines).toggleOrderedList().run()
    expect(f.getHTML()).toMatch(/^<p>one<\/p><ol><li><p>two<\/p><\/li><li><p>three<\/p><\/li><\/ol><p>four<\/p>/)
  })

  it('leaves real paragraphs alone', () => {
    const e = make('<p>one</p><p>two</p><p>three</p>')
    select(e, 'two')
    e.chain().command(splitLines).toggleBlockquote().run()
    expect(e.getHTML()).toBe('<p>one</p><blockquote><p>two</p></blockquote><p>three</p>')
  })

  it('keeps cid: images, drops ones it can’t show', () => {
    const e = make(`<p>a<img src="cid:${cid}"><img src="file:///C:/word.png"></p>`)
    const imgs = new DOMParser().parseFromString(e.getHTML(), 'text/html').querySelectorAll('img')
    expect([...imgs].map((i) => i.getAttribute('src'))).toEqual([`cid:${cid}`])
  })
})
