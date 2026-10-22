import { useEffect, useRef, useState } from 'react'
import type { PointerEvent } from 'react'
import { boardFromMoves, findWinLine } from '../lib/winLine'
import type { Point } from '../lib/winLine'
import { Board } from './Board'
import { Roll } from './Motion'

/*
 * A short scripted game for the landing figure: white builds a four on row 6
 * and black blocks it, then black forks with a double three, converts it into
 * an open four and finishes with five on the diagonal.
 */
const MOVES: readonly Point[] = [
  { x: 7, y: 7 },
  { x: 8, y: 6 },
  { x: 7, y: 8 },
  { x: 7, y: 6 },
  { x: 6, y: 8 },
  { x: 9, y: 6 },
  { x: 6, y: 6 },
  { x: 10, y: 6 },
  { x: 11, y: 6 },
  { x: 8, y: 9 },
  { x: 8, y: 8 },
  { x: 9, y: 8 },
  { x: 9, y: 9 },
  { x: 10, y: 10 },
  { x: 5, y: 5 },
]

const STEP_MS = 650
const HOLD_MS = 3200

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

const pad = (n: number) => String(n).padStart(2, '0')

/** Self-playing demo board, framed like a figure in a field manual. */
export function HeroBoard() {
  const [step, setStep] = useState(() => (prefersReducedMotion() ? MOVES.length : 0))
  const figRef = useRef<HTMLElement>(null)

  // Parallax: the board and the yellow block drift against each other with
  // the pointer. Written straight to CSS variables — no re-render per move.
  const onPointerMove = (e: PointerEvent<HTMLElement>) => {
    const el = figRef.current
    if (!el || e.pointerType !== 'mouse') return
    const r = el.getBoundingClientRect()
    el.style.setProperty('--mx', ((e.clientX - r.left) / r.width - 0.5).toFixed(3))
    el.style.setProperty('--my', ((e.clientY - r.top) / r.height - 0.5).toFixed(3))
  }
  const onPointerLeave = () => {
    figRef.current?.style.setProperty('--mx', '0')
    figRef.current?.style.setProperty('--my', '0')
  }

  useEffect(() => {
    if (prefersReducedMotion()) return
    const done = step >= MOVES.length
    const t = setTimeout(() => setStep(done ? 0 : step + 1), done ? HOLD_MS : step === 0 ? 900 : STEP_MS)
    return () => clearTimeout(t)
  }, [step])

  const played = MOVES.slice(0, step)
  const cells = boardFromMoves(15, played)
  const lastMove = played[played.length - 1] ?? null
  const finished = step === MOVES.length
  const winLine = finished ? findWinLine(cells, lastMove) : null

  return (
    <figure className="hero-fig" ref={figRef} onPointerMove={onPointerMove} onPointerLeave={onPointerLeave}>
      <div className="hero-block" />
      <figcaption className="fig-head">
        <span className="mono">FIG.01 — DEMO MATCH</span>
        <span className="mono">
          MOVE{' '}
          <b>
            <Roll value={pad(step)} />
          </b>{' '}
          / {MOVES.length}
        </span>
      </figcaption>
      <div className="hero-board bracketed">
        <Board cells={cells} lastMove={lastMove} winLine={winLine} coords={false} className={finished ? 'finished' : undefined} />
      </div>
      <div className="fig-foot">
        <span className="mono">{finished ? '[ BLACK WINS ] 黑棋五连' : '[ PLAYING ] 对弈中'}</span>
        <span className="mono">15×15 // FREESTYLE</span>
      </div>
    </figure>
  )
}
