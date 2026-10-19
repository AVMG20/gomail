// Light date buckets for the message list: Today, Yesterday, This week,
// Last week, then month (and year when not the current one).
const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long' })
const monthYearFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })

let todayStart = 0
let computedFor = ''
function refreshAnchors() {
  const now = new Date()
  const key = now.toDateString()
  if (key === computedFor) return
  computedFor = key
  todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
}

const monthCache = new Map<number, string>()

export function dateGroup(iso: string): string {
  refreshAnchors()
  const t = Date.parse(iso)
  const day = 86400000
  const now = new Date(todayStart)
  const weekday = (now.getDay() + 6) % 7 // Monday = 0
  const weekStart = todayStart - weekday * day
  if (t >= todayStart) return 'Today'
  if (t >= todayStart - day) return 'Yesterday'
  if (t >= weekStart) return 'This week'
  if (t >= weekStart - 7 * day) return 'Last week'
  const d = new Date(t)
  const key = d.getFullYear() * 12 + d.getMonth()
  let label = monthCache.get(key)
  if (!label) {
    label = d.getFullYear() === now.getFullYear() ? monthFmt.format(d) : monthYearFmt.format(d)
    monthCache.set(key, label)
  }
  return label
}
