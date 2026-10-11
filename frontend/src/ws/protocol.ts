/**
 * Message types aligned field-by-field with PROTOCOL.md v1 (M1, frozen).
 * Do not rename fields; any deviation from the spec is a bug.
 */

export const BOARD_SIZE = 15

/** Protocol time constants (§6) */
export const MOVE_TIMEOUT_MS = 30_000
export const RECONNECT_WINDOW_MS = 30_000

export type Color = 'black' | 'white'
export type Cell = 0 | 1 | 2
/** board[y][x], x = column 0-14, y = row 0-14. 0 empty, 1 black, 2 white. */
export type Board = Cell[][]

export type RoomStatus = 'waiting' | 'playing' | 'finished'
export type GameStatus = 'playing' | 'finished'
export type GameOverReason = 'five' | 'draw' | 'timeout' | 'resign' | 'disconnect_timeout'
export type Difficulty = 'easy' | 'normal' | 'hard'
export type MatchmakingStatus = 'queued' | 'cancelled'
export type RoomAction = 'created' | 'joined' | 'left'

/** §8 error codes */
export type ErrorCode =
  | 'bad_request'
  | 'hello_timeout'
  | 'unauthorized'
  | 'not_in_lobby'
  | 'already_in_room'
  | 'not_in_room'
  | 'room_not_found'
  | 'room_full'
  | 'not_your_turn'
  | 'illegal_move'
  | 'game_over'
  | 'invalid_difficulty'
  | 'not_implemented'
  | 'internal'

/* ── Client → Server ─────────────────────────────────────────────── */

export interface HelloMsg {
  type: 'hello'
  nick: string
  token?: string
}
export interface PingMsg {
  type: 'ping'
}
export interface CreateRoomMsg {
  type: 'create_room'
  name?: string
}
export interface JoinRoomMsg {
  type: 'join_room'
  roomId: string
}
export interface LeaveRoomMsg {
  type: 'leave_room'
}
export interface ListRoomsMsg {
  type: 'list_rooms'
}
export interface MatchmakeMsg {
  type: 'matchmake'
}
export interface MatchmakeCancelMsg {
  type: 'matchmake_cancel'
}
export interface PlayAiMsg {
  type: 'play_ai'
  difficulty: Difficulty
}
export interface ResignMsg {
  type: 'resign'
}
export interface MoveMsg {
  type: 'move'
  x: number
  y: number
}

export type ClientMessage =
  | HelloMsg
  | PingMsg
  | CreateRoomMsg
  | JoinRoomMsg
  | LeaveRoomMsg
  | ListRoomsMsg
  | MatchmakeMsg
  | MatchmakeCancelMsg
  | PlayAiMsg
  | ResignMsg
  | MoveMsg

/* ── Server → Client ─────────────────────────────────────────────── */

export interface WelcomeMsg {
  type: 'welcome'
  playerId: string
  token: string
  nick: string
  resumed: boolean
  /** server clock ts for client offset estimation (§1) */
  serverNow: number
}
export interface PongMsg {
  type: 'pong'
  ts: number
}

export interface RoomPlayer {
  playerId: string
  nick: string
  color: Color | null
  connected: boolean
}
export interface Room {
  id: string
  name: string
  status: RoomStatus
  players: RoomPlayer[]
  isAI: boolean
  createdAt: number
}

export type RoomMsg =
  | { type: 'room'; action: 'created' | 'joined'; room: Room }
  | { type: 'room'; action: 'left'; roomId: string }

export interface RoomListMsg {
  type: 'room_list'
  rooms: Room[]
}
export interface MatchmakingMsg {
  type: 'matchmaking'
  status: MatchmakingStatus
  position?: number
}
export interface GameStartMsg {
  type: 'game_start'
  game: GameState
  youColor: Color
}
export interface GameStateMsg {
  type: 'game_state'
  game: GameState
}
export interface GameOverMsg {
  type: 'game_over'
  reason: GameOverReason
  winner: Color | null
  game: GameState
}
export interface OpponentPresenceMsg {
  type: 'opponent_presence'
  connected: boolean
  deadline: number | null
}
export interface ErrorMsg {
  type: 'error'
  code: ErrorCode | string
  message: string
}

export interface GamePlayer {
  playerId: string
  nick: string
  connected: boolean
  isAI: boolean
}
export interface GameState {
  roomId: string
  status: GameStatus
  board: Board
  turn: Color
  moveNumber: number
  lastMove: { x: number; y: number } | null
  /** ts when the current move times out; null when status != 'playing' */
  turnDeadline: number | null
  /** ts when a disconnected player loses by forfeit; null otherwise */
  reconnectDeadline: number | null
  players: { black: GamePlayer; white: GamePlayer }
  winner: Color | null
  winReason: GameOverReason | null
  /** server clock ts when this snapshot was produced */
  serverNow: number
}

export type ServerMessage =
  | WelcomeMsg
  | PongMsg
  | RoomMsg
  | RoomListMsg
  | MatchmakingMsg
  | GameStartMsg
  | GameStateMsg
  | GameOverMsg
  | OpponentPresenceMsg
  | ErrorMsg

const SERVER_TYPES = new Set([
  'welcome',
  'pong',
  'room',
  'room_list',
  'matchmaking',
  'game_start',
  'game_state',
  'game_over',
  'opponent_presence',
  'error',
])

/**
 * Validate a decoded JSON value as a protocol S→C message.
 * Returns null for malformed input and for unknown types (§0: ignored).
 */
export function parseServerMessage(data: unknown): ServerMessage | null {
  if (typeof data !== 'object' || data === null) return null
  const type = (data as { type?: unknown }).type
  if (typeof type !== 'string' || !SERVER_TYPES.has(type)) return null
  return data as ServerMessage
}
