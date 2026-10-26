// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppState } from '../state/reducer'
import type { StoreApi } from '../state/useStore'
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

vi.mock('../state/useStore', () => ({
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
      spectating: false,
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

  it('abandoned 终局显示「双方弃局」而非「平局」', () => {
    const s = aiState(
      aiGame({ status: 'finished', winner: null, winReason: 'abandoned' }),
    )
    s.game.over = { reason: 'abandoned', winner: null }
    storeMock.state.current = s
    renderGame()
    expect(screen.getByText('双方弃局')).toBeInTheDocument()
    expect(screen.queryByText('平局')).toBeNull()
    expect(screen.getByText('双方断线超时')).toBeInTheDocument()
  })

  it('draw 终局仍显示「平局」', () => {
    const s = aiState(aiGame({ status: 'finished', winner: null, winReason: 'draw' }))
    s.game.over = { reason: 'draw', winner: null }
    storeMock.state.current = s
    renderGame()
    expect(screen.getByText('平局')).toBeInTheDocument()
  })

  it('人机房间保留认输按钮', () => {
    storeMock.state.current = aiState(aiGame())
    renderGame()
    expect(screen.getByRole('button', { name: '认输' })).toBeInTheDocument()
  })
})

describe('<Game> 观战', () => {
  const watched = () =>
    aiGame({
      roomId: 'r_watch',
      turn: 'white',
      spectators: 2,
      players: {
        black: { playerId: 'p_a', nick: '甲', connected: true, isAI: false },
        white: { playerId: 'p_b', nick: '乙', connected: true, isAI: false },
      },
    })
  const spectatorState = (game: GameState, over: AppState['game']['over'] = null): AppState => {
    const s = aiState(game)
    return { ...s, game: { ...s.game, youColor: null, spectating: true, over } }
  }

  beforeEach(() => {
    for (const k of Object.keys(storeMock.api)) delete (storeMock.api as unknown as Record<string, unknown>)[k]
    storeMock.api.stopSpectating = vi.fn()
    storeMock.api.clearGame = vi.fn()
    storeMock.api.resign = vi.fn()
  })

  afterEach(() => cleanup())

  it('shows the turn by colour, the viewer count and 退出观战 instead of 认输', () => {
    storeMock.state.current = spectatorState(watched())
    renderGame()
    expect(screen.getByRole('heading', { name: '观战' })).toBeInTheDocument()
    expect(screen.getByText('白方落子中')).toBeInTheDocument()
    expect(screen.getByTitle('观战人数')).toHaveTextContent('2 人观战')
    expect(screen.queryByRole('button', { name: /认输/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /退出观战/ }))
    expect(storeMock.api.stopSpectating).toHaveBeenCalledTimes(1)
    expect(storeMock.api.clearGame).toHaveBeenCalledTimes(1)
  })

  it('names the winning colour in the verdict and offers the replay', () => {
    const done = { ...watched(), status: 'finished' as const, winner: 'white' as const, winReason: 'resign' as const }
    storeMock.state.current = spectatorState(done, { reason: 'resign', winner: 'white' })
    renderGame()
    expect(screen.getByRole('heading', { name: '白方胜' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /再/ })).toBeNull()
    expect(screen.getByRole('button', { name: /逐手复盘/ })).toBeInTheDocument()
  })
})
