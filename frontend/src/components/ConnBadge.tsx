import type { ConnectionStatus } from '../ws/client'

const LABEL: Record<ConnectionStatus, string> = {
  idle: '未连接',
  connecting: '连接中…',
  connected: '已连接',
  reconnecting: '重连中…',
  disconnected: '已断开',
}

/** Connection status pill with live round-trip latency. */
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
    <span
      className={`conn ${status} ${quality}`}
      title={latencyMs != null ? `往返延迟 ${latencyMs} ms` : undefined}
    >
      <i className="conn-dot" />
      {status === 'connected' && latencyMs != null ? (
        <span className="conn-latency">{latencyMs}ms</span>
      ) : (
        LABEL[status]
      )}
    </span>
  )
}
