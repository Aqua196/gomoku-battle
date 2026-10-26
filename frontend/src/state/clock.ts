/**
 * Server-clock alignment + the resumed-session grace watchdog.
 * Kept out of store.tsx so the component file only exports components
 * (react-refresh/only-export-components).
 */

/** §1: after welcome(resumed=true), game_state only arrives if a live game
    exists — the provider clears the stale view once this window elapses. */
export const RESUME_GRACE_MS = 1500

/** §1 v1.1: welcome named our live game — its game_state may take a node
    failover (lease lapse + adoption, ~7s) to arrive. */
export const ADOPT_GRACE_MS = 15_000

/** EMA estimate of the client→server clock skew. */
export function smoothClockOffset(prev: number | null, sample: number): number {
  return prev === null ? sample : prev + 0.3 * (sample - prev)
}

/** One-shot timer pair used for the resumed-session game_state grace window. */
export function createResumeWatchdog(onExpire: () => void, ms = RESUME_GRACE_MS) {
  let t: ReturnType<typeof setTimeout> | null = null
  return {
    arm(after = ms) {
      this.cancel()
      t = setTimeout(onExpire, after)
    },
    cancel() {
      if (t !== null) {
        clearTimeout(t)
        t = null
      }
    },
  }
}
