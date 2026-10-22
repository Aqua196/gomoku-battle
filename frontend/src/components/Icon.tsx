const PATHS = {
  bolt: <path d="M13 2 4 14h7l-1 8 9-12h-7l1-8z" />,
  bot: (
    <>
      <rect x="4" y="8" width="16" height="12" rx="3" />
      <path d="M12 4v4M9 13v2M15 13v2M2 14h2M20 14h2" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  refresh: <path d="M20 12a8 8 0 1 1-2.34-5.66L20 8.5M20 3.5v5h-5" />,
  users: (
    <>
      <circle cx="9" cy="8" r="3.5" />
      <path d="M2.5 20v-.5a6.5 6.5 0 0 1 13 0v.5M16 4.6a3.5 3.5 0 0 1 0 6.8M21.5 20v-.5a6.5 6.5 0 0 0-3.5-5.8" />
    </>
  ),
  link: (
    <path d="M10 13.5a4.5 4.5 0 0 0 6.4.4l3-3a4.5 4.5 0 0 0-6.4-6.4l-1.2 1.2M14 10.5a4.5 4.5 0 0 0-6.4-.4l-3 3a4.5 4.5 0 0 0 6.4 6.4l1.2-1.2" />
  ),
  volume: <path d="M11 5 6 9H3v6h3l5 4V5zM15.5 9a4 4 0 0 1 0 6M18.5 6a8 8 0 0 1 0 12" />,
  mute: <path d="M11 5 6 9H3v6h3l5 4V5zM16 9.5l5 5M21 9.5l-5 5" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M4.6 4.6 6 6M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4" />
    </>
  ),
  moon: <path d="M20.5 13.4A8.5 8.5 0 1 1 10.6 3.5a6.6 6.6 0 0 0 9.9 9.9z" />,
  flag: <path d="M5 21V4M5 4h11l-2 4 2 4H5" />,
  arrowLeft: <path d="M19 12H5M11 18l-6-6 6-6" />,
  arrowRight: <path d="M5 12h14M13 6l6 6-6 6" />,
  eye: (
    <>
      <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  hash: <path d="M4 9h16M4 15h16M10 3 8 21M16 3l-2 18" />,
  replay: <path d="M3 4v6h6M4.2 15a8 8 0 1 0 1.9-8.3L3 10" />,
  close: <path d="M18 6 6 18M6 6l12 12" />,
  door: <path d="M14 3h5a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1h-5M9 17l5-5-5-5M14 12H3" />,
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
} as const

export type IconName = keyof typeof PATHS

/** Inline stroke icon (24px grid); decorative unless `label` is given. */
export function Icon({ name, size = 18, label }: { name: IconName; size?: number; label?: string }) {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="square"
      strokeLinejoin="miter"
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {PATHS[name]}
    </svg>
  )
}
