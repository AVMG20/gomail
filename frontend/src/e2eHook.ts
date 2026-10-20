// TEMPORARY (real-app verification only, VITE_E2E builds): Ctrl+Alt+Shift+O
// emits calendar:open through the Wails runtime for an event 1–4 weeks out,
// exactly like a reminder-notification click.
import { Events } from '@wailsio/runtime'
import { api } from '@/lib/api'

window.addEventListener('keydown', async (e) => {
  if (!(e.ctrlKey && e.altKey && e.shiftKey && e.key.toLowerCase() === 'o')) return
  e.preventDefault()
  const now = Date.now()
  const evs = await api.listEvents(new Date(now + 7 * 864e5).toISOString(), new Date(now + 28 * 864e5).toISOString())
  const ev = evs.find((x) => !x.allDay) ?? evs[0]
  console.log('[e2e] emitting calendar:open', ev?.id, ev?.title)
  document.title = `e2e:${ev?.title ?? 'none'}`
  if (ev) void Events.Emit('calendar:open', { eventId: ev.id })
})

// Ctrl+Alt+Shift+D: report leftover selection/mirror elements in the window title.
window.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey && e.altKey && e.shiftKey && e.key.toLowerCase() === 'd')) return
  const q = (s: string) => document.querySelectorAll(s).length
  let box = document.getElementById('e2e-dbg')
  if (!box) {
    box = document.createElement('pre')
    box.id = 'e2e-dbg'
    box.style.cssText = 'position:fixed;left:8px;bottom:8px;z-index:99999;max-width:900px;white-space:pre-wrap;font:11px monospace;background:#ff0;color:#000;padding:6px'
    document.body.appendChild(box)
  }
  const els = [...document.querySelectorAll<HTMLElement>('.fc-event-mirror, .fc-highlight')]
  box.textContent = `mirror=${q('.fc-event-mirror')} highlight=${q('.fc-highlight')} harness=${q('.fc-timegrid-event-harness')} cards=${q('[data-floating-card]')}\n` +
    els.map((el) => `${el.className} :: ${JSON.stringify(el.getBoundingClientRect())} :: ${el.outerHTML.slice(0, 300)}`).join('\n')
})
