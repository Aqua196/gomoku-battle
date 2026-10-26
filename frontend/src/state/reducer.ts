import type { ConnectionStatus } from '../ws/client'
import type { Color, GameOverReason, GameState, Room, ServerMessage } from '../ws/protocol'
import { smoothClockOffset } from './clock'

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
  /** watching someone else's game (§11): no seat, youColor stays null */
  spectating: boolean
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
    spectating: false,
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
    case 'spectate_start':
      return {
        ...state,
        session: applyServerNow(state.session, msg.game.serverNow),
        game: { ...initialState.game, game: msg.game, spectating: true },
      }
    case 'spectate_end': {
      // Only drop the board we are actually watching; a stale end (e.g.
      // after switching rooms, or once our own game has started) is ignored.
      const watched = state.game.spectating ? state.game.game?.roomId : undefined
      if (!watched || (msg.roomId !== '' && msg.roomId !== watched)) return state
      return { ...state, game: { ...initialState.game } }
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
