import { useRef } from 'react'
import type { GameState } from '../ws/protocol'

const EMPTY: ReadonlyMap<string, number> = new Map()

/**
 * Move numbers per point ("x,y" → n), reconstructed from the stream of full
 * game_state snapshots via lastMove + moveNumber. Stones already on the
 * board when this view mounted (e.g. after a resume) stay unnumbered.
 */
export function useMoveOrder(game: GameState | null): ReadonlyMap<string, number> {
  const ref = useRef<{ roomId: string; order: Map<string, number> } | null>(null)
  if (!game) return EMPTY
  if (ref.current?.roomId !== game.roomId) {
    ref.current = { roomId: game.roomId, order: new Map() }
  }
  const { order } = ref.current
  const lm = game.lastMove
  if (lm && game.moveNumber > 0) {
    const key = `${lm.x},${lm.y}`
    if (!order.has(key)) order.set(key, game.moveNumber)
  }
  return order
}
