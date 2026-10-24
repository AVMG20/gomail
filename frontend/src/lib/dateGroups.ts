// Light date buckets for the message list: Today, Yesterday, This week,
// Last week, then month (and year when not the current one).
const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long' })
const monthYearFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })

// Day starts are local midnights from calendar-day arithmetic: a day with a
// DST change has 23 or 25 hours.
let todayStart = 0
let yesterdayStart = 0
let weekStart = 0
let lastWeekStart = 0
let thisYear = 0
let computedFor = ''
const monthCache = new Map<number, string>() // labels depend on the current year
function refreshAnchors() {
  const now = new Date()
  const key = now.toDateString()
  if (key === computedFor) return
  computedFor = key
  monthCache.clear()
  const y = now.getFullYear()
  const m = now.getMonth()
  const d = now.getDate()
  const weekday = (now.getDay() + 6) % 7 // Monday = 0
  todayStart = new Date(y, m, d).getTime()
  yesterdayStart = new Date(y, m, d - 1).getTime()
  weekStart = new Date(y, m, d - weekday).getTime()
  lastWeekStart = new Date(y, m, d - weekday - 7).getTime()
  thisYear = y
}

export function dateGroup(iso: string): string {
  refreshAnchors()
  const t = Date.parse(iso)
  if (t >= todayStart) return 'Today'
  if (t >= yesterdayStart) return 'Yesterday'
  if (t >= weekStart) return 'This week'
  if (t >= lastWeekStart) return 'Last week'
  const d = new Date(t)
  const key = d.getFullYear() * 12 + d.getMonth()
  let label = monthCache.get(key)
  if (!label) {
    label = d.getFullYear() === thisYear ? monthFmt.format(d) : monthYearFmt.format(d)
    monthCache.set(key, label)
  }
  return label
}
