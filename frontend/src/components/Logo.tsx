import { useId } from 'react'

/** Brand mark: a black and a white stone overlapping, with a vermilion seal dot. */
export function Logo({ size = 32 }: { size?: number }) {
  const id = useId()
  return (
    <svg className="logo" width={size} height={size} viewBox="0 0 40 40" aria-hidden>
      <defs>
        <radialGradient id={`${id}b`} cx="35%" cy="30%" r="75%">
          <stop offset="0" stopColor="#6d6d72" />
          <stop offset=".35" stopColor="#1d1d21" />
          <stop offset="1" stopColor="#000" />
        </radialGradient>
        <radialGradient id={`${id}w`} cx="35%" cy="30%" r="75%">
          <stop offset="0" stopColor="#fff" />
          <stop offset=".6" stopColor="#e9e5dc" />
          <stop offset="1" stopColor="#bdb6a8" />
        </radialGradient>
      </defs>
      <circle cx="25" cy="24" r="12" fill={`url(#${id}w)`} />
      <circle cx="15" cy="16" r="12" fill={`url(#${id}b)`} stroke="rgba(255,255,255,.28)" strokeWidth=".8" />
      <circle cx="29.5" cy="9.5" r="3.4" fill="var(--seal)" />
    </svg>
  )
}
