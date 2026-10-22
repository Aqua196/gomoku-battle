import { describe, expect, it } from 'vitest'
import { boardFromMoves, findWinLine } from './winLine'
import type { Board } from '../ws/protocol'

function empty(): Board {
  return Array.from({ length: 15 }, () => Array<0>(15).fill(0))
}

describe('findWinLine', () => {
  it('finds a horizontal five through the last move, ordered end to end', () => {
    const b = empty()
    for (let x = 3; x <= 7; x++) b[4][x] = 1
    expect(findWinLine(b, { x: 5, y: 4 })).toEqual([
      { x: 3, y: 4 },
      { x: 4, y: 4 },
      { x: 5, y: 4 },
      { x: 6, y: 4 },
      { x: 7, y: 4 },
    ])
  })

  it('finds both diagonals and includes overlines (≥5)', () => {
    const b = empty()
    for (let i = 0; i < 6; i++) b[2 + i][2 + i] = 2
    expect(findWinLine(b, { x: 4, y: 4 })).toHaveLength(6)

    const c = empty()
    for (let i = 0; i < 5; i++) c[10 - i][i] = 1
    expect(findWinLine(c, { x: 0, y: 10 })?.[4]).toEqual({ x: 4, y: 6 })
  })

  it('returns null for four in a row, an empty point or no move', () => {
    const b = empty()
    for (let y = 0; y < 4; y++) b[y][9] = 1
    expect(findWinLine(b, { x: 9, y: 3 })).toBeNull()
    expect(findWinLine(b, { x: 0, y: 0 })).toBeNull()
    expect(findWinLine(b, null)).toBeNull()
  })

  it('does not join stones of the other color', () => {
    const b = empty()
    b[0][0] = 1
    b[0][1] = 1
    b[0][2] = 2
    b[0][3] = 1
    b[0][4] = 1
    expect(findWinLine(b, { x: 1, y: 0 })).toBeNull()
  })
})

describe('boardFromMoves', () => {
  it('alternates black and white starting with black', () => {
    const b = boardFromMoves(15, [
      { x: 7, y: 7 },
      { x: 8, y: 7 },
      { x: 7, y: 8 },
    ])
    expect(b[7][7]).toBe(1)
    expect(b[7][8]).toBe(2)
    expect(b[8][7]).toBe(1)
  })
})
