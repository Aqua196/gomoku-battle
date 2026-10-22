/**
 * AI-opponent view derivations (M2). Pure helpers shared by Lobby/Game and
 * unit-tested directly — no protocol additions, everything derives from the
 * existing GameState fields (§3).
 */
import type { Color, Difficulty, GamePlayer, GameState } from '../ws/protocol'

export const DIFFICULTIES: { id: Difficulty; label: string }[] = [
  { id: 'easy', label: '简单' },
  { id: 'normal', label: '普通' },
  { id: 'hard', label: '困难' },
]

const DIFFICULTY_LABEL: Record<Difficulty, string> = {
  easy: '简单',
  normal: '普通',
  hard: '困难',
}

/** The AI seat of a game, regardless of which color it plays. */
export function aiOpponent(game: GameState): GamePlayer | null {
  if (game.players.black.isAI) return game.players.black
  if (game.players.white.isAI) return game.players.white
  return null
}

/** Color of the AI seat; null for human-vs-human games. */
export function aiSeatColor(game: GameState): Color | null {
  if (game.players.black.isAI) return 'black'
  if (game.players.white.isAI) return 'white'
  return null
}

/** Difficulty of an AI seat parsed from its `AI (difficulty)` nick (§10). */
export function aiDifficulty(aiPlayer: GamePlayer | null): Difficulty | null {
  if (!aiPlayer?.isAI) return null
  const key = /^AI\s*\(([^)]+)\)/.exec(aiPlayer.nick)?.[1]?.trim().toLowerCase()
  return key && key in DIFFICULTY_LABEL ? (key as Difficulty) : null
}

/**
 * 「人机 · 难度」badge text for an AI opponent, e.g. 人机 · 普通.
 * Contract (PROTOCOL §10): the server names AI seats `AI (difficulty)`
 * where difficulty is easy|normal|hard — the badge parses that nick
 * rather than relying on an extra field. Falls back to the raw
 * parenthetical / nick. Returns null for human opponents.
 */
export function aiBadgeLabel(aiPlayer: GamePlayer | null): string | null {
  if (!aiPlayer?.isAI) return null
  const m = /^AI\s*\(([^)]+)\)/.exec(aiPlayer.nick)
  const key = m?.[1]?.trim().toLowerCase()
  const diff =
    key && key in DIFFICULTY_LABEL
      ? DIFFICULTY_LABEL[key as Difficulty]
      : (m?.[1]?.trim() || aiPlayer.nick)
  return `人机 · ${diff}`
}

/**
 * Between my move landing (game_state, turn = AI) and the AI's reply
 * (game_state, turn = me) the board shows 「AI 思考中…」. Derives purely
 * from turn ownership vs the AI seat's color — correct even when
 * youColor is still being derived (resume edge, R9).
 */
export function isAiThinking(game: GameState, over: boolean): boolean {
  if (over || game.status !== 'playing') return false
  return aiSeatColor(game) === game.turn
}
