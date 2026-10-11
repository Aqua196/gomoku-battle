import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
} from 'react'
import type { ReactNode } from 'react'
import { WSClient, defaultWsUrl } from '../ws/client'
import { createResumeWatchdog, smoothClockOffset } from './clock'
import type { ConnectionStatus } from '../ws/client'
import type {
  Color,
  Difficulty,
  GameOverReason,
  GameState,
  Room,
  ServerMessage,
} from '../ws/protocol'

const LS_NICK = 'gomoku.nick'
const LS_TOKEN = 'gomoku.token'

/* ── State ───────────────────────────────────────────────────────── */

export interface SessionState {
  status: ConnectionStatus
  /** server-assigned id once welcomed */
  playerId: string | null
  /** effective nick (from welcome); pre-login it holds the stored/input nick */
  nick: string
  resumed: boolean
  latencyMs: number | null
  /** EMA of (serverNow − Date.now()); aligns deadline countdowns to server time */
  clockOffsetMs: number | null
}

export interface LobbyState {
  rooms: Room[]
  /** waiting room I'm seated in (cleared on game_start / left) */
  myRoomId: string | null
  matchmaking: boolean
  queuePosition: number | null
}

export interface GameViewState {
  game: GameState | null
  youColor: Color | null
  opponentConnected: boolean
  /** forfeit deadline while opponent is disconnected */
  opponentDeadline: number | null
  /** timestamp of last opponent reconnect (transient banner) */
  opponentReconnectedAt: number | null
  over: { reason: GameOverReason; winner: Color | null } | null
}

export interface AppState {
  session: SessionState
  lobby: LobbyState
  game: GameViewState
  error: { code: string; message: string; ts: number } | null
}

export const initialState: AppState = {
  session: {
    status: 'idle',
    playerId: null,
    nick: '',
    resumed: false,
    latencyMs: null,
    clockOffsetMs: null,
  },
  lobby: { rooms: [], myRoomId: null, matchmaking: false, queuePosition: null },
  game: {
    game: null,
    youColor: null,
    opponentConnected: true,
    opponentDeadline: null,
    opponentReconnectedAt: null,
    over: null,
  },
  error: null,
}

/* ── Reducer ─────────────────────────────────────────────────────── */

export type Action =
  | { type: 'STATUS'; status: ConnectionStatus }
  | { type: 'NICK'; nick: string }
  | { type: 'LATENCY'; ms: number }
  | { type: 'SERVER'; msg: ServerMessage }
  | { type: 'LOCAL_ERROR'; code: string; message: string }
  | { type: 'DISMISS_ERROR' }
  | { type: 'CLEAR_GAME' }

/** Own color derived from playerId — also covers resume via bare game_state. */
export function deriveYouColor(game: GameState, playerId: string | null): Color | null {
  if (!playerId) return null
  if (game.players.black.playerId === playerId) return 'black'
  if (game.players.white.playerId === playerId) return 'white'
  return null
}

export function reducer(state: AppState, action: Action): AppState {
  switch (action.type) {
    case 'STATUS':
      return { ...state, session: { ...state.session, status: action.status } }
    case 'NICK':
      return { ...state, session: { ...state.session, nick: action.nick } }
    case 'LATENCY':
      return { ...state, session: { ...state.session, latencyMs: action.ms } }
    case 'DISMISS_ERROR':
      return { ...state, error: null }
    case 'LOCAL_ERROR':
      return {
        ...state,
        error: { code: action.code, message: action.message, ts: Date.now() },
      }
    case 'CLEAR_GAME':
      return { ...state, game: { ...initialState.game } }
    case 'SERVER':
      return reduceServer(state, action.msg)
  }
}

function applyServerNow(session: SessionState, serverNow: unknown): SessionState {
  if (typeof serverNow !== 'number') return session
  return {
    ...session,
    clockOffsetMs: smoothClockOffset(session.clockOffsetMs, serverNow - Date.now()),
  }
}


function reduceServer(state: AppState, msg: ServerMessage): AppState {
  switch (msg.type) {
    case 'welcome': {
      const session = applyServerNow(
        {
          ...state.session,
          status: 'connected',
          playerId: msg.playerId,
          nick: msg.nick,
          resumed: msg.resumed,
        },
        msg.serverNow,
      )
      if (msg.resumed) {
        // Takeover / disconnected-session resume: keep views — a game_state
        // follows when a live game exists; the provider watchdog clears the
        // stale board if none arrives within the grace window (§1).
        return { ...state, session }
      }
      // Fresh session (invalid/expired token, server restart): every previous
      // view is stale — drop lobby seat and any ghost board so the user lands
      // back in the lobby (R1).
      return {
        ...state,
        session,
        lobby: { ...initialState.lobby },
        game: { ...initialState.game },
      }
    }
    case 'room_list':
      return { ...state, lobby: { ...state.lobby, rooms: msg.rooms } }
    case 'room':
      if (msg.action === 'left') {
        const lobby =
          state.lobby.myRoomId === msg.roomId
            ? { ...state.lobby, myRoomId: null }
            : state.lobby
        return { ...state, lobby }
      }
      return { ...state, lobby: { ...state.lobby, myRoomId: msg.room.id } }
    case 'matchmaking':
      return {
        ...state,
        lobby: {
          ...state.lobby,
          matchmaking: msg.status === 'queued',
          queuePosition: msg.status === 'queued' ? (msg.position ?? null) : null,
        },
      }
    case 'game_start': {
      const opp = opponentOf(msg.game, msg.youColor)
      return {
        ...state,
        session: applyServerNow(state.session, msg.game.serverNow),
        lobby: { ...state.lobby, myRoomId: null, matchmaking: false, queuePosition: null },
        game: {
          ...initialState.game,
          game: msg.game,
          youColor: msg.youColor,
          opponentConnected: opp?.connected ?? true,
          opponentDeadline: opp && !opp.connected ? msg.game.reconnectDeadline : null,
        },
      }
    }
    case 'game_state': {
      // game_state is the authoritative snapshot: re-sync the presence view
      // from it so a missed opponent_presence (e.g. after a double-disconnect
      // resume) still self-heals (R1/R2 frontend half).
      const youColor =
        state.game.youColor ?? deriveYouColor(msg.game, state.session.playerId)
      const opp = youColor ? opponentOf(msg.game, youColor) : null
      return {
        ...state,
        session: applyServerNow(state.session, msg.game.serverNow),
        game: {
          ...state.game,
          game: msg.game,
          youColor,
          opponentConnected: opp ? opp.connected : state.game.opponentConnected,
          opponentDeadline: msg.game.reconnectDeadline,
          opponentReconnectedAt:
            opp?.connected && !state.game.opponentConnected
              ? Date.now()
              : state.game.opponentReconnectedAt,
          over:
            msg.game.status === 'finished' && state.game.over === null && msg.game.winReason
              ? { reason: msg.game.winReason, winner: msg.game.winner }
              : state.game.over,
        },
      }
    }
    case 'game_over': {
      const youColor =
        state.game.youColor ?? deriveYouColor(msg.game, state.session.playerId)
      const opp = youColor ? opponentOf(msg.game, youColor) : null
      return {
        ...state,
        session: applyServerNow(state.session, msg.game.serverNow),
        game: {
          ...state.game,
          game: msg.game,
          youColor,
          opponentConnected: opp ? opp.connected : state.game.opponentConnected,
          opponentDeadline: msg.game.reconnectDeadline,
          over: { reason: msg.reason, winner: msg.winner },
        },
      }
    }
    case 'opponent_presence':
      return {
        ...state,
        game: {
          ...state.game,
          opponentConnected: msg.connected,
          opponentDeadline: msg.deadline,
          opponentReconnectedAt: msg.connected ? Date.now() : null,
        },
      }
    case 'error':
      return { ...state, error: { code: msg.code, message: msg.message, ts: Date.now() } }
    default:
      return state
  }
}

function opponentOf(game: GameState, youColor: Color) {
  return youColor === 'black' ? game.players.white : game.players.black
}

/* ── Provider ────────────────────────────────────────────────────── */

export interface StoreApi {
  connect: (nick?: string) => void
  createRoom: (name?: string) => void
  joinRoom: (roomId: string) => void
  leaveRoom: () => void
  listRooms: () => void
  matchmake: () => void
  matchmakeCancel: () => void
  playAi: (difficulty: Difficulty) => void
  move: (x: number, y: number) => void
  resign: () => void
  dismissError: () => void
  clearGame: () => void
}

const StoreContext = createContext<{ state: AppState; api: StoreApi } | null>(null)

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
      playAi: (difficulty) => send({ type: 'play_ai', difficulty }),
      move: (x, y) => send({ type: 'move', x, y }),
      resign: () => send({ type: 'resign' }),
      dismissError: () => dispatch({ type: 'DISMISS_ERROR' }),
      clearGame: () => dispatch({ type: 'CLEAR_GAME' }),
    }
  }, [connect])

  const value = useMemo(() => ({ state, api }), [state, api])
  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>
}

export function useStore() {
  const ctx = useContext(StoreContext)
  if (!ctx) throw new Error('useStore must be used inside StoreProvider')
  return ctx
}
