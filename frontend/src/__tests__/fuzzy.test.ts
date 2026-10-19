import { describe, expect, it } from 'vitest'
import { fuzzyFilter, fuzzyScore } from '@/lib/fuzzy'

describe('fuzzyScore', () => {
  it('returns null when the query is not a subsequence', () => {
    expect(fuzzyScore('xyz', 'Inbox')).toBeNull()
  })

  it('returns 0 for an empty query', () => {
    expect(fuzzyScore('  ', 'anything')).toBe(0)
  })

  it('ranks prefix > word start > middle substring', () => {
    const prefix = fuzzyScore('arc', 'Archive')!
    const word = fuzzyScore('arc', 'Old archive')!
    const middle = fuzzyScore('chi', 'Archive')!
    expect(prefix).toBeGreaterThan(word)
    expect(word).toBeGreaterThan(middle)
  })

  it('scores direct substrings above scattered subsequences', () => {
    expect(fuzzyScore('sent', 'Sent items')!).toBeGreaterThan(fuzzyScore('snt', 'Sent items')!)
  })

  it('rewards word starts in subsequence matches', () => {
    expect(fuzzyScore('mn', 'Morning News')!).toBeGreaterThan(fuzzyScore('mn', 'Ammonia')!)
  })

  it('is case-insensitive', () => {
    expect(fuzzyScore('INBOX', 'inbox')).not.toBeNull()
  })
})

describe('fuzzyFilter', () => {
  const items = ['Settings', 'Sent', 'Starred', 'Spam', 'Archive']
  it('sorts by score and limits', () => {
    expect(fuzzyFilter('se', items, (x) => x, 2)).toEqual(['Sent', 'Settings'])
  })
  it('returns the first items for an empty query', () => {
    expect(fuzzyFilter('', items, (x) => x, 3)).toEqual(['Settings', 'Sent', 'Starred'])
  })
  it('drops non-matches', () => {
    expect(fuzzyFilter('zz', items, (x) => x)).toEqual([])
  })
})
