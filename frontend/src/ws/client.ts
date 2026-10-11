import type { ClientMessage, HelloMsg, ServerMessage } from './protocol'
import { parseServerMessage } from './protocol'

export type ConnectionStatus =
  | 'idle'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'disconnected'

export interface WSClientHandlers {
  /** Supplies the hello payload (nick + stored token) for every (re)connect. */
  getHello: () => HelloMsg
  onMessage: (msg: ServerMessage) => void
  onStatus: (status: ConnectionStatus) => void
  onLatency?: (ms: number) => void
}

const PING_INTERVAL_MS = 10_000

/** Exponential backoff with 50–100% jitter, capped. */
export function computeBackoffMs(attempt: number, baseMs = 1000, capMs = 15_000): number {
  const exp = Math.min(capMs, baseMs * 2 ** Math.max(0, attempt))
  return Math.round(exp * (0.5 + Math.random() * 0.5))
}

export function defaultWsUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss://' : 'ws://'
  return `${proto}${window.location.host}/ws`
}

/**
 * WebSocket client: sends hello on open, dispatches parsed server messages,
 * and reconnects with exponential backoff until close() is called.
 * Backoff resets once a `welcome` is received (session established).
 */
export class WSClient {
  private ws: WebSocket | null = null
  private attempt = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private pingTimer: ReturnType<typeof setInterval> | null = null
  private closed = false
  private pingSentAt = 0
  private readonly url: string
  private readonly handlers: WSClientHandlers

  constructor(url: string, handlers: WSClientHandlers) {
    this.url = url
    this.handlers = handlers
  }

  connect(): void {
    if (this.closed) return
    this.clearReconnectTimer()
    this.handlers.onStatus(this.attempt > 0 ? 'reconnecting' : 'connecting')

    const ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = () => {
      this.send({ ...this.handlers.getHello(), type: 'hello' })
      this.startPing()
    }
    ws.onmessage = (ev: MessageEvent) => {
      let raw: unknown
      try {
        raw = JSON.parse(String(ev.data))
      } catch {
        return // malformed frame: ignore
      }
      const msg = parseServerMessage(raw)
      if (!msg) return
      if (msg.type === 'welcome') this.attempt = 0
      if (msg.type === 'pong') {
        this.handlers.onLatency?.(Date.now() - this.pingSentAt)
        return
      }
      this.handlers.onMessage(msg)
    }
    ws.onclose = () => {
      this.stopPing()
      if (this.ws !== ws) return
      this.ws = null
      if (this.closed) {
        this.handlers.onStatus('disconnected')
        return
      }
      this.handlers.onStatus('reconnecting')
      const delay = computeBackoffMs(this.attempt)
      this.attempt += 1
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    }
    ws.onerror = () => {
      // onclose follows; nothing to do here.
    }
  }

  send(msg: ClientMessage): boolean {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg))
      return true
    }
    return false
  }

  /** Permanently stop the client (no further reconnects). */
  close(): void {
    this.closed = true
    this.clearReconnectTimer()
    this.stopPing()
    if (this.ws) {
      const ws = this.ws
      this.ws = null
      ws.onclose = null
      ws.close()
    }
    this.handlers.onStatus('disconnected')
  }

  private startPing(): void {
    this.stopPing()
    this.pingTimer = setInterval(() => {
      if (this.ws && this.ws.readyState === WebSocket.OPEN) {
        this.pingSentAt = Date.now()
        this.send({ type: 'ping' })
      }
    }, PING_INTERVAL_MS)
  }

  private stopPing(): void {
    if (this.pingTimer !== null) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }
}
