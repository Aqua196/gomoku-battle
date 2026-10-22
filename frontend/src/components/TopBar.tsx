import type { ReactNode } from 'react'
import { useMetrics } from '../hooks/useMetrics'
import { useStore } from '../state/store'
import { ConnBadge } from './ConnBadge'
import { Logo } from './Logo'
import { SoundToggle, ThemeToggle } from './Toggles'

/** App header: brand, live server counters, identity, connection and toggles. */
export function TopBar({ showStats = true, center }: { showStats?: boolean; center?: ReactNode }) {
  const { state } = useStore()
  const { session } = state
  const metrics = useMetrics(showStats)

  return (
    <header className="topbar">
      <div className="brand">
        <Logo size={30} />
        <span className="brand-name">五子棋</span>
        <span className="brand-sub">Gomoku Battle</span>
      </div>

      <div className="topbar-center">
        {center ??
          (metrics && metrics.connections > 0 && (
            <span className="live-stats" title="实时服务端指标 /metrics">
              <i className="live-dot" />
              <b>{metrics.connections}</b> 在线
              <span className="live-sep" />
              <b>{metrics.activeGames}</b> 局对弈中
            </span>
          ))}
      </div>

      <div className="topbar-right">
        {session.playerId && (
          <span className="user-chip" title={session.nick}>
            <i className="avatar-initial" aria-hidden>
              {Array.from(session.nick)[0]?.toUpperCase()}
            </i>
            <span className="user-nick">{session.nick}</span>
          </span>
        )}
        {session.status !== 'idle' && <ConnBadge status={session.status} latencyMs={session.latencyMs} />}
        <SoundToggle />
        <ThemeToggle />
      </div>
    </header>
  )
}
