import type { Color, GamePlayer } from '../ws/protocol'
import { TimerRing } from './TimerRing'

/** One seat in the game sidebar: avatar with turn countdown ring, name, state. */
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
  const danger = active && secs !== null && secs <= 10
  return (
    <div className={`player-panel ${color}${active ? ' active' : ''}${online ? '' : ' offline'}${isYou ? ' you' : ''}`}>
      <TimerRing progress={turnLeftMs !== null ? turnLeftMs / turnTotalMs : 0} active={active} danger={danger}>
        <i className={`avatar-stone ${color}`} />
      </TimerRing>
      <div className="pp-main">
        <div className="pp-name">
          <span className="pp-nick">{player.nick}</span>
          {isYou && <em className="tag you">你</em>}
          {player.isAI && <em className="tag ai">AI</em>}
        </div>
        <div className="pp-meta">
          <span className={`pp-presence ${online ? 'on' : 'off'}`}>
            <i />
            {online ? '在线' : '断线'}
          </span>
          <span className="pp-sep" />
          <span>
            执{color === 'black' ? '黑' : '白'} · {stones} 子
          </span>
        </div>
      </div>
      <div className={`pp-clock${danger ? ' danger' : ''}`} aria-hidden={!active}>
        {!online && offlineLeftMs !== null ? (
          <span className="pp-offline">{Math.ceil(offlineLeftMs / 1000)}s</span>
        ) : active && secs !== null ? (
          <>
            <b>{secs}</b>
            <small>s</small>
          </>
        ) : null}
      </div>
    </div>
  )
}
