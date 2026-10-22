import { useSyncExternalStore } from 'react'

/** Live match state of a CSS media query (false where matchMedia is missing). */
export function useMedia(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const mq = window.matchMedia?.(query)
      mq?.addEventListener?.('change', onChange)
      return () => mq?.removeEventListener?.('change', onChange)
    },
    () => !!window.matchMedia?.(query).matches,
    () => false,
  )
}
