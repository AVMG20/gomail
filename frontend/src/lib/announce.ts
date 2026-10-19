// Screen-reader announcements through a single polite live region.
let region: HTMLElement | null = null

export function announce(message: string) {
  if (typeof document === 'undefined') return
  if (!region) {
    region = document.createElement('div')
    region.setAttribute('role', 'status')
    region.setAttribute('aria-live', 'polite')
    region.setAttribute('aria-atomic', 'true')
    region.className = 'sr-only'
    document.body.appendChild(region)
  }
  // Clear first so repeating the same text is announced again.
  region.textContent = ''
  setTimeout(() => {
    if (region) region.textContent = message
  }, 50)
}
