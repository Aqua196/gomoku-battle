import type { ConnectionStatus } from '../ws/client'

const LABEL: Record<ConnectionStatus, string> = {
  idle: '未连接',
  connecting: '连接中…',
  connected: '已连接',
  reconnecting: '重连中…',
  disconnected: '已断开',
}

/** Connection status pill shown in lobby and game headers. */
export function ConnBadge({
  status,
  latencyMs,
}: {
  status: ConnectionStatus
  latencyMs: number | null
}) {
  return (
    <span className={`conn ${status}`} title={latencyMs != null ? `延迟 ${latencyMs} ms` : undefined}>
      <i className="dot" />
      {LABEL[status]}
      {status === 'connected' && latencyMs != null && (
        <span className="latency">{latencyMs}ms</span>
      )}
    </span>
  )
}
