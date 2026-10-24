import { useEffect, useRef, useState } from 'react'
import type { PointerEvent } from 'react'
import { boardFromMoves, findWinLine } from '../lib/winLine'
import type { Point } from '../lib/winLine'
import { Board } from './Board'
import { Roll } from './Motion'
import { Seal } from './Seal'

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

/** Self-playing demo board, captioned like a plate in an album. */
export function HeroBoard() {
  const [step, setStep] = useState(() => (prefersReducedMotion() ? MOVES.length : 0))
  const figRef = useRef<HTMLElement>(null)

  // Parallax: the board drifts gently with the pointer, against the landscape
  // behind it. Written straight to CSS variables — no re-render per move.
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
      <div className="hero-board">
        <Board cells={cells} lastMove={lastMove} winLine={winLine} coords={false} className={finished ? 'finished' : undefined} />
      </div>
      <figcaption className="fig-cap">
        <span className="fig-title serif">图一{'\u3000'}对局演示</span>
        <span className="fig-move">
          <span className="latin">move</span>
          <b className="latin">
            <Roll value={pad(step)} />
          </b>
          <span className="latin">/ {MOVES.length}</span>
        </span>
        <span className={`fig-result serif${finished ? ' on' : ''}`}>
          <Seal text="五连" size={30} seed={5} />
          黑棋胜
        </span>
      </figcaption>
    </figure>
  )
}
