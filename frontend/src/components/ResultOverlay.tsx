import type { Color, GameOverReason } from '../ws/protocol'
import { Icon } from './Icon'
import { Seal } from './Seal'

const REASON_LABEL: Record<GameOverReason, string> = {
  five: '五连获胜',
  draw: '满盘平局',
  timeout: '回合超时判负',
  resign: '认输',
  disconnect_timeout: '断线超时判负',
  abandoned: '双方断线超时',
}

type Outcome = 'win' | 'lose' | 'draw' | 'abandoned'

const COPY: Record<Outcome, { title: string; glyph: string; seal: string; latin: string }> = {
  win: { title: '你赢了！', glyph: '胜', seal: '大吉', latin: 'a game well won' },
  lose: { title: '对手获胜', glyph: '负', seal: '再战', latin: 'the stones fell against you' },
  draw: { title: '平局', glyph: '和', seal: '和局', latin: 'neither side yields' },
  abandoned: { title: '双方弃局', glyph: '弃', seal: '空局', latin: 'the table was left empty' },
}

/** End-of-game verdict: a hanging scroll that unrolls from its centre. */
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
  /** hold the scroll back so the winning stroke can draw first */
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
      <div className="scroll">
        <i className="rod top" aria-hidden />
        <div className="scroll-paper">
          <div className="scroll-glyph serif" aria-hidden>
            {copy.glyph}
          </div>
          <div className="scroll-seal">
            <Seal text={copy.seal} size={46} seed={13} solid={outcome === 'win'} />
          </div>
          <h2 id="result-title" className="scroll-title serif">
            {copy.title}
          </h2>
          <p className="scroll-latin latin">{copy.latin}</p>
          <dl className="slab-spec">
            <div>
              <dt>结果</dt>
              <dd>{REASON_LABEL[reason]}</dd>
            </div>
            <div>
              <dt>手数</dt>
              <dd className="latin">{moves}</dd>
            </div>
            <div>
              <dt>执子</dt>
              <dd className="serif">{youColor === 'white' ? '白' : '黑'}</dd>
            </div>
          </dl>
          <div className="scroll-actions">
            {again && (
              <button type="button" className="btn block primary" onClick={again.run}>
                <span>
                  <Icon name="replay" size={16} />
                  {again.label}
                </span>
                <small aria-hidden>again</small>
              </button>
            )}
            <button type="button" className="btn block" onClick={onBack} autoFocus={!again}>
              <span>返回大厅</span>
              <small aria-hidden>lobby</small>
            </button>
          </div>
          <button type="button" className="link-btn" onClick={onReview}>
            <Icon name="eye" size={15} />
            查看终局棋盘
          </button>
        </div>
        <i className="rod bottom" aria-hidden />
      </div>
    </div>
  )
}
