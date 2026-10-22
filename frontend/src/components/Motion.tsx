import type { ReactNode } from 'react'

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

/** Endless horizontal ticker; children are rendered twice for a seamless loop. */
export function Marquee({ children, speed = 40 }: { children: ReactNode; speed?: number }) {
  return (
    <div className="marquee" aria-hidden style={{ ['--marquee-speed' as string]: `${speed}s` }}>
      <div className="marquee-track">
        <div className="marquee-run">{children}</div>
        <div className="marquee-run">{children}</div>
      </div>
    </div>
  )
}
