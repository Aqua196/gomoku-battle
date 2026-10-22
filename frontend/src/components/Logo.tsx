/** Brand mark: two flat stones and a signal-yellow square. */
export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg className="logo" width={size} height={size} viewBox="0 0 40 40" aria-hidden>
      <circle cx="16" cy="17" r="11" fill="currentColor" />
      <circle cx="25" cy="24" r="10" fill="var(--logo-white, #fff)" stroke="currentColor" strokeWidth="2" />
      <rect x="29" y="5" width="7" height="7" fill="#fffa00" />
    </svg>
  )
}
