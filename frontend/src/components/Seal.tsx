import { useMemo } from 'react'
import { rng } from '../lib/ink'

/** Rough-edged square: the worn border of a carved stone seal. */
function sealOutline(seed: number): string {
  const rand = rng(seed)
  const pts: string[] = []
  const side = (x0: number, y0: number, x1: number, y1: number) => {
    for (let i = 0; i < 6; i++) {
      const t = i / 6
      pts.push(`${(x0 + (x1 - x0) * t + (rand() - 0.5) * 2.2).toFixed(1)},${(y0 + (y1 - y0) * t + (rand() - 0.5) * 2.2).toFixed(1)}`)
    }
  }
  side(4, 4, 96, 4)
  side(96, 4, 96, 96)
  side(96, 96, 4, 96)
  side(4, 96, 4, 4)
  return `M${pts.join('L')}Z`
}

/**
 * Vermilion seal (印章). `solid` = 朱文 filled block with paper-colored
 * characters; otherwise 白文-style outline. One or two characters.
 */
export function Seal({ text, size = 40, solid = true, seed = 1 }: { text: string; size?: number; solid?: boolean; seed?: number }) {
  const outline = useMemo(() => sealOutline(seed), [seed])
  const chars = Array.from(text)
  return (
    <svg className={`seal${solid ? ' solid' : ''}`} width={size} height={size} viewBox="0 0 100 100" aria-hidden>
      <path className="seal-edge" d={outline} />
      {chars.length === 1 ? (
        <text x="50" y="52" className="seal-text" fontSize="62" textAnchor="middle" dominantBaseline="central">
          {chars[0]}
        </text>
      ) : (
        chars.slice(0, 2).map((c, i) => (
          <text key={i} x="50" y={i === 0 ? 31 : 71} className="seal-text" fontSize="38" textAnchor="middle" dominantBaseline="central">
            {c}
          </text>
        ))
      )}
    </svg>
  )
}
