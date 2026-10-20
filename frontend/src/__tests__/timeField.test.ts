// Time fields and displayed times follow Settings → Time format (not the OS locale).
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, nextTick } from 'vue'
import TimeField from '@/components/calendar/TimeField.vue'
import { setClockPreference } from '@/lib/calendar'
import { listDate } from '@/lib/format'

function mount(model: string) {
  let value = model
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(TimeField, { modelValue: value, label: 'Start time', 'onUpdate:modelValue': (v: string) => (value = v) })
  app.mount(el)
  return { input: el.querySelector('input')!, get value() { return value }, unmount: () => { app.unmount(); el.remove() } }
}
const key = (el: HTMLElement, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))
async function type(input: HTMLInputElement, text: string) {
  input.focus()
  input.value = text
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

afterEach(() => setClockPreference('auto'))

describe('TimeField', () => {
  it('shows the time on the 24-hour clock when that is the setting', async () => {
    setClockPreference('24h')
    const f = mount('13:00')
    expect(f.input.value).toBe('13:00')
    f.unmount()
  })

  it('shows AM/PM only with the 12-hour setting', async () => {
    setClockPreference('12h')
    const f = mount('13:00')
    expect(f.input.value).toBe('1:00 PM')
    f.unmount()
  })

  it('re-renders when the setting changes', async () => {
    setClockPreference('12h')
    const f = mount('13:00')
    setClockPreference('24h')
    await nextTick()
    expect(f.input.value).toBe('13:00')
    f.unmount()
  })

  it('Enter takes exactly the typed time, not the nearest slot in the list', async () => {
    setClockPreference('24h')
    const f = mount('09:00')
    await type(f.input, '9:10')
    key(f.input, 'Enter')
    await nextTick()
    expect(f.value).toBe('09:10')
    expect(f.input.value).toBe('09:10')
    f.unmount()
  })

  it('↓ then Enter picks from the list', async () => {
    setClockPreference('24h')
    const f = mount('09:00')
    f.input.focus()
    key(f.input, 'ArrowDown') // opens the list on 09:00
    await nextTick()
    key(f.input, 'ArrowDown') // 09:15
    key(f.input, 'Enter')
    await nextTick()
    expect(f.value).toBe('09:15')
    f.unmount()
  })

  it('snaps unreadable text back to the current time on blur', async () => {
    setClockPreference('24h')
    const f = mount('14:30')
    await type(f.input, 'soon')
    f.input.dispatchEvent(new FocusEvent('blur'))
    await nextTick()
    expect(f.value).toBe('14:30')
    expect(f.input.value).toBe('14:30')
    f.unmount()
  })
})

describe('mail list times', () => {
  it('follow the Time format setting', () => {
    const today = new Date()
    today.setHours(13, 5, 0, 0)
    setClockPreference('24h')
    expect(listDate(today.toISOString())).toMatch(/13[:.]05/)
    expect(listDate(today.toISOString())).not.toMatch(/PM/i)
    setClockPreference('12h')
    expect(listDate(today.toISOString())).toMatch(/1[:.]05\s?PM/i)
  })

  it('never shows 24:xx after midnight', () => {
    const d = new Date()
    d.setHours(0, 5, 0, 0)
    setClockPreference('24h')
    expect(listDate(d.toISOString())).not.toMatch(/^24/)
  })
})
