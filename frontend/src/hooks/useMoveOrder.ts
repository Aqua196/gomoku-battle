import { useRef } from 'react'
import type { GameState, Point } from '../ws/protocol'

const EMPTY: ReadonlyMap<string, number> = new Map()

/** Move numbers ("x,y" → n) for the first `upto` moves of a game record. */
export function orderOf(moves: readonly Point[], upto = moves.length): Map<string, number> {
  const order = new Map<string, number>()
  for (let i = 0; i < Math.min(upto, moves.length); i++) order.set(`${moves[i].x},${moves[i].y}`, i + 1)
  return order
}

/**
 * Move numbers per point ("x,y" → n). v1.1 servers send the full move list
 * in every snapshot, so numbers are exact even after a resume or when
 * spectating mid-game. Against a v1 server the order is reconstructed from
 * the stream of snapshots via lastMove + moveNumber, and stones already on
 * the board when this view mounted stay unnumbered.
 */
export function useMoveOrder(game: GameState | null): ReadonlyMap<string, number> {
  const ref = useRef<{ roomId: string; order: Map<string, number> } | null>(null)
  if (!game) return EMPTY
  if (ref.current?.roomId !== game.roomId) {
    ref.current = { roomId: game.roomId, order: new Map() }
  }
  if (game.moves) {
    if (ref.current.order.size !== game.moves.length) ref.current.order = orderOf(game.moves)
    return ref.current.order
  }
  const { order } = ref.current
  const lm = game.lastMove
  if (lm && game.moveNumber > 0) {
    const key = `${lm.x},${lm.y}`
    if (!order.has(key)) order.set(key, game.moveNumber)
  }
  return order
}
