import { useEffect } from 'react'
import { BrowserRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { Game } from './pages/Game'
import { Lobby } from './pages/Lobby'
import { StoreProvider, useStore } from './state/store'

const ERROR_LABEL: Record<string, string> = {
  not_implemented: '该功能暂未实现',
  not_your_turn: '还没轮到你',
  illegal_move: '非法落子',
  already_in_room: '你已在房间中',
  not_in_room: '你不在房间中',
  room_full: '房间已满',
  room_not_found: '房间不存在',
  not_in_lobby: '当前不在大厅',
  game_over: '对局已结束',
  hello_timeout: '连接超时',
  unauthorized: '身份验证失败，正在重新连接',
  invalid_difficulty: '无效难度',
  bad_request: '请求格式错误',
  internal: '服务器内部错误',
  offline: '连接已断开，正在重连，请稍候再试',
}

function Redirector() {
  const { state } = useStore()
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const inGame = state.game.game !== null

  useEffect(() => {
    // game_start (or resumed game_state) → jump to the board.
    if (inGame && pathname !== '/game') navigate('/game')
  }, [inGame, pathname, navigate])

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
    <div className="toast" role="alert" onClick={() => api.dismissError()}>
      {ERROR_LABEL[err.code] ?? err.message}
      {err.code !== 'not_implemented' && <span className="muted small"> ({err.code})</span>}
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
            <Route path="*" element={<Lobby />} />
          </Routes>
          <Redirector />
          <ErrorToast />
        </div>
      </StoreProvider>
    </BrowserRouter>
  )
}
