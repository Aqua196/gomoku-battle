import type { CSSProperties } from 'react'
import type { Color, GameOverReason } from '../ws/protocol'
import { Icon } from './Icon'

const REASON_LABEL: Record<GameOverReason, string> = {
  five: '五连获胜',
  draw: '满盘平局',
  timeout: '回合超时判负',
  resign: '认输',
  disconnect_timeout: '断线超时判负',
  abandoned: '双方断线超时',
}

type Outcome = 'win' | 'lose' | 'draw' | 'abandoned'

const COPY: Record<Outcome, { title: string; seal: string }> = {
  win: { title: '你赢了！', seal: '胜' },
  lose: { title: '对手获胜', seal: '负' },
  draw: { title: '平局', seal: '和' },
  abandoned: { title: '双方弃局', seal: '弃' },
}

// Deterministic burst so renders stay pure: angle / distance / delay per particle.
const PARTICLES = Array.from({ length: 26 }, (_, i) => ({
  a: (i / 26) * 360 + ((i * 37) % 11),
  d: 120 + ((i * 53) % 90),
  delay: (i % 5) * 40,
  gold: i % 3 !== 0,
}))

export function ResultOverlay({
  reason,
  winner,
  youColor,
  moves,
  delayed,
  again,
  onBack,
  onReview,
}: {
  reason: GameOverReason
  winner: Color | null
  youColor: Color | null
  moves: number
  /** hold the card back so the winning line can draw first */
  delayed: boolean
  again: { label: string; run: () => void } | null
  onBack: () => void
  onReview: () => void
}) {
  const outcome: Outcome =
    winner === null ? (reason === 'abandoned' ? 'abandoned' : 'draw') : winner === youColor ? 'win' : 'lose'
  const copy = COPY[outcome]

  return (
    <div className={`overlay result ${outcome}${delayed ? ' delayed' : ''}`} role="dialog" aria-modal="true" aria-labelledby="result-title">
      <div className="result-card">
        {outcome === 'win' && (
          <div className="burst" aria-hidden>
            {PARTICLES.map((p, i) => (
              <i
                key={i}
                className={p.gold ? 'gold' : 'seal'}
                style={{ '--a': `${p.a}deg`, '--d': `${p.d}px`, '--delay': `${p.delay}ms` } as CSSProperties}
              />
            ))}
          </div>
        )}
        <div className="seal-stamp" aria-hidden>
          <span>{copy.seal}</span>
        </div>
        <h2 id="result-title" className="result-title">
          {copy.title}
        </h2>
        <p className="result-reason">{REASON_LABEL[reason]}</p>
        <div className="result-stats">
          <span>
            <b>{moves}</b> 手
          </span>
          <span className="pp-sep" />
          <span>执{youColor === 'white' ? '白' : '黑'}</span>
        </div>
        <div className="result-actions">
          {again && (
            <button type="button" className="btn primary lg" onClick={again.run}>
              <Icon name="replay" />
              {again.label}
            </button>
          )}
          <button type="button" className={`btn lg ${again ? 'ghost' : 'primary'}`} onClick={onBack} autoFocus={!again}>
            返回大厅
          </button>
        </div>
        <button type="button" className="link-btn" onClick={onReview}>
          <Icon name="eye" size={16} />
          查看终局棋盘
        </button>
      </div>
    </div>
  )
}
