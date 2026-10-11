import { describe, expect, it } from 'vitest'
import { deriveYouColor, initialState, reducer } from './store'
import type { AppState } from './store'
import type { GameState, Room, ServerMessage } from '../ws/protocol'

function game(over: Partial<GameState> = {}): GameState {
  return {
    roomId: 'r_1',
    status: 'playing',
    board: Array.from({ length: 15 }, () => Array(15).fill(0)),
    turn: 'black',
    moveNumber: 1,
    lastMove: { x: 7, y: 7 },
    turnDeadline: 1730000030000,
    reconnectDeadline: null,
    players: {
      black: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
      white: { playerId: 'p_op', nick: 'op', connected: true, isAI: false },
    },
    winner: null,
    winReason: null,
    ...over,
  }
}

const room: Room = {
  id: 'r_1',
  name: 'test',
  status: 'waiting',
  players: [{ playerId: 'p_me', nick: 'me', color: 'black', connected: true }],
  isAI: false,
  createdAt: 1,
}

function welcomed(): AppState {
  return reducer(initialState, {
    type: 'SERVER',
    msg: { type: 'welcome', playerId: 'p_me', token: 'tok', nick: 'me', resumed: false },
  })
}

const srv = (state: AppState, msg: ServerMessage) => reducer(state, { type: 'SERVER', msg })

describe('store reducer', () => {
  it('welcome establishes the session', () => {
    const s = welcomed()
    expect(s.session.playerId).toBe('p_me')
    expect(s.session.nick).toBe('me')
    expect(s.session.status).toBe('connected')
    expect(s.session.resumed).toBe(false)
  })

  it('room_list / room lifecycle track myRoomId', () => {
    let s = welcomed()
    s = srv(s, { type: 'room', action: 'created', room })
    expect(s.lobby.myRoomId).toBe('r_1')
    s = srv(s, { type: 'room_list', rooms: [room] })
    expect(s.lobby.rooms).toHaveLength(1)
    s = srv(s, { type: 'room', action: 'left', roomId: 'r_1' })
    expect(s.lobby.myRoomId).toBeNull()
  })

  it('matchmaking queued → cancelled', () => {
    let s = welcomed()
    s = srv(s, { type: 'matchmaking', status: 'queued', position: 3 })
    expect(s.lobby.matchmaking).toBe(true)
    expect(s.lobby.queuePosition).toBe(3)
    s = srv(s, { type: 'matchmaking', status: 'cancelled' })
    expect(s.lobby.matchmaking).toBe(false)
    expect(s.lobby.queuePosition).toBeNull()
  })

  it('game_start sets the game view and clears lobby seat', () => {
    let s = welcomed()
    s = srv(s, { type: 'room', action: 'created', room })
    s = srv(s, { type: 'game_start', game: game(), youColor: 'black' })
    expect(s.lobby.myRoomId).toBeNull()
    expect(s.game.game?.roomId).toBe('r_1')
    expect(s.game.youColor).toBe('black')
    expect(s.game.over).toBeNull()
  })

  it('resume path: bare game_state derives youColor from playerId', () => {
    // After reconnect the server sends welcome(resumed) + game_state — no game_start.
    let s = reducer(initialState, {
      type: 'SERVER',
      msg: { type: 'welcome', playerId: 'p_me', token: 'tok', nick: 'me', resumed: true },
    })
    s = srv(s, { type: 'game_state', game: game() })
    expect(s.game.youColor).toBe('black')
    expect(s.game.game?.roomId).toBe('r_1')
  })

  it('game_state refreshes the board; game_over resolves the result', () => {
    let s = welcomed()
    s = srv(s, { type: 'game_start', game: game(), youColor: 'white' })
    const moved = game({ moveNumber: 2, turn: 'white' })
    moved.board[7][7] = 1
    s = srv(s, { type: 'game_state', game: moved })
    expect(s.game.game?.board[7][7]).toBe(1)
    expect(s.game.game?.moveNumber).toBe(2)

    s = srv(s, {
      type: 'game_over',
      reason: 'five',
      winner: 'black',
      game: game({ status: 'finished', winner: 'black', winReason: 'five' }),
    })
    expect(s.game.over).toEqual({ reason: 'five', winner: 'black' })
    // Board stays for review behind the overlay until CLEAR_GAME.
    expect(s.game.game).not.toBeNull()

    s = reducer(s, { type: 'CLEAR_GAME' })
    expect(s.game.game).toBeNull()
    expect(s.game.over).toBeNull()
  })

  it('opponent_presence drives the disconnect banner', () => {
    let s = welcomed()
    s = srv(s, { type: 'game_start', game: game(), youColor: 'black' })
    s = srv(s, { type: 'opponent_presence', connected: false, deadline: 999 })
    expect(s.game.opponentConnected).toBe(false)
    expect(s.game.opponentDeadline).toBe(999)
    s = srv(s, { type: 'opponent_presence', connected: true, deadline: null })
    expect(s.game.opponentConnected).toBe(true)
    expect(s.game.opponentDeadline).toBeNull()
    expect(s.game.opponentReconnectedAt).not.toBeNull()
  })

  it('error surfaces a toast entry; dismiss clears it', () => {
    let s = welcomed()
    s = srv(s, { type: 'error', code: 'not_implemented', message: 'M2' })
    expect(s.error?.code).toBe('not_implemented')
    s = reducer(s, { type: 'DISMISS_ERROR' })
    expect(s.error).toBeNull()
  })

  it('deriveYouColor matches playerId to a seat', () => {
    expect(deriveYouColor(game(), 'p_me')).toBe('black')
    expect(deriveYouColor(game(), 'p_op')).toBe('white')
    expect(deriveYouColor(game(), 'p_nobody')).toBeNull()
    expect(deriveYouColor(game(), null)).toBeNull()
  })
})
