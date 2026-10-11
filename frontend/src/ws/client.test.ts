import { describe, expect, it } from 'vitest'
import { computeBackoffMs } from './client'

describe('computeBackoffMs', () => {
  it('grows exponentially within the jitter band', () => {
    for (let i = 0; i < 200; i++) {
      const ms = computeBackoffMs(2, 1000, 15000)
      expect(ms).toBeGreaterThanOrEqual(2000) // base * 2^2 * 0.5
      expect(ms).toBeLessThanOrEqual(4000) // base * 2^2 * 1.0
    }
  })

  it('attempt 0 waits at most baseMs', () => {
    for (let i = 0; i < 100; i++) {
      expect(computeBackoffMs(0, 1000)).toBeLessThanOrEqual(1000)
      expect(computeBackoffMs(0, 1000)).toBeGreaterThanOrEqual(500)
    }
  })

  it('is capped at capMs', () => {
    for (let i = 0; i < 100; i++) {
      expect(computeBackoffMs(100, 1000, 15000)).toBeLessThanOrEqual(15000)
    }
  })

  it('handles negative attempts as 0', () => {
    expect(computeBackoffMs(-5, 1000)).toBeLessThanOrEqual(1000)
  })
})
