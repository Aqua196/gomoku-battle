import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Board } from '../components/Board'
import { Icon } from '../components/Icon'
import { InkScape } from '../components/InkScape'
import { InkStone } from '../components/InkStone'
import { Enso, Roll } from '../components/Motion'
import { Rail } from '../components/Rail'
import { Seal } from '../components/Seal'
import { orderOf } from '../hooks/useMoveOrder'
import { usePrefs } from '../hooks/usePrefs'
import { pointSeed } from '../lib/ink'
import { pointLabel } from '../lib/notation'
import { setPref } from '../lib/prefs'
import { RecordError, duration, fetchGame, shortDate, verdict } from '../lib/records'
import { sfx } from '../lib/sound'
import { boardFromMoves, findWinLine } from '../lib/winLine'
import { BOARD_SIZE } from '../ws/protocol'
import type { Color, GameRecord, RecordSeat } from '../ws/protocol'

/** Autoplay pace: one stone per beat. */
const BEAT_MS = 700

type Load = { state: 'loading' } | { state: 'missing' } | { state: 'error' } | { state: 'ready'; rec: GameRecord }

/** /replay/:id — a recorded game, stepped through stone by stone. */
export function Replay() {
  const { id = '' } = useParams()
  const [load, setLoad] = useState<Load>({ state: 'loading' })

  useEffect(() => {
    const ctl = new AbortController()
    let retried = false
    const run = () =>
      fetchGame(id, ctl.signal)
        .then((rec) => setLoad({ state: 'ready', rec }))
        .catch((err: unknown) => {
          if (ctl.signal.aborted) return
          // The record lands a moment after game_over: retry a 404 once.
          if (err instanceof RecordError && err.status === 404 && !retried) {
            retried = true
            setTimeout(run, 600)
            return
          }
          setLoad({ state: err instanceof RecordError && err.status === 404 ? 'missing' : 'error' })
        })
    setLoad({ state: 'loading' })
    run()
    return () => ctl.abort()
  }, [id])

  return (
    <div className="app-shell">
      <Rail showStats={false} />
      <main className="stage replay">
        <InkScape className="game-scape" seed={47} sun={null} lift={0.1} />
        {load.state === 'ready' ? (
          <ReplayView key={load.rec.id} rec={load.rec} />
        ) : (
          <ReplayStatus state={load.state} />
        )}
      </main>
    </div>
  )
}

function ReplayStatus({ state }: { state: 'loading' | 'missing' | 'error' }) {
  const navigate = useNavigate()
  return (
    <section className="replay-status">
      <Enso size={72} />
      <p className="serif">
        {state === 'loading' ? '展卷中…' : state === 'missing' ? '查无此局' : '棋谱暂时取不到'}
      </p>
      {state !== 'loading' && (
        <span>{state === 'missing' ? '棋谱只保留最近的对局，或链接有误。' : '请稍后再试。'}</span>
      )}
      {state !== 'loading' && (
        <button className="btn" onClick={() => navigate('/')}>
          <span>返回大厅</span>
        </button>
      )}
    </section>
  )
}

function ReplayView({ rec }: { rec: GameRecord }) {
  const navigate = useNavigate()
  const prefs = usePrefs()
  const moves = useMemo(() => rec.moves ?? [], [rec.moves])
  const total = moves.length
  const [step, setStep] = useState(total)
  const [playing, setPlaying] = useState(false)
  const [copied, setCopied] = useState(false)
  const listRef = useRef<HTMLOListElement>(null)

  const cells = useMemo(() => boardFromMoves(BOARD_SIZE, moves.slice(0, step)), [moves, step])
  const lastMove = step > 0 ? moves[step - 1] : null
  const atEnd = step === total
  const winLine = atEnd && rec.reason === 'five' ? findWinLine(cells, lastMove) : null
  const order = useMemo(() => (prefs.showOrder ? orderOf(moves, step) : null), [prefs.showOrder, moves, step])

  const go = useCallback(
    (n: number) => {
      const next = Math.max(0, Math.min(total, n))
      if (next === step + 1) sfx.stone()
      setStep(next)
    },
    [step, total],
  )

  // Autoplay: one stone per beat until the end.
  useEffect(() => {
    if (!playing) return
    if (step >= total) {
      setPlaying(false)
      return
    }
    const t = setTimeout(() => go(step + 1), BEAT_MS)
    return () => clearTimeout(t)
  }, [playing, step, total, go])

  const togglePlay = useCallback(() => {
    if (playing) {
      setPlaying(false)
      return
    }
    if (atEnd) setStep(0) // replay from the empty board
    setPlaying(true)
  }, [playing, atEnd])

  // Keep the current move visible by scrolling the list itself (never the
  // page). The list is position: relative, so offsetTop is list-relative.
  useEffect(() => {
    const list = listRef.current
    const el = list?.querySelector<HTMLElement>('[aria-current="step"]')
    if (!list || !el) return
    const top = el.offsetTop
    if (top < list.scrollTop || top + el.offsetHeight > list.scrollTop + list.clientHeight) {
      list.scrollTop = top - list.clientHeight / 2
    }
  }, [step])

  // Keys work wherever focus is, except inside the scrubber (which has
  // its own arrow handling).
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      const keys: Record<string, () => void> = {
        ArrowLeft: () => go(step - 1),
        ArrowRight: () => go(step + 1),
        Home: () => go(0),
        End: () => go(total),
      }
      const target = e.target as HTMLElement | null
      if (target?.tagName === 'INPUT' || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === ' ') {
        e.preventDefault()
        togglePlay()
        return
      }
      const run = keys[e.key]
      if (!run) return
      e.preventDefault()
      setPlaying(false)
      run()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [go, step, total, togglePlay])

  const share = () => {
    void navigator.clipboard?.writeText(window.location.href).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    })
  }

  const toMove: Color = step % 2 === 0 ? 'black' : 'white'

  return (
    <div className="replay-layout">
      <section className="board-stage">
        <div className="board-wrap">
          <Board cells={cells} lastMove={lastMove} winLine={winLine} order={order} className="scan" />
        </div>
        <div className="replay-transport" role="group" aria-label="复盘控制">
          <button className="icon-btn" aria-label="第一手前" onClick={() => go(0)} disabled={step === 0}>
            <Icon name="first" />
          </button>
          <button className="icon-btn" aria-label="上一手" onClick={() => go(step - 1)} disabled={step === 0}>
            <Icon name="prev" />
          </button>
          <button className="icon-btn play" aria-label={playing ? '暂停' : '自动播放'} onClick={togglePlay}>
            <Icon name={playing ? 'pause' : 'play'} />
          </button>
          <button className="icon-btn" aria-label="下一手" onClick={() => go(step + 1)} disabled={atEnd}>
            <Icon name="next" />
          </button>
          <button className="icon-btn" aria-label="终局" onClick={() => go(total)} disabled={atEnd}>
            <Icon name="last" />
          </button>
          <input
            className="replay-scrub"
            type="range"
            min={0}
            max={total}
            value={step}
            aria-label="手数"
            onChange={(e) => {
              setPlaying(false)
              go(Number(e.target.value))
            }}
            style={{ ['--p' as string]: `${total ? (step / total) * 100 : 0}%` }}
          />
          <span className="replay-count latin">
            <b>
              <Roll value={step} />
            </b>{' '}
            / {total}
          </span>
        </div>
      </section>

      <aside className="game-side replay-side">
        <header className="side-head reveal">
          <div>
            <p className="kicker">
              <span>No.04</span>
              <span className="latin">the record</span>
            </p>
            <h1 className="serif">复盘</h1>
          </div>
          <Seal text={rec.winner === 'white' ? '白胜' : rec.winner === 'black' ? '黑胜' : '和'} size={44} seed={21} />
        </header>

        <div className="replay-seats">
          <SeatLine color="black" seat={rec.black} won={rec.winner === 'black'} />
          <SeatLine color="white" seat={rec.white} won={rec.winner === 'white'} />
        </div>

        <dl className="spec">
          <div>
            <dt>结果</dt>
            <dd className="serif">{verdict(rec)}</dd>
          </div>
          <div>
            <dt>手数</dt>
            <dd className="latin">{total}</dd>
          </div>
          <div>
            <dt>时间</dt>
            <dd className="latin">
              {shortDate(rec.startedAt)} · {duration(rec.endedAt - rec.startedAt)}
            </dd>
          </div>
        </dl>

        <p className="replay-now serif" aria-live="polite">
          {lastMove ? `第 ${step} 手 · ${step % 2 === 1 ? '黑' : '白'} ${pointLabel(lastMove.x, lastMove.y)}` : '空枰'}
          {!atEnd && <span className="latin"> — {toMove} to move</span>}
        </p>

        <ol className="move-list" ref={listRef} aria-label="着法">
          {moves.map((m, i) => (
            <li key={i}>
              <button
                type="button"
                aria-label={`第 ${i + 1} 手 ${pointLabel(m.x, m.y)}`}
                className={i % 2 === 0 ? 'b' : 'w'}
                aria-current={i + 1 === step ? 'step' : undefined}
                onClick={() => {
                  setPlaying(false)
                  go(i + 1)
                }}
              >
                <span className="latin">{i + 1}</span>
                {pointLabel(m.x, m.y)}
              </button>
            </li>
          ))}
        </ol>

        <div className="side-actions">
          <button
            type="button"
            className={`btn sm toggle${prefs.showOrder ? ' on' : ''}`}
            aria-pressed={prefs.showOrder}
            onClick={() => setPref('showOrder', !prefs.showOrder)}
          >
            <Icon name="hash" size={15} />
            <span>手数</span>
          </button>
          <button type="button" className="btn sm" onClick={share}>
            <Icon name="link" size={15} />
            <span>{copied ? '已复制' : '分享链接'}</span>
          </button>
          <button type="button" className="btn sm primary" onClick={() => navigate('/')}>
            <span>返回大厅</span>
          </button>
        </div>
        <p className="side-hint serif">← → 逐手 · 空格播放 · Home / End 首尾</p>
      </aside>
    </div>
  )
}

function SeatLine({ color, seat, won }: { color: Color; seat: RecordSeat; won: boolean }) {
  return (
    <div className={`replay-seat${won ? ' won' : ''}`}>
      <span className="chip">
        <InkStone color={color} seed={pointSeed(color === 'black' ? 3 : 4, 9, 2)} />
      </span>
      <span className="replay-nick">{seat.nick}</span>
      {seat.isAI && <em className="tag ai">AI</em>}
      {won && <em className="tag you">胜</em>}
    </div>
  )
}
