/**
 * Game record API (PROTOCOL §12): plain fetches against the server that
 * also serves the app. Records never change once written, so the browser
 * cache does the rest for single games.
 */
import type { Color, GameOverReason, GameRecord } from '../ws/protocol'

export class RecordError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, headers: { Accept: 'application/json' } })
  if (!res.ok) throw new RecordError(res.status, `${path}: HTTP ${res.status}`)
  return (await res.json()) as T
}

export function fetchGame(id: string, signal?: AbortSignal): Promise<GameRecord> {
  return getJSON<GameRecord>(`/api/games/${encodeURIComponent(id)}`, signal)
}

export async function fetchGames(
  scope: { playerId: string } | 'recent',
  limit = 12,
  signal?: AbortSignal,
): Promise<GameRecord[]> {
  const path =
    scope === 'recent'
      ? `/api/games?limit=${limit}`
      : `/api/players/${encodeURIComponent(scope.playerId)}/games?limit=${limit}`
  return (await getJSON<{ games: GameRecord[] }>(path, signal)).games
}

export const REASON_SHORT: Record<GameOverReason, string> = {
  five: '五连',
  draw: '满盘',
  timeout: '超时',
  resign: '认输',
  disconnect_timeout: '断线',
  abandoned: '弃局',
}

/** 「黑胜 · 五连」 style verdict of a record. */
export function verdict(rec: Pick<GameRecord, 'winner' | 'reason'>): string {
  const who: Record<Color, string> = { black: '黑胜', white: '白胜' }
  const head = rec.winner ? who[rec.winner] : rec.reason === 'abandoned' ? '弃局' : '和棋'
  return rec.winner ? `${head} · ${REASON_SHORT[rec.reason]}` : head
}

/** The viewer's result in a record they played: win / lose / draw, or null. */
export function resultFor(rec: GameRecord, playerId: string | null): 'win' | 'lose' | 'draw' | null {
  const mine: Color | null =
    rec.black.playerId === playerId ? 'black' : rec.white.playerId === playerId ? 'white' : null
  if (!mine) return null
  if (!rec.winner) return 'draw'
  return rec.winner === mine ? 'win' : 'lose'
}

/** 10/26 18:05 — compact local date for list rows. */
export function shortDate(ts: number): string {
  const d = new Date(ts)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** 12:34 → "12 分 34 秒" for game durations. */
export function duration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  const m = Math.floor(s / 60)
  return m > 0 ? `${m} 分 ${s % 60} 秒` : `${s} 秒`
}
