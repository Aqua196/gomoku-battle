import { useEffect } from 'react'
import { BrowserRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { Game } from './pages/Game'
import { Lobby } from './pages/Lobby'
import { Replay } from './pages/Replay'
import { StoreProvider } from './state/store'
import { useStore } from './state/useStore'

const ERROR_LABEL: Record<string, string> = {
  not_implemented: '该功能暂未实现',
  not_your_turn: '还没轮到你',
  illegal_move: '非法落子',
  already_in_room: '你已在房间中',
  not_in_room: '你不在房间中',
  room_full: '房间已满',
  room_not_playing: '这一局还没开始，暂不能观战',
  room_not_found: '房间不存在',
  not_in_lobby: '当前不在大厅',
  game_over: '对局已结束',
  hello_timeout: '连接超时',
  unauthorized: '身份验证失败，正在重新连接',
  invalid_difficulty: '无效难度',
  bad_request: '请求格式错误',
  rate_limited: '操作太频繁，请稍后再试',
  internal: '服务器内部错误',
  offline: '连接已断开，正在重连，请稍候再试',
}

function Redirector() {
  const { state } = useStore()
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const inGame = state.game.game !== null
  // Only a game still being played pulls the viewer to the board; a
  // finished one may be left for its replay (/replay/:id) or the lobby.
  const live = inGame && state.game.over === null

  useEffect(() => {
    // game_start (or resumed game_state / spectate_start) → jump to the board.
    if (live && pathname !== '/game') navigate('/game')
  }, [live, pathname, navigate])

  useEffect(() => {
    // No active game (cleared via 返回大厅) → leave /game.
    if (!inGame && pathname === '/game') navigate('/')
  }, [inGame, pathname, navigate])

  return null
}

function ErrorToast() {
  const { state, api } = useStore()
  const err = state.error
  useEffect(() => {
    if (!err) return
    const t = setTimeout(() => api.dismissError(), 4500)
    return () => clearTimeout(t)
  }, [err, api])
  if (!err) return null
  return (
    <div className="toast" role="alert" key={err.ts} onClick={() => api.dismissError()}>
      <i className="toast-mark" />
      <span>{ERROR_LABEL[err.code] ?? err.message}</span>
      {err.code !== 'not_implemented' && <code className="toast-code">{err.code}</code>}
    </div>
  )
}

/** Thin top banner while an established session is re-connecting. */
function ConnectionBanner() {
  const { state } = useStore()
  const { status, playerId } = state.session
  if (!playerId || (status !== 'reconnecting' && status !== 'disconnected')) return null
  return (
    <div className="conn-banner" role="status">
      <span className="conn-banner-spinner" />
      连接中断，正在重连… 对局保留 30 秒
    </div>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <StoreProvider>
        <div className="app">
          <Routes>
            <Route path="/" element={<Lobby />} />
            <Route path="/game" element={<Game />} />
            <Route path="/replay/:id" element={<Replay />} />
            <Route path="*" element={<Lobby />} />
          </Routes>
          <Redirector />
          <ErrorToast />
          <ConnectionBanner />
        </div>
      </StoreProvider>
    </BrowserRouter>
  )
}
