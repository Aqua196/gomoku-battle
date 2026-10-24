import { blobPath } from '../lib/ink'

/** A value that rolls in from below whenever it changes (odometer feel). */
export function Roll({ value }: { value: string | number }) {
  return (
    <span className="roll">
      <span key={String(value)} className="roll-in">
        {value}
      </span>
    </span>
  )
}

const ENSO = blobPath(23, 40, 0.06)

/** Ensō: a brush circle that keeps drawing itself — used while waiting. */
export function Enso({ size = 56 }: { size?: number }) {
  return (
    <svg className="enso" width={size} height={size} viewBox="-50 -50 100 100" aria-hidden>
      <path d={ENSO} pathLength={1} strokeWidth={5} />
    </svg>
  )
}
