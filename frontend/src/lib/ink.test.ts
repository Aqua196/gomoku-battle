import { describe, expect, it } from 'vitest'
import { blobPath, fbm, noise1, pointSeed, ringPath, rng, wobbleLine } from './ink'

describe('rng', () => {
  it('is deterministic per seed and stays in [0, 1)', () => {
    const a = rng(42)
    const b = rng(42)
    const xs = Array.from({ length: 200 }, () => a())
    expect(xs).toEqual(Array.from({ length: 200 }, () => b()))
    expect(xs.every((v) => v >= 0 && v < 1)).toBe(true)
    expect(rng(43)()).not.toBe(xs[0])
  })
})

describe('noise', () => {
  it('stays within [-1, 1] and is continuous', () => {
    for (let x = 0; x < 20; x += 0.07) {
      const v = noise1(x, 3)
      expect(Math.abs(v)).toBeLessThanOrEqual(1)
      expect(Math.abs(noise1(x + 0.001, 3) - v)).toBeLessThan(0.01)
      expect(Math.abs(fbm(x, 3))).toBeLessThanOrEqual(1)
    }
  })
})

describe('paths', () => {
  it('blobPath is closed, seed-stable, and differs across seeds', () => {
    const p = blobPath(7, 40)
    expect(p.startsWith('M')).toBe(true)
    expect(p.endsWith('Z')).toBe(true)
    expect(blobPath(7, 40)).toBe(p)
    expect(blobPath(8, 40)).not.toBe(p)
  })

  it('blob points stay near the requested radius', () => {
    const nums = blobPath(11, 40, 0.05).match(/-?\d+\.\d+/g)!.map(Number)
    for (let i = 0; i + 1 < nums.length; i += 6) {
      const r = Math.hypot(nums[i], nums[i + 1])
      expect(r).toBeGreaterThan(36)
      expect(r).toBeLessThan(44)
    }
  })

  it('ringPath holds two closed contours', () => {
    expect(ringPath(5, 42, 5).match(/Z/g)).toHaveLength(2)
  })

  it('wobbleLine starts and ends exactly on the endpoints', () => {
    const d = wobbleLine(1, 0.5, 2.5, 14.5, 2.5, 0.05)
    expect(d.startsWith('M0.50,2.50')).toBe(true)
    expect(d.endsWith('L14.50,2.50')).toBe(true)
  })

  it('pointSeed separates neighbouring points', () => {
    const seeds = new Set<number>()
    for (let x = 0; x < 15; x++) for (let y = 0; y < 15; y++) seeds.add(pointSeed(x, y))
    expect(seeds.size).toBe(225)
  })
})
