import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react'
import type { ReactNode } from 'react'
import { WSClient, defaultWsUrl } from '../ws/client'
import type { ServerMessage } from '../ws/protocol'
import { createResumeWatchdog } from './clock'
import { initialState, reducer } from './reducer'
import { StoreContext } from './useStore'
import type { StoreApi } from './useStore'

const LS_NICK = 'gomoku.nick'
const LS_TOKEN = 'gomoku.token'

/* ── Provider ────────────────────────────────────────────────────── */

export function StoreProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState)
  const clientRef = useRef<WSClient | null>(null)
  const resumeWatchdogRef = useRef<ReturnType<typeof createResumeWatchdog> | null>(null)
  resumeWatchdogRef.current ??= createResumeWatchdog(() =>
    dispatch({ type: 'CLEAR_GAME' }),
  )
  const nickRef = useRef(state.session.nick)
  nickRef.current = state.session.nick

  const connect = useCallback((nick?: string) => {
    const n = (nick ?? nickRef.current).trim().slice(0, 32)
    if (!n) return
    dispatch({ type: 'NICK', nick: n })
    localStorage.setItem(LS_NICK, n)
    clientRef.current?.close()
    const client = new WSClient(defaultWsUrl(), {
      getHello: () => {
        const token = localStorage.getItem(LS_TOKEN)
        return { type: 'hello', nick: n, ...(token ? { token } : {}) }
      },
      onStatus: (s) => dispatch({ type: 'STATUS', status: s }),
      onMessage: (m) => {
        if (m.type === 'welcome') {
          // Persist the reconnection token + effective nick (§1, §9).
          localStorage.setItem(LS_TOKEN, m.token)
          localStorage.setItem(LS_NICK, m.nick)
          // §1: game_state follows only when the resumed session had a live
          // game — arm the watchdog to clear any stale board otherwise.
          if (m.resumed) resumeWatchdogRef.current?.arm()
          else resumeWatchdogRef.current?.cancel()
        }
        if (m.type === 'game_state' || m.type === 'game_over') {
          resumeWatchdogRef.current?.cancel()
        }
        dispatch({ type: 'SERVER', msg: m })
      },
      onLatency: (ms) => dispatch({ type: 'LATENCY', ms }),
    })
    clientRef.current = client
    client.connect()
  }, [])

  // Auto-connect on entry when a nick was stored (§9). Cleanup closes the client.
  useEffect(() => {
    const stored = localStorage.getItem(LS_NICK)
    if (stored) connect(stored)
    return () => {
      clientRef.current?.close()
      resumeWatchdogRef.current?.cancel()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Dev-only: let manual tests inject a server message without a backend
  // (e.g. `__gomoku({type:'game_state', game})` from the console).
  useEffect(() => {
    if (!import.meta.env.DEV) return
    const w = window as unknown as { __gomoku?: (m: ServerMessage) => void }
    w.__gomoku = (m) => dispatch({ type: 'SERVER', msg: m })
    return () => {
      delete w.__gomoku
    }
  }, [])

  const api = useMemo<StoreApi>(() => {
    const send = (msg: Parameters<WSClient['send']>[0]) => {
      const ok = clientRef.current?.send(msg) ?? false
      if (!ok) {
        // Dropped while the socket is down — tell the user instead of
        // silently swallowing the action (R14).
        dispatch({
          type: 'LOCAL_ERROR',
          code: 'offline',
          message: '连接已断开，正在重连，请稍候再试',
        })
      }
      return ok
    }
    return {
      connect,
      createRoom: (name) => send({ type: 'create_room', ...(name ? { name } : {}) }),
      joinRoom: (roomId) => send({ type: 'join_room', roomId }),
      leaveRoom: () => send({ type: 'leave_room' }),
      listRooms: () => send({ type: 'list_rooms' }),
      matchmake: () => send({ type: 'matchmake' }),
      matchmakeCancel: () => send({ type: 'matchmake_cancel' }),
      playAi: (difficulty, color) =>
        send({ type: 'play_ai', difficulty, ...(color && color !== 'black' ? { color } : {}) }),
      move: (x, y) => send({ type: 'move', x, y }),
      resign: () => send({ type: 'resign' }),
      dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
      clearGame: () => dispatch({ type: 'CLEAR_GAME' }),
    }
  }, [connect])

  const value = useMemo(() => ({ state, api }), [state, api])
  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>
}
