import { useEffect, useState } from 'react'
import { useStore } from '../state/store'
import type { Difficulty, Room } from '../ws/protocol'
import { ConnBadge } from '../components/ConnBadge'

const DIFFICULTIES: { id: Difficulty; label: string }[] = [
  { id: 'easy', label: '简单' },
  { id: 'normal', label: '普通' },
  { id: 'hard', label: '困难' },
]

export function Lobby() {
  const { state, api } = useStore()
  const { session, lobby } = state
  const welcomed = session.playerId !== null
  const [nickInput, setNickInput] = useState(session.nick)
  const [roomName, setRoomName] = useState('')

  // Pull the room list on entry and keep it fresh; the server also pushes
  // room_list to every lobby player on change (§2).
  useEffect(() => {
    if (!welcomed) return
    api.listRooms()
    const t = setInterval(() => api.listRooms(), 5000)
    return () => clearInterval(t)
  }, [welcomed, api])

  if (!welcomed) {
    return (
      <div className="page lobby">
        <section className="card gate">
          <h1 className="gate-title">五子棋对战</h1>
          <p className="muted">输入昵称进入大厅，实时匹配对手。</p>
          <form
            className="gate-form"
            onSubmit={(e) => {
              e.preventDefault()
              api.connect(nickInput)
            }}
          >
            <input
              className="input"
              value={nickInput}
              onChange={(e) => setNickInput(e.target.value)}
              placeholder="昵称（1–32 字符）"
              maxLength={32}
              autoFocus
            />
            <button
              className="btn primary"
              type="submit"
              disabled={!nickInput.trim() || session.status === 'connecting' || session.status === 'reconnecting'}
            >
              {session.status === 'connecting' || session.status === 'reconnecting'
                ? '连接中…'
                : '进入大厅'}
            </button>
          </form>
          <ConnBadge status={session.status} latencyMs={session.latencyMs} />
        </section>
      </div>
    )
  }

  const myRoom = lobby.rooms.find((r) => r.id === lobby.myRoomId) ?? null

  return (
    <div className="page lobby">
      <section className="card">
        <div className="row spread">
          <h2>大厅</h2>
          <ConnBadge status={session.status} latencyMs={session.latencyMs} />
        </div>

        {myRoom && (
          <div className="myroom">
            <div>
              <div className="myroom-name">🏁 {myRoom.name}</div>
              <div className="muted small">等待对手加入…（你执黑）</div>
            </div>
            <button className="btn ghost" onClick={() => api.leaveRoom()}>
              离开房间
            </button>
          </div>
        )}

        <div className="actions">
          <form
            className="row"
            onSubmit={(e) => {
              e.preventDefault()
              api.createRoom(roomName.trim() || undefined)
              setRoomName('')
            }}
          >
            <input
              className="input"
              value={roomName}
              onChange={(e) => setRoomName(e.target.value)}
              placeholder="房间名（可选）"
              maxLength={64}
              disabled={!!myRoom || lobby.matchmaking}
            />
            <button className="btn" type="submit" disabled={!!myRoom || lobby.matchmaking}>
              创建房间
            </button>
          </form>

          <div className="row">
            {lobby.matchmaking ? (
              <>
                <span className="muted">匹配中{lobby.queuePosition ? `（队列 #${lobby.queuePosition}）` : '…'}</span>
                <button className="btn warn" onClick={() => api.matchmakeCancel()}>
                  取消匹配
                </button>
              </>
            ) : (
              <button className="btn primary" onClick={() => api.matchmake()} disabled={!!myRoom}>
                ⚡ 快速匹配
              </button>
            )}
          </div>

          <div className="row ai-row">
            <span className="muted">人机对战</span>
            {DIFFICULTIES.map((d) => (
              <button
                key={d.id}
                className="btn ghost"
                onClick={() => api.playAi(d.id)}
                disabled={!!myRoom || lobby.matchmaking}
              >
                {d.label}
              </button>
            ))}
          </div>
        </div>
      </section>

      <section className="card">
        <div className="row spread">
          <h2>房间列表</h2>
          <button className="btn ghost small" onClick={() => api.listRooms()}>
            刷新
          </button>
        </div>
        {lobby.rooms.length === 0 ? (
          <p className="muted">暂无房间 — 创建一个或快速匹配。</p>
        ) : (
          <ul className="rooms">
            {lobby.rooms.map((room) => (
              <RoomRow
                key={room.id}
                room={room}
                mine={room.id === lobby.myRoomId}
                canJoin={!myRoom && !lobby.matchmaking && room.status === 'waiting'}
                onJoin={() => api.joinRoom(room.id)}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function RoomRow({
  room,
  mine,
  canJoin,
  onJoin,
}: {
  room: Room
  mine: boolean
  canJoin: boolean
  onJoin: () => void
}) {
  const seated = room.players.length
  return (
    <li className="room">
      <div className="room-main">
        <span className="room-name">
          {room.name}
          {mine && <em className="tag">我的房间</em>}
          {room.isAI && <em className="tag ai">AI</em>}
        </span>
        <span className="muted small">
          {seated}/2 · {room.players.map((p) => p.nick).join(' vs ') || '—'}
        </span>
      </div>
      <div className="row">
        <span className={`badge ${room.status}`}>{statusLabel(room.status)}</span>
        {canJoin && (
          <button className="btn small" onClick={onJoin}>
            加入
          </button>
        )}
      </div>
    </li>
  )
}

function statusLabel(s: Room['status']): string {
  switch (s) {
    case 'waiting':
      return '等待中'
    case 'playing':
      return '对局中'
    case 'finished':
      return '已结束'
  }
}
