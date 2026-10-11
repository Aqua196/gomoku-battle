import { describe, expect, it } from 'vitest'
import { parseServerMessage } from './protocol'
import type { GameState } from './protocol'

function sampleGame(): GameState {
  return {
    roomId: 'r_1',
    status: 'playing',
    board: Array.from({ length: 15 }, () => Array(15).fill(0)),
    turn: 'black',
    moveNumber: 0,
    lastMove: null,
    turnDeadline: 1730000030000,
    reconnectDeadline: null,
    players: {
      black: { playerId: 'p_b', nick: 'a', connected: true, isAI: false },
      white: { playerId: 'p_w', nick: 'b', connected: true, isAI: false },
    },
    winner: null,
    winReason: null,
    serverNow: 1730000000000,
  }
}

describe('parseServerMessage', () => {
  it('accepts every server→client message type (PROTOCOL §0–§3)', () => {
    const msgs: unknown[] = [
      { type: 'welcome', playerId: 'p_1', token: 't', nick: 'n', resumed: false, serverNow: 1730000000000 },
      { type: 'pong', ts: 1730000000000 },
      { type: 'room', action: 'created', room: { id: 'r_1', name: 'x', status: 'waiting', players: [], isAI: false, createdAt: 1 } },
      { type: 'room', action: 'joined', room: { id: 'r_1', name: 'x', status: 'waiting', players: [], isAI: false, createdAt: 1 } },
      { type: 'room', action: 'left', roomId: 'r_1' },
      { type: 'room_list', rooms: [] },
      { type: 'matchmaking', status: 'queued', position: 1 },
      { type: 'matchmaking', status: 'cancelled' },
      { type: 'game_start', game: sampleGame(), youColor: 'black' },
      { type: 'game_state', game: sampleGame() },
      { type: 'game_over', reason: 'five', winner: 'black', game: sampleGame() },
      { type: 'game_over', reason: 'draw', winner: null, game: sampleGame() },
      { type: 'opponent_presence', connected: false, deadline: 1730000030000 },
      { type: 'opponent_presence', connected: true, deadline: null },
      { type: 'error', code: 'not_implemented', message: 'M2' },
    ]
    for (const m of msgs) {
      expect(parseServerMessage(m), JSON.stringify(m)).not.toBeNull()
    }
  })

  it('keeps protocol field names intact', () => {
    const msg = parseServerMessage({
      type: 'game_start',
      game: sampleGame(),
      youColor: 'white',
    })
    expect(msg).toMatchObject({ type: 'game_start', youColor: 'white' })
    if (msg?.type === 'game_start') {
      expect(msg.game.players.black.playerId).toBe('p_b')
      expect(msg.game.turnDeadline).toBe(1730000030000)
    }
  })

  it('ignores unknown types (§0: receiver ignores)', () => {
    expect(parseServerMessage({ type: 'future_msg', x: 1 })).toBeNull()
  })

  it('rejects malformed payloads', () => {
    for (const bad of [null, undefined, 42, 'welcome', [], {}, { type: 1 }]) {
      expect(parseServerMessage(bad)).toBeNull()
    }
  })

  it('discriminates room actions', () => {
    const left = parseServerMessage({ type: 'room', action: 'left', roomId: 'r_9' })
    expect(left).toMatchObject({ action: 'left', roomId: 'r_9' })
    if (left?.type === 'room' && left.action === 'left') {
      expect(left.roomId).toBe('r_9')
    }
  })
})
