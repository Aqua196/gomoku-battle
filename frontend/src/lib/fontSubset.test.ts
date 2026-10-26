import { describe, expect, it } from 'vitest'
import subsetChars from '../assets/fonts/serif-subset.chars.txt?raw'

// Same character class and file selection as scripts/subset-serif-font.py.
const CJK = /[\u3000-\u303f\u4e00-\u9fff\uff00-\uffef]/g
const sources = import.meta.glob<string>(['../**/*.{ts,tsx}', '!../**/*.test.*'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

describe('serif font subset', () => {
  it('covers every CJK character used in the UI source', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(20)
    const subset = new Set(subsetChars)
    const missing = new Set<string>()
    for (const text of Object.values(sources)) {
      for (const ch of text.match(CJK) ?? []) {
        if (!subset.has(ch)) missing.add(ch)
      }
    }
    // Fix: rerun scripts/subset-serif-font.py (see frontend/README.md).
    expect([...missing].join('')).toBe('')
  })
})
