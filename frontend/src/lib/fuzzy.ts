// Tiny fuzzy scorer for local command palette matching. Returns null when
// `query` is not a subsequence of `text`; otherwise a score (higher = better)
// rewarding prefix, word-start and consecutive matches.
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLowerCase()
  if (!q) return 0
  const t = text.toLowerCase()
  const direct = t.indexOf(q)
  if (direct >= 0) return 100 - direct + (direct === 0 || /\W/.test(t[direct - 1] ?? ' ') ? 40 : 0) + q.length
  let score = 0
  let ti = 0
  let streak = 0
  for (let qi = 0; qi < q.length; qi++) {
    const ch = q[qi]
    if (ch === ' ') continue
    const found = t.indexOf(ch, ti)
    if (found < 0) return null
    const wordStart = found === 0 || /[\s\-_/.:@]/.test(t[found - 1])
    streak = found === ti ? streak + 1 : 0
    score += 1 + streak * 2 + (wordStart ? 6 : 0) - Math.min(4, found - ti) * 0.5
    ti = found + 1
  }
  return score
}

export function fuzzyFilter<T>(query: string, items: T[], key: (item: T) => string, limit = 50): T[] {
  if (!query.trim()) return items.slice(0, limit)
  const scored: { item: T; s: number }[] = []
  for (const item of items) {
    const s = fuzzyScore(query, key(item))
    if (s !== null) scored.push({ item, s })
  }
  scored.sort((a, b) => b.s - a.s)
  return scored.slice(0, limit).map((x) => x.item)
}
