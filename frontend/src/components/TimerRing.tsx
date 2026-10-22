import type { ReactNode } from 'react'

const R = 45
const CIRCUMFERENCE = 2 * Math.PI * R

/** Circular countdown wrapped around an avatar; progress runs 1 → 0. */
export function TimerRing({
  progress,
  active,
  danger,
  children,
}: {
  progress: number
  active: boolean
  danger: boolean
  children: ReactNode
}) {
  const p = Math.min(1, Math.max(0, progress))
  return (
    <div className={`timer-ring${active ? ' active' : ''}${danger ? ' danger' : ''}`}>
      <svg viewBox="0 0 100 100" aria-hidden>
        <circle className="timer-track" cx="50" cy="50" r={R} />
        {active && (
          <circle
            className="timer-fill"
            cx="50"
            cy="50"
            r={R}
            strokeDasharray={CIRCUMFERENCE}
            strokeDashoffset={CIRCUMFERENCE * (1 - p)}
          />
        )}
      </svg>
      <div className="timer-ring-body">{children}</div>
    </div>
  )
}
