import { useMetrics } from '../hooks/useMetrics'
import { useStore } from '../state/useStore'
import { ConnBadge } from './ConnBadge'
import { Roll } from './Motion'
import { Seal } from './Seal'
import { SoundToggle, ThemeToggle } from './Toggles'

/**
 * Left rail (top bar on narrow screens): vermilion seal, the title set
 * vertically like a scroll label, the live player count and the toggles.
 */
export function Rail({ showStats = true }: { showStats?: boolean }) {
  const { state } = useStore()
  const { session } = state
  const metrics = useMetrics(showStats)

  return (
    <aside className="rail">
      <div className="rail-logo">
        <Seal text="弈" size={38} seed={3} />
      </div>
      <div className="rail-title" aria-hidden>
        <span>五子棋</span>
        <i className="rail-dot" />
        <span className="latin">Gomoku</span>
      </div>
      <div className="rail-bottom">
        {metrics && metrics.connections > 0 && (
          <div className="rail-stat" title="实时服务端指标 /metrics">
            <b>
              <Roll value={metrics.connections} />
            </b>
            <span>在线</span>
          </div>
        )}
        {session.status !== 'idle' && <ConnBadge status={session.status} latencyMs={session.latencyMs} />}
        <SoundToggle />
        <ThemeToggle />
      </div>
    </aside>
  )
}
