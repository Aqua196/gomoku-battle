import { createContext, useContext } from 'react'
import type { Color, Difficulty } from '../ws/protocol'
import type { AppState } from './reducer'

export interface StoreApi {
  connect: (nick?: string) => void
  createRoom: (name?: string) => void
  joinRoom: (roomId: string) => void
  leaveRoom: () => void
  listRooms: () => void
  matchmake: () => void
  matchmakeCancel: () => void
  playAi: (difficulty: Difficulty, color?: Color | 'random') => void
  move: (x: number, y: number) => void
  resign: () => void
  dismissError: () => void
  clearGame: () => void
}

export const StoreContext = createContext<{ state: AppState; api: StoreApi } | null>(null)

export function useStore() {
  const ctx = useContext(StoreContext)
  if (!ctx) throw new Error('useStore must be used inside StoreProvider')
  return ctx
}
