import type { Color, GamePlayer } from '../ws/protocol'

const SEGMENTS = 30
const pad = (n: number) => String(n).padStart(2, '0')

/** One seat as a spec sheet: side tag, name, state rows and a segmented turn clock. */
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
  const lit = active && turnLeftMs !== null ? Math.ceil((turnLeftMs / turnTotalMs) * SEGMENTS) : 0
  const danger = active && secs !== null && secs <= 10

  return (
    <div className={`pp ${color}${active ? ' active' : ''}${online ? '' : ' offline'}${isYou ? ' you' : ''}`}>
      <div className="pp-head">
        <span className={`pp-side ${color}`}>
          {color === 'black' ? '黑' : '白'}
          <small className="en">{color}</small>
        </span>
        <span className="pp-nick">{player.nick}</span>
        {isYou && <em className="tag you">你</em>}
        {player.isAI && <em className="tag ai">AI</em>}
      </div>

      <dl className="pp-spec">
        <div>
          <dt>状态</dt>
          <dd className={online ? '' : 'alert'}>
            {online ? '在线' : '断线'}
            {!online && offlineLeftMs !== null && <span className="mono"> · {Math.ceil(offlineLeftMs / 1000)}s</span>}
          </dd>
        </div>
        <div>
          <dt>落子</dt>
          <dd className="mono">{pad(stones)}</dd>
        </div>
      </dl>

      <div className={`pp-clock${danger ? ' danger' : ''}`}>
        <div className="seg" aria-hidden>
          {Array.from({ length: SEGMENTS }, (_, i) => (
            <i key={i} className={i < lit ? 'on' : ''} />
          ))}
        </div>
        <span className="pp-secs mono">
          {active && secs !== null ? pad(secs) : '--'}
          <small>s</small>
        </span>
      </div>
    </div>
  )
}
