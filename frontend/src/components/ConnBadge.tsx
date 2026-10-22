import type { ConnectionStatus } from '../ws/client'

const LABEL: Record<ConnectionStatus, string> = {
  idle: '未连接',
  connecting: '连接中',
  connected: '已连接',
  reconnecting: '重连中',
  disconnected: '已断开',
}

/** Connection state: a status square plus round-trip latency once known. */
export function ConnBadge({
  status,
  latencyMs,
}: {
  status: ConnectionStatus
  latencyMs: number | null
}) {
  const quality =
    status !== 'connected' || latencyMs == null ? '' : latencyMs < 80 ? 'good' : latencyMs < 200 ? 'fair' : 'poor'
  return (
    <span className={`conn ${status} ${quality}`} title={latencyMs != null ? `往返延迟 ${latencyMs} ms` : LABEL[status]}>
      <i className="conn-dot" />
      {status === 'connected' && latencyMs != null ? (
        <span className="mono">{latencyMs}ms</span>
      ) : (
        <span>{LABEL[status]}</span>
      )}
    </span>
  )
}
