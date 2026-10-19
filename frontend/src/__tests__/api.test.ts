import { beforeEach, describe, expect, it } from 'vitest'
import { normMessage, normSettings, normSummary, writeLocal } from '@/lib/api'
import type { Message, MessageSummary, Settings } from '@/lib/types'

describe('api normalisation (Go nil slices / legacy fields)', () => {
  beforeEach(() => localStorage.clear())

  it('fills nil slices on summaries and maps legacy category', () => {
    const m = normSummary({ id: '1', folderIds: null, to: null, labels: null, labelDetails: null, from: null, category: 'work' } as unknown as MessageSummary)
    expect(m.folderIds).toEqual([])
    expect(m.to).toEqual([])
    expect(m.labels).toEqual(['work'])
    expect(m.labelDetails).toEqual([])
    expect(m.from).toEqual({ name: '', email: '' })
  })

  it('fills nil slices and bodies on full messages', () => {
    const m = normMessage({ id: '1', cc: null, bcc: null, replyTo: null, attachments: null, bodyHtml: null, bodyText: null } as unknown as Message)
    expect([m.cc, m.bcc, m.replyTo, m.attachments]).toEqual([[], [], [], []])
    expect(m.bodyHtml).toBe('')
    expect(m.bodyText).toBe('')
    expect(m.labels).toEqual([])
  })

  it('defaults settings and maps legacy categories to labels', () => {
    const s = normSettings({ theme: '', density: '', syncIntervalMinutes: 0, radius: undefined, categories: [{ id: 'a', name: 'A', description: '', color: '#f00' }] } as unknown as Settings)
    expect(s.theme).toBe('system')
    expect(s.density).toBe('comfortable')
    expect(s.syncIntervalMinutes).toBe(5)
    expect(s.labels.map((l) => l.id)).toEqual(['a'])
    expect(s.classifierModel).toBe('typesafe/jev-1.13')
    expect(s.classifyThreshold).toBe(0.5)
    expect(s.aiDailyBudgetUsd).toBe(1)
    expect(s.syncDepthDays).toBe(90)
    expect(s.emailBackground).toBe('auto')
    expect(s.checkUpdates).toBe(true)
    expect(normSettings({ emailBackground: 'light' } as unknown as Settings).emailBackground).toBe('light')
    expect(normSettings({ emailBackground: 'bogus' } as unknown as Settings).emailBackground).toBe('auto')
  })

  it('keeps explicit zeros and falls back to locally stored prefs for older backends', () => {
    writeLocal('readingPane', 'off')
    const s = normSettings({ radius: 0, aiDailyBudgetUsd: 0, syncDepthDays: 0, labels: [] } as unknown as Settings)
    expect(s.radius).toBe(0)
    expect(s.aiDailyBudgetUsd).toBe(0)
    expect(s.syncDepthDays).toBe(0)
    expect(s.readingPane).toBe('off')
  })
})
