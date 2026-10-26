// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppState } from '../state/reducer'
import type { StoreApi } from '../state/useStore'
import type { Room } from '../ws/protocol'

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
    expect(storeMock.api.playAi).toHaveBeenCalledWith(difficulty)
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
