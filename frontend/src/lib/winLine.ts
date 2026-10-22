import type { Board } from '../ws/protocol'

export interface Point {
  x: number
  y: number
}

const DIRECTIONS = [
  [1, 0],
  [0, 1],
  [1, 1],
  [1, -1],
] as const

/**
 * The run of ≥5 same-colored stones passing through `at`, ordered end to
 * end, or null. Display-only (win-line highlight): the server stays the
 * authority on who won.
 */
export function findWinLine(board: Board, at: Point | null): Point[] | null {
  if (!at) return null
  const color = board[at.y]?.[at.x]
  if (!color) return null
  for (const [dx, dy] of DIRECTIONS) {
    const run: Point[] = [at]
    for (const sign of [1, -1]) {
      let x = at.x + dx * sign
      let y = at.y + dy * sign
      while (board[y]?.[x] === color) {
        if (sign === 1) run.push({ x, y })
        else run.unshift({ x, y })
        x += dx * sign
        y += dy * sign
      }
    }
    if (run.length >= 5) return run
  }
  return null
}

/** Build a board from a move list (black first) — used by the hero demo. */
export function boardFromMoves(size: number, moves: readonly Point[]): Board {
  const board: Board = Array.from({ length: size }, () => Array<0>(size).fill(0))
  moves.forEach((m, i) => {
    board[m.y][m.x] = i % 2 === 0 ? 1 : 2
  })
  return board
}
