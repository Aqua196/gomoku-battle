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

const REASON_CODE: Record<GameOverReason, string> = {
  five: 'FIVE IN A ROW',
  draw: 'BOARD FULL',
  timeout: 'TURN TIMEOUT',
  resign: 'RESIGNATION',
  disconnect_timeout: 'DISCONNECT TIMEOUT',
  abandoned: 'BOTH DISCONNECTED',
}

type Outcome = 'win' | 'lose' | 'draw' | 'abandoned'

const COPY: Record<Outcome, { title: string; seal: string; word: string }> = {
  win: { title: '你赢了！', seal: '胜', word: 'Victory' },
  lose: { title: '对手获胜', seal: '负', word: 'Defeat' },
  draw: { title: '平局', seal: '和', word: 'Draw' },
  abandoned: { title: '双方弃局', seal: '弃', word: 'Void' },
}

/** End-of-game verdict: a solid slab docked beside the board. */
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
  /** hold the verdict back so the winning line can draw first */
  delayed: boolean
  again: { label: string; run: () => void } | null
  onBack: () => void
  onReview: () => void
}) {
  const outcome: Outcome =
    winner === null ? (reason === 'abandoned' ? 'abandoned' : 'draw') : winner === youColor ? 'win' : 'lose'
  const copy = COPY[outcome]

  return (
    <div
      className={`overlay result ${outcome}${delayed ? ' delayed' : ''}`}
      role="dialog"
      aria-modal="true"
      aria-labelledby="result-title"
    >
      <div className="slab">
        <div className="slab-word en" aria-hidden>
          {copy.word}
        </div>
        <div className="slab-head">
          <span className="slab-seal" aria-hidden>
            {copy.seal}
          </span>
          <div>
            <p className="kicker">RESULT // {REASON_CODE[reason]}</p>
            <h2 id="result-title" className="slab-title">
              {copy.title}
            </h2>
          </div>
        </div>
        <dl className="slab-spec">
          <div>
            <dt>结果</dt>
            <dd>{REASON_LABEL[reason]}</dd>
          </div>
          <div>
            <dt>手数</dt>
            <dd className="mono">{String(moves).padStart(3, '0')}</dd>
          </div>
          <div>
            <dt>执子</dt>
            <dd>{youColor === 'white' ? '白' : '黑'}</dd>
          </div>
        </dl>
        <div className="slab-actions">
          {again && (
            <button type="button" className="btn lg block primary" onClick={again.run}>
              <span>
                <Icon name="replay" size={16} />
                {again.label}
              </span>
              <small className="en" aria-hidden>
                Again
              </small>
            </button>
          )}
          <button type="button" className={`btn lg block${again ? '' : ' primary'}`} onClick={onBack} autoFocus={!again}>
            <span>返回大厅</span>
            <small className="en" aria-hidden>
              Lobby
            </small>
          </button>
        </div>
        <button type="button" className="link-btn" onClick={onReview}>
          <Icon name="eye" size={15} />
          查看终局棋盘
        </button>
      </div>
    </div>
  )
}
