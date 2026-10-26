// Tiptap node for images in the message body. Its src is what is saved and sent (cid: for images
// the user added, whose bytes are draft attachments); the editor shows `resolve(src)`, a data:
// URL of those bytes, and updates when they arrive (a draft restored after a restart).
import { Node } from '@tiptap/core'
import { watchEffect } from 'vue'
import { IMAGE_SRC } from '@/lib/inlineImages'

export interface InlineImageOptions {
  resolve: (src: string) => string
}

export const InlineImage = Node.create<InlineImageOptions>({
  name: 'image',
  group: 'inline',
  inline: true,
  atom: true,
  draggable: true,
  addOptions: () => ({ resolve: (src: string) => src }),
  addAttributes: () => ({
    src: { default: '' },
    alt: { default: null },
    width: { default: null },
    height: { default: null },
  }),
  parseHTML: () => [{
    tag: 'img[src]',
    getAttrs: (el) => {
      const src = (el as HTMLElement).getAttribute('src') ?? ''
      return IMAGE_SRC.test(src) ? null : false
    },
  }],
  renderHTML: ({ HTMLAttributes }) => ['img', { ...HTMLAttributes, style: 'max-width:100%;height:auto' }],
  addNodeView() {
    return ({ node }) => {
      const img = document.createElement('img')
      img.className = 'gm-inline-image'
      img.draggable = true
      for (const k of ['alt', 'width', 'height'] as const) if (node.attrs[k]) img.setAttribute(k, String(node.attrs[k]))
      const stop = watchEffect(() => {
        const src = this.options.resolve(node.attrs.src as string)
        if (src) img.src = src
        else img.removeAttribute('src')
      })
      return { dom: img, destroy: stop }
    }
  },
})
