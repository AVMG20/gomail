// Global error reporting: anything uncaught becomes a toast with "Copy details"
// instead of a silent failure or a blank screen.
import type { App } from 'vue'
import { toast } from 'vue-sonner'
import { errorMessage } from './api'

/** Plain-text details for bug reports (message, stack, where, app state). */
export function errorDetails(err: unknown, where = ''): string {
  const e = err instanceof Error ? err : null
  return [
    `gomail error${where ? ` in ${where}` : ''}`,
    `Message: ${e ? e.message : String(err)}`,
    e?.stack ? `Stack:\n${e.stack}` : '',
    `When: ${new Date().toISOString()}`,
    `Where: ${location.pathname}${location.search}`,
    `Agent: ${navigator.userAgent}`,
  ].filter(Boolean).join('\n')
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // WebKitGTK may refuse the async clipboard: fall back to a hidden textarea.
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.cssText = 'position:fixed;opacity:0;top:0;left:0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

let last = { key: '', at: 0 }

/** Show one toast per distinct error (same error within 3s is collapsed). */
export function reportError(err: unknown, where = ''): void {
  console.error('[gomail]', where || 'error', err)
  const message = errorMessage(err) || 'Something went wrong'
  const key = `${where}|${message}`
  const now = Date.now()
  if (key === last.key && now - last.at < 3000) return
  last = { key, at: now }
  const details = errorDetails(err, where)
  toast.error('Something went wrong', {
    description: message,
    duration: 8000,
    action: {
      label: 'Copy details',
      onClick: () => void copyText(details).then((ok) => ok && toast('Error details copied')),
    },
  })
}

function ignorable(reason: unknown): boolean {
  const msg = reason instanceof Error ? `${reason.name} ${reason.message}` : String(reason ?? '')
  // Benign browser noise: aborted fetches, ResizeObserver loop notices, cancelled navigation.
  return /AbortError|ResizeObserver loop|cancell?ed/i.test(msg)
}

export function installErrorHandlers(app: App): void {
  app.config.errorHandler = (err, _instance, info) => {
    if (!ignorable(err)) reportError(err, info)
  }
  window.addEventListener('unhandledrejection', (e) => {
    if (ignorable(e.reason)) return
    reportError(e.reason, 'background task')
  })
  window.addEventListener('error', (e) => {
    // Resource load errors (images in mail frames etc.) have no error object.
    if (!e.error || ignorable(e.error)) return
    reportError(e.error, 'script')
  })
}
