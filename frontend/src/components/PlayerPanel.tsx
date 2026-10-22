import type { Color, GamePlayer } from '../ws/protocol'
import { Roll } from './Motion'

const TICKS = 30
const pad = (n: number) => String(n).padStart(2, '0')

/**
 * One seat: an oversized side glyph (黑 / 白) as the anchor, name and state
 * in type, and a hairline tick clock. No boxes — the active seat lights a
 * bar down its left edge.
 */
export function PlayerPanel({
  color,
  player,
  isYou,
  active,
  online,
  turnLeftMs,
  turnTotalMs,
  offlineLeftMs,
  stones,
}: {
  color: Color
  player: GamePlayer
  isYou: boolean
  active: boolean
  online: boolean
  turnLeftMs: number | null
  turnTotalMs: number
  offlineLeftMs: number | null
  stones: number
}) {
  // Clock-offset jitter can put the deadline a hair past the full window.
  const secs = turnLeftMs !== null ? Math.min(Math.ceil(turnLeftMs / 1000), turnTotalMs / 1000) : null
  const lit = active && turnLeftMs !== null ? Math.ceil((turnLeftMs / turnTotalMs) * TICKS) : 0
  const danger = active && secs !== null && secs <= 10

  return (
    <div className={`pp ${color}${active ? ' active' : ''}${online ? '' : ' offline'}${isYou ? ' you' : ''}`}>
      <span className="pp-glyph" aria-hidden>
        {color === 'black' ? '黑' : '白'}
      </span>
      <div className="pp-body">
        <div className="pp-name">
          <span className="pp-nick">{player.nick}</span>
          {isYou && <em className="tag you">你</em>}
          {player.isAI && <em className="tag ai">AI</em>}
        </div>
        <div className="pp-meta">
          <span className="en">{color}</span>
          <span className={online ? 'on' : 'off'}>
            {online ? '在线' : '断线'}
            {!online && offlineLeftMs !== null && <span className="mono"> {Math.ceil(offlineLeftMs / 1000)}s</span>}
          </span>
          <span className="mono">{pad(stones)} 子</span>
        </div>
        <div className={`ticks${danger ? ' danger' : ''}`} aria-hidden>
          {Array.from({ length: TICKS }, (_, i) => (
            <i key={i} className={i < lit ? 'on' : ''} />
          ))}
        </div>
      </div>
      <span className={`pp-secs mono${danger ? ' danger' : ''}`}>
        {active && secs !== null ? <Roll value={pad(secs)} /> : '--'}
      </span>
    </div>
  )
}
