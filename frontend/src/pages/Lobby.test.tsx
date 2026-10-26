// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render as rtlRender, screen } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setPref } from '../lib/prefs'
import type { AppState } from '../state/reducer'
import type { StoreApi } from '../state/useStore'
import type { GameRecord, Room } from '../ws/protocol'

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

import { Lobby } from './Lobby'

// The lobby reads records over HTTP and navigates to replays.
const render = (ui: ReactElement) => rtlRender(<MemoryRouter>{ui}</MemoryRouter>)
const fetchMock = vi.fn()
vi.stubGlobal('fetch', fetchMock)

function respond(games: GameRecord[]) {
  fetchMock.mockImplementation(async () => new Response(JSON.stringify({ games }), { status: 200 }))
}

function lobbyState(over: Partial<AppState['lobby']> = {}): AppState {
  return {
    session: {
      status: 'connected',
      playerId: 'p_me',
      nick: 'me',
      resumed: false,
      latencyMs: null,
      clockOffsetMs: null,
    },
    lobby: { rooms: [], myRoomId: null, matchmaking: false, queuePosition: null, ...over },
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
}

describe('<Lobby> 人机对战入口', () => {
  beforeEach(() => {
    storeMock.state.current = lobbyState()
    for (const k of Object.keys(storeMock.api)) delete (storeMock.api as unknown as Record<string, unknown>)[k]
    storeMock.api.playAi = vi.fn()
    storeMock.api.listRooms = vi.fn()
    setPref('aiSide', 'black')
    respond([])
  })

  afterEach(() => cleanup())

  it.each([
    ['简单', 'easy'],
    ['普通', 'normal'],
    ['困难', 'hard'],
  ] as const)('「%s」按钮发送 play_ai{difficulty:%s}', (label, difficulty) => {
    render(<Lobby />)
    fireEvent.click(screen.getByRole('button', { name: label }))
    expect(storeMock.api.playAi).toHaveBeenCalledTimes(1)
    expect(storeMock.api.playAi).toHaveBeenCalledWith(difficulty, 'black')
  })

  it.each([
    ['执白', 'white'],
    ['猜先', 'random'],
  ] as const)('选「%s」后开局带 color:%s', (label, side) => {
    render(<Lobby />)
    const radio = screen.getByRole('radio', { name: label })
    fireEvent.click(radio)
    expect(radio).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: '执黑' })).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(screen.getByRole('button', { name: '普通' }))
    expect(storeMock.api.playAi).toHaveBeenCalledWith('normal', side)
  })

  it('在自己房间中或匹配中时人机按钮禁用', () => {
    const myRoom: Room = {
      id: 'r_1',
      name: 'test',
      status: 'waiting',
      players: [{ playerId: 'p_me', nick: 'me', color: 'black', connected: true }],
      isAI: false,
      createdAt: 1,
    }
    storeMock.state.current = lobbyState({ myRoomId: 'r_1', rooms: [myRoom] })
    const { unmount } = render(<Lobby />)
    for (const label of ['简单', '普通', '困难']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }
    unmount()

    storeMock.state.current = lobbyState({ matchmaking: true })
    render(<Lobby />)
    for (const label of ['简单', '普通', '困难']) {
      expect(screen.getByRole('button', { name: label })).toBeDisabled()
    }
  })
})

describe('<Lobby> 观战与棋谱', () => {
  const record: GameRecord = {
    id: 'r_done',
    black: { playerId: 'p_me', nick: '执黑者', isAI: false },
    white: { playerId: 'ai_1', nick: 'AI (hard)', isAI: true },
    winner: 'white',
    reason: 'five',
    moveCount: 41,
    startedAt: 1_700_000_000_000,
    endedAt: 1_700_000_300_000,
  }

  beforeEach(() => {
    storeMock.state.current = lobbyState({
      rooms: [
        {
          id: 'r_live',
          name: '对弈中',
          status: 'playing',
          players: [
            { playerId: 'p_a', nick: 'a', color: 'black', connected: true },
            { playerId: 'p_b', nick: 'b', color: 'white', connected: true },
          ],
          isAI: false,
          createdAt: 2,
          spectators: 3,
        },
      ],
    })
    for (const k of Object.keys(storeMock.api)) delete (storeMock.api as unknown as Record<string, unknown>)[k]
    storeMock.api.listRooms = vi.fn()
    storeMock.api.spectate = vi.fn()
    respond([record])
  })

  afterEach(() => cleanup())

  it('playing rooms offer 观战 and show the viewer count', () => {
    render(<Lobby />)
    expect(screen.getByText('3 观')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '观战 对弈中' }))
    expect(storeMock.api.spectate).toHaveBeenCalledWith('r_live')
  })

  it('lists my records with my result, and switches to everyone\'s', async () => {
    render(<Lobby />)
    const row = await screen.findByRole('button', { name: '复盘 执黑者 对 AI (hard)' })
    expect(row).toHaveTextContent('白胜 · 五连')
    expect(row).toHaveTextContent('41 手')
    expect(row).toHaveTextContent('负')
    expect(fetchMock).toHaveBeenLastCalledWith('/api/players/p_me/games?limit=12', expect.anything())
    fireEvent.click(screen.getByRole('tab', { name: '最近对局' }))
    await screen.findByRole('button', { name: '复盘 执黑者 对 AI (hard)' })
    expect(fetchMock).toHaveBeenLastCalledWith('/api/games?limit=12', expect.anything())
  })
})
