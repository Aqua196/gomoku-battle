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

/**
 * 「人机 · 难度」badge text for an AI opponent, e.g. 人机 · 普通.
 * The server names AI seats `AI (easy|normal|hard)` — the difficulty is
 * parsed out of the nick; falls back to the raw parenthetical / nick.
 * Returns null for human opponents.
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
 * from turn ownership; no new message type.
 */
export function isAiThinking(
  game: GameState,
  aiPlayer: GamePlayer | null,
  youColor: Color | null,
  over: boolean,
): boolean {
  if (!aiPlayer?.isAI || over || game.status !== 'playing') return false
  return youColor !== null && game.turn !== youColor
}
