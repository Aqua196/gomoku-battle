import { useEffect, useState } from 'react'

export interface LiveMetrics {
  connections: number
  activeGames: number
  gamesPlayed: number
}

/**
 * Polls the server's /metrics (PROTOCOL §7) for the lobby's live counters.
 * Purely decorative: any failure just leaves the counters hidden.
 */
export function useMetrics(enabled = true, intervalMs = 5000): LiveMetrics | null {
  const [metrics, setMetrics] = useState<LiveMetrics | null>(null)

  useEffect(() => {
    if (!enabled) return
    let alive = true
    const poll = async () => {
      if (typeof document !== 'undefined' && document.hidden) return
      try {
        const res = await fetch('/metrics')
        if (!res.ok) return
        const m = (await res.json()) as Partial<LiveMetrics>
        if (alive && typeof m.connections === 'number' && typeof m.activeGames === 'number') {
          setMetrics({
            connections: m.connections,
            activeGames: m.activeGames,
            gamesPlayed: m.gamesPlayed ?? 0,
          })
        }
      } catch {
        /* backend unreachable or not same-origin: keep counters hidden */
      }
    }
    void poll()
    const t = setInterval(() => void poll(), intervalMs)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [enabled, intervalMs])

  return enabled ? metrics : null
}
