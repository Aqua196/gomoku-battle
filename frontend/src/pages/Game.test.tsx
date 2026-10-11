// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppState, StoreApi } from '../state/store'
import type { GameState } from '../ws/protocol'

const storeMock = vi.hoisted(() => {
  const state: { current: AppState | null } = { current: null }
  const api: Partial<StoreApi> = {}
  return {
    state,
    api: api as StoreApi,
    get() {
      return { state: this.state.current as AppState, api: this.api }
    },
  }
})

vi.mock('../state/store', () => ({
  useStore: () => storeMock.get(),
}))

import { Game } from './Game'

function aiGame(over: Partial<GameState> = {}): GameState {
  return {
    roomId: 'r_ai',
    status: 'playing',
    board: Array.from({ length: 15 }, () => Array(15).fill(0)),
    turn: 'black',
    moveNumber: 1,
    lastMove: { x: 7, y: 7 },
    turnDeadline: Date.now() + 30_000,
    reconnectDeadline: null,
    players: {
      black: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
      white: { playerId: 'ai_1', nick: 'AI (normal)', connected: true, isAI: true },
    },
    winner: null,
    winReason: null,
    serverNow: Date.now(),
    ...over,
  }
}

function aiState(game: GameState, youColor: 'black' | 'white' = 'black'): AppState {
  return {
    session: {
      status: 'connected',
      playerId: 'p_me',
      nick: 'me',
      resumed: false,
      latencyMs: null,
      clockOffsetMs: null,
    },
    lobby: { rooms: [], myRoomId: null, matchmaking: false, queuePosition: null },
    game: {
      game,
      youColor,
      opponentConnected: true,
      opponentDeadline: null,
      opponentReconnectedAt: null,
      over: null,
    },
    error: null,
  }
}

const renderGame = () =>
  render(
    <MemoryRouter initialEntries={['/game']}>
      <Game />
    </MemoryRouter>,
  )

describe('<Game> 人机对局', () => {
  beforeEach(() => {
    for (const k of Object.keys(storeMock.api)) delete (storeMock.api as unknown as Record<string, unknown>)[k]
    storeMock.api.move = vi.fn()
    storeMock.api.resign = vi.fn()
    storeMock.api.clearGame = vi.fn()
  })

  afterEach(() => cleanup())

  it('房间信息区渲染「人机 · 难度」徽标与 AI 昵称', () => {
    storeMock.state.current = aiState(aiGame())
    renderGame()
    expect(screen.getByText('人机 · 普通')).toBeInTheDocument()
    // AI nick renders in its player card and the footer line.
    expect(screen.getAllByText(/AI \(normal\)/).length).toBeGreaterThan(0)
  })

  it('人人对局不显示人机徽标', () => {
    const pvp = aiGame({
      players: {
        black: { playerId: 'p_me', nick: 'me', connected: true, isAI: false },
        white: { playerId: 'p_op', nick: 'op', connected: true, isAI: false },
      },
    })
    storeMock.state.current = aiState(pvp)
    renderGame()
    expect(screen.queryByText(/人机 ·/)).toBeNull()
  })

  it('我方落子后（turn=AI）显示「AI 思考中」，AI 落子后消失', () => {
    // After my move: lastMove mine, turn switched to white (the AI seat).
    storeMock.state.current = aiState(aiGame({ turn: 'white' }))
    const view = renderGame()
    expect(screen.getByText(/AI 思考中/)).toBeInTheDocument()
    expect(screen.queryByText('对手思考中')).toBeNull()

    // AI replies: turn back to me — hint disappears.
    view.unmount()
    storeMock.state.current = aiState(aiGame({ turn: 'black', moveNumber: 2 }))
    renderGame()
    expect(screen.queryByText(/AI 思考中/)).toBeNull()
    expect(screen.getByText('你的回合')).toBeInTheDocument()
  })

  it('对局结束后不再显示思考提示，结算弹层可返回大厅', () => {
    const s = aiState(aiGame({ status: 'finished', winner: 'black', winReason: 'five' }))
    s.game.over = { reason: 'five', winner: 'black' }
    storeMock.state.current = s
    renderGame()
    expect(screen.queryByText(/思考中/)).toBeNull()
    expect(screen.getByText('你赢了！')).toBeInTheDocument()
    expect(screen.getByText('返回大厅')).toBeInTheDocument()
  })

  it('人机房间保留认输按钮', () => {
    storeMock.state.current = aiState(aiGame())
    renderGame()
    expect(screen.getByRole('button', { name: '认输' })).toBeInTheDocument()
  })
})
