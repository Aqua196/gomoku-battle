import { memo } from 'react'
import { blobPath, ringPath } from '../lib/ink'
import type { Color } from '../ws/protocol'

/**
 * A stone painted in ink. Black: a wet blot with a feathered halo and a faint
 * sheen. White: paper-white body inside an uneven brush ring. The outline is
 * derived from `seed`, so every point on the board keeps its own shape.
 */
export const InkStone = memo(function InkStone({ color, seed }: { color: Color; seed: number }) {
  return (
    <svg className={`ink-stone ${color}`} viewBox="-50 -50 100 100" aria-hidden>
      {color === 'black' ? (
        <>
          <path className="halo" d={blobPath(seed + 3, 45, 0.07)} />
          <path className="body" d={blobPath(seed, 41, 0.05)} />
          <path className="sheen" d={blobPath(seed + 11, 15, 0.12, -13, -15)} />
        </>
      ) : (
        <>
          <path className="halo" d={blobPath(seed + 3, 44, 0.06, 2, 3)} />
          <path className="body" d={blobPath(seed, 41, 0.04)} />
          <path className="ring" d={ringPath(seed + 5, 42, 5.5)} fillRule="evenodd" />
        </>
      )}
    </svg>
  )
})

/** Vermilion brush circle around a winning stone. */
export const InkCircle = memo(function InkCircle({ seed }: { seed: number }) {
  return (
    <svg className="ink-circle" viewBox="-50 -50 100 100" aria-hidden>
      <path d={ringPath(seed, 49, 4, 0.05)} fillRule="evenodd" />
    </svg>
  )
})
