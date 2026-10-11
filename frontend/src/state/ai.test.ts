import { describe, expect, it } from 'vitest'
import { aiBadgeLabel, aiOpponent, isAiThinking } from './ai'
import type { GamePlayer, GameState } from '../ws/protocol'

const aiP = (nick: string): GamePlayer => ({
  playerId: 'ai_1',
  nick,
  connected: true,
  isAI: true,
})
const human = (id: string, nick: string): GamePlayer => ({
  playerId: id,
  nick,
  connected: true,
  isAI: false,
})

function game(over: Partial<GameState> = {}): GameState {
  return {
    roomId: 'r_ai',
    status: 'playing',
    board: Array.from({ length: 15 }, () => Array(15).fill(0)),
    turn: 'white',
    moveNumber: 2,
    lastMove: { x: 7, y: 7 },
    turnDeadline: 1730000030000,
    reconnectDeadline: null,
    players: {
      black: human('p_me', 'me'),
      white: aiP('AI (normal)'),
    },
    winner: null,
    winReason: null,
    serverNow: 1730000000000,
    ...over,
  }
}

describe('aiOpponent', () => {
  it('finds the AI seat whichever color it plays', () => {
    expect(aiOpponent(game())?.nick).toBe('AI (normal)')
    const blackAi = game({
      players: { black: aiP('AI (hard)'), white: human('p_me', 'me') },
    })
    expect(aiOpponent(blackAi)?.playerId).toBe('ai_1')
  })

  it('returns null for human-vs-human games', () => {
    const pvp = game({
      players: { black: human('p_a', 'a'), white: human('p_b', 'b') },
    })
    expect(aiOpponent(pvp)).toBeNull()
  })
})

describe('aiBadgeLabel', () => {
  it('maps server AI nicks to 「人机 · 难度」', () => {
    expect(aiBadgeLabel(aiP('AI (easy)'))).toBe('人机 · 简单')
    expect(aiBadgeLabel(aiP('AI (normal)'))).toBe('人机 · 普通')
    expect(aiBadgeLabel(aiP('AI (hard)'))).toBe('人机 · 困难')
  })

  it('falls back to the raw parenthetical / nick for unknown nicks', () => {
    expect(aiBadgeLabel(aiP('AI (expert)'))).toBe('人机 · expert')
    expect(aiBadgeLabel(aiP('机器人'))).toBe('人机 · 机器人')
  })

  it('returns null for human players and missing seats', () => {
    expect(aiBadgeLabel(human('p', 'bob'))).toBeNull()
    expect(aiBadgeLabel(null)).toBeNull()
  })
})

describe('isAiThinking', () => {
  it('true while the turn belongs to the AI seat', () => {
    // I am black; after my move game.turn = white (AI).
    expect(isAiThinking(game({ turn: 'white' }), aiP('AI (normal)'), 'black', false)).toBe(true)
  })

  it('false on my turn, when finished/over, or vs humans', () => {
    const ai = aiP('AI (normal)')
    expect(isAiThinking(game({ turn: 'black' }), ai, 'black', false)).toBe(false)
    expect(isAiThinking(game({ status: 'finished' }), ai, 'black', false)).toBe(false)
    expect(isAiThinking(game({ turn: 'white' }), ai, 'black', true)).toBe(false)
    expect(isAiThinking(game({ turn: 'white' }), human('p', 'bob'), 'black', false)).toBe(false)
    expect(isAiThinking(game({ turn: 'white' }), ai, null, false)).toBe(false)
  })
})
