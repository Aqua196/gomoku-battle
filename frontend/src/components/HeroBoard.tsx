import { useEffect, useState } from 'react'
import { boardFromMoves, findWinLine } from '../lib/winLine'
import type { Point } from '../lib/winLine'
import { Board } from './Board'

/*
 * A short scripted game for the landing visual: white builds a four on row 6
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

/** Self-playing 3D board on the landing page. */
export function HeroBoard() {
  const [step, setStep] = useState(() => (prefersReducedMotion() ? MOVES.length : 0))

  useEffect(() => {
    if (prefersReducedMotion()) return
    const done = step >= MOVES.length
    const t = setTimeout(() => setStep(done ? 0 : step + 1), done ? HOLD_MS : step === 0 ? 900 : STEP_MS)
    return () => clearTimeout(t)
  }, [step])

  const played = MOVES.slice(0, step)
  const cells = boardFromMoves(15, played)
  const lastMove = played[played.length - 1] ?? null
  const winLine = step === MOVES.length ? findWinLine(cells, lastMove) : null

  return (
    <div className="hero-stage">
      <div className="hero-glow" />
      <div className={`hero-tilt${step === MOVES.length ? ' finished' : ''}`}>
        <Board cells={cells} lastMove={lastMove} winLine={winLine} coords={false} className="hero-board" />
      </div>
      <div className="hero-caption">
        <span className="hero-caption-dot" />
        {step === MOVES.length ? '黑棋五连 · 胜' : `第 ${step} 手`}
      </div>
    </div>
  )
}
