import { pointSeed } from '../lib/ink'
import type { Color, GamePlayer } from '../ws/protocol'
import { InkStone } from './InkStone'
import { Roll } from './Motion'

const pad = (n: number) => String(n).padStart(2, '0')

/**
 * One seat: an ink stone as the avatar, the name, and the turn clock as a
 * stick of incense burning down — its ember glows while the seat is to move.
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
  const left = active && turnLeftMs !== null ? Math.min(1, turnLeftMs / turnTotalMs) : 1
  const danger = active && secs !== null && secs <= 10

  return (
    <div className={`pp ${color}${active ? ' active' : ''}${online ? '' : ' offline'}${isYou ? ' you' : ''}`}>
      <span className="pp-avatar" aria-hidden>
        <InkStone color={color} seed={pointSeed(color === 'black' ? 3 : 11, 7, 1)} />
      </span>
      <div className="pp-body">
        <div className="pp-name">
          <span className="pp-nick">{player.nick}</span>
          {isYou && <em className="tag you">你</em>}
          {player.isAI && <em className="tag ai">AI</em>}
        </div>
        <div className="pp-meta">
          <span className="serif">执{color === 'black' ? '黑' : '白'}</span>
          <span className={online ? 'on' : 'off'}>
            {online ? '在线' : '断线'}
            {!online && offlineLeftMs !== null && <span className="latin"> {Math.ceil(offlineLeftMs / 1000)}s</span>}
          </span>
          <span>
            <span className="latin">{pad(stones)}</span> 子
          </span>
        </div>
        <div className={`incense${active ? ' lit' : ''}${danger ? ' danger' : ''}`} aria-hidden>
          <span className="incense-stick" style={{ width: `${left * 100}%` }} />
        </div>
      </div>
      <span className={`pp-secs latin${danger ? ' danger' : ''}`}>
        {active && secs !== null ? <Roll value={pad(secs)} /> : '—'}
      </span>
    </div>
  )
}
