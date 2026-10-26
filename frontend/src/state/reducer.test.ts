import { afterEach, describe, expect, it, vi } from 'vitest'
import { deriveYouColor, initialState, reducer } from './reducer'
import {
  RESUME_GRACE_MS,
  createResumeWatchdog,
  smoothClockOffset,
} from './clock'
import type { AppState } from './reducer'
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
    serverNow: 1730000000000,
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

function welcomed(resumed = false): AppState {
  return reducer(initialState, {
    type: 'SERVER',
    msg: {
      type: 'welcome',
      playerId: 'p_me',
      token: 'tok',
      nick: 'me',
      resumed,
      serverNow: 1730000000000,
    },
  })
}

const srv = (state: AppState, msg: ServerMessage) => reducer(state, { type: 'SERVER', msg })

describe('store reducer', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

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
    let s = welcomed(true)
    s = srv(s, { type: 'game_state', game: game() })
    expect(s.game.youColor).toBe('black')
    expect(s.game.game?.roomId).toBe('r_1')
  })

  it('R1: welcome(resumed=false) clears stale lobby/game views', () => {
    // Stale view left over from a lost session (server restart / expired token).
    let s = welcomed()
    s = srv(s, { type: 'room', action: 'created', room })
    s = srv(s, { type: 'game_start', game: game(), youColor: 'black' })
    expect(s.game.game).not.toBeNull()

    s = srv(s, {
      type: 'welcome',
      playerId: 'p_me2',
      token: 'tok2',
      nick: 'me',
      resumed: false,
      serverNow: 1730000000000,
    })
    expect(s.game.game).toBeNull()
    expect(s.game.over).toBeNull()
    expect(s.lobby).toEqual(initialState.lobby)
    expect(s.session.playerId).toBe('p_me2')
  })

  it('R1: welcome(resumed=true) keeps views until game_state or watchdog clears', () => {
    let s = welcomed()
    s = srv(s, { type: 'game_start', game: game(), youColor: 'black' })
    s = srv(s, {
      type: 'welcome',
      playerId: 'p_me',
      token: 'tok',
      nick: 'me',
      resumed: true,
      serverNow: 1730000000000,
    })
    // Still pinned — either a live game_state follows or the provider
    // watchdog fires CLEAR_GAME after RESUME_GRACE_MS.
    expect(s.game.game).not.toBeNull()
    s = srv(s, { type: 'game_state', game: game({ moveNumber: 5 }) })
    expect(s.game.game?.moveNumber).toBe(5)
  })

  it('R1: resume watchdog fires once and can be cancelled', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const wd = createResumeWatchdog(onExpire)
    wd.arm()
    vi.advanceTimersByTime(RESUME_GRACE_MS - 1)
    expect(onExpire).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(onExpire).toHaveBeenCalledTimes(1)

    wd.arm()
    wd.cancel()
    vi.advanceTimersByTime(RESUME_GRACE_MS)
    expect(onExpire).toHaveBeenCalledTimes(1)

    // re-arm resets the timer
    wd.arm()
    vi.advanceTimersByTime(RESUME_GRACE_MS - 10)
    wd.arm()
    vi.advanceTimersByTime(RESUME_GRACE_MS - 1)
    expect(onExpire).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    expect(onExpire).toHaveBeenCalledTimes(2)
  })

  it('R10: serverNow drives the clock-offset estimate', () => {
    vi.setSystemTime(1_000_000)
    let s = srv(initialState, {
      type: 'welcome',
      playerId: 'p',
      token: 't',
      nick: 'n',
      resumed: false,
      serverNow: 1_000_500,
    })
    expect(s.session.clockOffsetMs).toBe(500)

    // Second sample smooths via EMA (0.3): 500 + 0.3 * (0 - 500) = 350.
    vi.setSystemTime(1_001_000)
    s = srv(s, { type: 'game_state', game: game({ serverNow: 1_001_000 }) })
    expect(s.session.clockOffsetMs).toBeCloseTo(350)
  })

  it('smoothClockOffset: first sample seeds, later samples smooth', () => {
    expect(smoothClockOffset(null, 100)).toBe(100)
    expect(smoothClockOffset(100, 200)).toBeCloseTo(130)
    expect(smoothClockOffset(-50, -100)).toBeCloseTo(-65)
  })

  it('R14: LOCAL_ERROR surfaces a toast entry', () => {
    const s = reducer(welcomed(), {
      type: 'LOCAL_ERROR',
      code: 'offline',
      message: 'down',
    })
    expect(s.error?.code).toBe('offline')
    expect(s.error?.message).toBe('down')
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

  it('R1/R2: game_state re-syncs presence fields from the snapshot', () => {
    let s = welcomed()
    s = srv(s, { type: 'game_start', game: game(), youColor: 'black' })

    // Snapshot carries an offline opponent + forfeit deadline → view syncs
    // even without a preceding opponent_presence frame.
    const dropped = game({
      reconnectDeadline: 1730000099000,
      players: {
        black: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
        white: { playerId: 'p_op', nick: 'op', connected: false, isAI: false },
      },
    })
    s = srv(s, { type: 'game_state', game: dropped })
    expect(s.game.opponentConnected).toBe(false)
    expect(s.game.opponentDeadline).toBe(1730000099000)

    // Opponent back online in the next snapshot → banner heals itself.
    const back = game({
      players: {
        black: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
        white: { playerId: 'p_op', nick: 'op', connected: true, isAI: false },
      },
    })
    s = srv(s, { type: 'game_state', game: back })
    expect(s.game.opponentConnected).toBe(true)
    expect(s.game.opponentDeadline).toBeNull()
    expect(s.game.opponentReconnectedAt).not.toBeNull()
  })

  it('R1/R2: game_over also refreshes presence fields', () => {
    let s = welcomed()
    s = srv(s, { type: 'game_start', game: game(), youColor: 'white' })
    const fin = game({
      status: 'finished',
      winner: 'black',
      winReason: 'disconnect_timeout',
      reconnectDeadline: 1730000010000,
      players: {
        black: { playerId: 'p_op', nick: 'op', connected: false, isAI: false },
        white: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
      },
    })
    s = srv(s, {
      type: 'game_over',
      reason: 'disconnect_timeout',
      winner: 'black',
      game: fin,
    })
    expect(s.game.opponentConnected).toBe(false)
    expect(s.game.over).toEqual({ reason: 'disconnect_timeout', winner: 'black' })
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
