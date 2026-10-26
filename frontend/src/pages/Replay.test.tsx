// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GameRecord } from '../ws/protocol'

vi.mock('../state/useStore', () => ({
  useStore: () => ({
    state: { session: { status: 'connected', playerId: null, latencyMs: null } },
    api: {},
  }),
}))

import { Replay } from './Replay'

const rec: GameRecord = {
  id: 'r_rep',
  black: { playerId: 'p_a', nick: '甲', isAI: false },
  white: { playerId: 'p_b', nick: '乙', isAI: false },
  winner: 'black',
  reason: 'five',
  moveCount: 9,
  moves: [
    { x: 7, y: 7 },
    { x: 0, y: 0 },
    { x: 8, y: 7 },
    { x: 0, y: 1 },
    { x: 9, y: 7 },
    { x: 0, y: 2 },
    { x: 10, y: 7 },
    { x: 0, y: 3 },
    { x: 11, y: 7 },
  ],
  startedAt: 1_700_000_000_000,
  endedAt: 1_700_000_065_000,
}

const fetchMock = vi.fn()
vi.stubGlobal('fetch', fetchMock)

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/replay/:id" element={<Replay />} />
      </Routes>
    </MemoryRouter>,
  )

const stones = () => document.querySelectorAll('.ink-stone').length

describe('<Replay>', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    fetchMock.mockImplementation(async (url: string) =>
      url === '/api/games/r_rep'
        ? new Response(JSON.stringify(rec), { status: 200 })
        : new Response('{}', { status: 404 }),
    )
  })
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('opens on the final position and steps through the record', async () => {
    renderAt('/replay/r_rep')
    expect(await screen.findByText('第 9 手 · 黑 L8')).toBeInTheDocument()
    expect(screen.getByText('黑胜 · 五连')).toBeInTheDocument()
    expect(stones()).toBeGreaterThanOrEqual(9)

    fireEvent.click(screen.getByRole('button', { name: '上一手' }))
    expect(screen.getByText('第 8 手 · 白 A12')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '第一手前' }))
    expect(screen.getByText('空枰')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一手' })).toBeDisabled()

    fireEvent.keyDown(window, { key: 'ArrowRight' })
    expect(screen.getByText('第 1 手 · 黑 H8')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'End' })
    expect(screen.getByText('第 9 手 · 黑 L8')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '第 3 手 I8' }))
    expect(screen.getByText('第 3 手 · 黑 I8')).toBeInTheDocument()
  })

  it('autoplays from the empty board', async () => {
    renderAt('/replay/r_rep')
    await screen.findByText('第 9 手 · 黑 L8')
    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: '自动播放' }))
    expect(screen.getByText('空枰')).toBeInTheDocument()
    for (let i = 0; i < 2; i++) {
      // one beat per act: each stone schedules the next after rendering
      await act(async () => {
        vi.advanceTimersByTime(710)
      })
    }
    expect(screen.getByText('第 2 手 · 白 A15')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '暂停' }))
  })

  it('says so when the record does not exist', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderAt('/replay/r_gone')
    await act(async () => {
      vi.advanceTimersByTime(1000) // one retry for a record still being written
    })
    expect(await screen.findByText('查无此局')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
