import { useEffect, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { colLabel, pointLabel, rowLabel } from '../lib/notation'
import type { Point } from '../lib/winLine'
import type { Board as Cells, Color } from '../ws/protocol'

const STAR_POINTS: readonly (readonly [number, number])[] = [
  [3, 3],
  [3, 11],
  [7, 7],
  [11, 3],
  [11, 11],
]

const ARROWS: Record<string, readonly [number, number]> = {
  ArrowLeft: [-1, 0],
  ArrowRight: [1, 0],
  ArrowUp: [0, -1],
  ArrowDown: [0, 1],
}

export interface BoardInteraction {
  /** true while it is this viewer's turn and the socket is up */
  enabled: boolean
  /** color of the ghost stone previewed under the pointer */
  color: Color | null
  onPlay: (x: number, y: number) => void
}

interface BoardProps {
  cells: Cells
  lastMove: Point | null
  winLine?: Point[] | null
  /** move numbers by "x,y"; when given, stones show their move number */
  order?: ReadonlyMap<string, number> | null
  coords?: boolean
  interaction?: BoardInteraction
  className?: string
}

function isCoarsePointer(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: coarse)').matches
}

/**
 * Wooden gomoku board. Grid lines are one SVG (crisp at any size); every
 * intersection is a cell holding its stone. Interactive boards render cells
 * as buttons with roving-tabindex arrow-key navigation, and on touch screens
 * a first tap arms a point and a second tap on it plays (no mis-taps).
 */
export function Board({ cells, lastMove, winLine, order, coords = true, interaction, className }: BoardProps) {
  const n = cells.length
  const gridRef = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<Point>({ x: Math.floor(n / 2), y: Math.floor(n / 2) })
  const [armed, setArmed] = useState<string | null>(null)
  const [hover, setHover] = useState<Point | null>(null)
  const enabled = !!interaction?.enabled
  // Crosshair + coordinate highlight only while a move can be made.
  const aim = enabled ? hover : null

  // Any new stone or a lost turn invalidates an armed touch target.
  useEffect(() => {
    setArmed(null)
  }, [enabled, lastMove?.x, lastMove?.y])

  const winSet = new Set(winLine?.map((p) => `${p.x},${p.y}`))

  const play = (x: number, y: number) => {
    if (!interaction?.enabled || cells[y][x] !== 0) return
    const key = `${x},${y}`
    if (isCoarsePointer() && armed !== key) {
      setArmed(key)
      return
    }
    setArmed(null)
    interaction.onPlay(x, y)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const d = ARROWS[e.key]
    if (!d) return
    e.preventDefault()
    const next = {
      x: Math.min(n - 1, Math.max(0, focus.x + d[0])),
      y: Math.min(n - 1, Math.max(0, focus.y + d[1])),
    }
    setFocus(next)
    gridRef.current?.querySelector<HTMLElement>(`[data-cell="${next.x},${next.y}"]`)?.focus()
  }

  const first = winLine?.[0]
  const last = winLine?.[winLine.length - 1]

  return (
    <div
      className={['board', order ? 'show-order' : '', className ?? ''].filter(Boolean).join(' ')}
      style={{ ['--n' as string]: n }}
    >
      {coords && (
        <>
          <div className="board-coords cols" aria-hidden>
            {cells.map((_, i) => (
              <span key={i} className={aim?.x === i ? 'hot' : undefined}>
                {colLabel(i)}
              </span>
            ))}
          </div>
          <div className="board-coords rows" aria-hidden>
            {cells.map((_, i) => (
              <span key={i} className={aim?.y === i ? 'hot' : undefined}>
                {rowLabel(i, n)}
              </span>
            ))}
          </div>
        </>
      )}

      <div className="board-inner">
        <svg className="board-lines" viewBox={`0 0 ${n} ${n}`} aria-hidden>
          {cells.map((_, i) => (
            <g key={i}>
              <line x1={0.5} y1={i + 0.5} x2={n - 0.5} y2={i + 0.5} />
              <line x1={i + 0.5} y1={0.5} x2={i + 0.5} y2={n - 0.5} />
            </g>
          ))}
          <rect className="board-frame" x={0.5} y={0.5} width={n - 1} height={n - 1} />
          {n === 15 &&
            STAR_POINTS.map(([x, y]) => (
              <rect key={`${x},${y}`} className="board-star" x={x + 0.38} y={y + 0.38} width={0.24} height={0.24} />
            ))}
        </svg>

        {aim && (
          <>
            <span className="xh xh-h" style={{ top: `${((aim.y + 0.5) / n) * 100}%` }} aria-hidden />
            <span className="xh xh-v" style={{ left: `${((aim.x + 0.5) / n) * 100}%` }} aria-hidden />
          </>
        )}

        <div
          ref={gridRef}
          className="board-cells"
          role={interaction ? 'group' : undefined}
          aria-label={interaction ? '棋盘，方向键移动，回车落子' : undefined}
          onKeyDown={interaction ? onKeyDown : undefined}
          onPointerLeave={interaction ? () => setHover(null) : undefined}
        >
          {cells.flatMap((row, y) =>
            row.map((cell, x) => {
              const key = `${x},${y}`
              const isLast = lastMove?.x === x && lastMove?.y === y
              const num = cell !== 0 ? order?.get(key) : undefined
              const playable = enabled && cell === 0
              const stone =
                cell !== 0 ? (
                  <span
                    className={[
                      'stone',
                      cell === 1 ? 'black' : 'white',
                      isLast && 'last',
                      winSet.has(key) && 'win',
                    ]
                      .filter(Boolean)
                      .join(' ')}
                  >
                    {num !== undefined && <span className="stone-num">{num}</span>}
                  </span>
                ) : null

              if (!interaction) {
                return (
                  <div key={key} className="cell">
                    {stone}
                    {isLast && <span className="reticle" />}
                  </div>
                )
              }
              return (
                <button
                  key={key}
                  type="button"
                  data-cell={key}
                  className={['cell', playable && 'playable', armed === key && 'armed'].filter(Boolean).join(' ')}
                  tabIndex={focus.x === x && focus.y === y ? 0 : -1}
                  aria-label={`${pointLabel(x, y, n)} ${cell === 1 ? '黑子' : cell === 2 ? '白子' : '空'}`}
                  aria-disabled={!playable}
                  onFocus={() => setFocus({ x, y })}
                  onPointerEnter={() => setHover({ x, y })}
                  onClick={() => play(x, y)}
                >
                  {stone}
                  {isLast && <span className="reticle" />}
                  {playable && interaction.color && <span className={`stone ghost ${interaction.color}`} />}
                </button>
              )
            }),
          )}
        </div>

        {first && last && (
          <svg className="board-winline" viewBox={`0 0 ${n} ${n}`} aria-hidden>
            {['under', 'over'].map((layer) => (
              <line
                key={layer}
                className={`winline-${layer}`}
                x1={first.x + 0.5}
                y1={first.y + 0.5}
                x2={last.x + 0.5}
                y2={last.y + 0.5}
                pathLength={1}
              />
            ))}
          </svg>
        )}
      </div>
    </div>
  )
}
