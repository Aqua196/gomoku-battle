import { useMetrics } from '../hooks/useMetrics'
import { useStore } from '../state/store'
import { ConnBadge } from './ConnBadge'
import { Logo } from './Logo'
import { SoundToggle, ThemeToggle } from './Toggles'

/**
 * Left navigation rail (top bar on narrow screens): brand block, vertical
 * system label, live server counter, connection state and toggles.
 */
export function Rail({ showStats = true }: { showStats?: boolean }) {
  const { state } = useStore()
  const { session } = state
  const metrics = useMetrics(showStats)

  return (
    <aside className="rail">
      <div className="rail-logo">
        <Logo size={34} />
      </div>
      <div className="rail-title" aria-hidden>
        <span className="en">Gomoku Battle</span>
        <span className="rail-sep" />
        <span className="mono">SYS 15×15</span>
      </div>
      <div className="rail-bottom">
        {metrics && metrics.connections > 0 && (
          <div className="rail-stat" title="实时服务端指标 /metrics">
            <b className="mono">{String(metrics.connections).padStart(2, '0')}</b>
            <span className="en">Online</span>
          </div>
        )}
        {session.status !== 'idle' && <ConnBadge status={session.status} latencyMs={session.latencyMs} />}
        <SoundToggle />
        <ThemeToggle />
      </div>
    </aside>
  )
}
