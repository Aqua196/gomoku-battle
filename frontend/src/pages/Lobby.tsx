import { useEffect, useState } from 'react'
import { Icon } from '../components/Icon'
import { TopBar } from '../components/TopBar'
import { useNow } from '../hooks/useNow'
import { DIFFICULTIES } from '../state/ai'
import { useStore } from '../state/store'
import type { Color, Difficulty, Room, RoomPlayer } from '../ws/protocol'
import { Gate } from './Gate'

const DIFFICULTY_DESC: Record<Difficulty, string> = {
  easy: '入门 · 0.4s',
  normal: '进阶 · 1.2s',
  hard: '大师 · 1.8s',
}

const STATUS_LABEL: Record<Room['status'], string> = {
  waiting: '等待中',
  playing: '对局中',
  finished: '已结束',
}

function greeting(hour: number): string {
  if (hour < 5) return '夜深了'
  if (hour < 11) return '早上好'
  if (hour < 13) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
}

function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

export function Lobby() {
  const { state } = useStore()
  return state.session.playerId === null ? <Gate /> : <LobbyHome />
}

function LobbyHome() {
  const { state, api } = useStore()
  const { session, lobby } = state
  const [roomName, setRoomName] = useState('')
  const [matchSince, setMatchSince] = useState<number | null>(null)
  const now = useNow(1000)

  // Pull the room list on entry and keep it fresh; the server also pushes
  // room_list to every lobby player on change (§2).
  useEffect(() => {
    api.listRooms()
    const t = setInterval(() => api.listRooms(), 5000)
    return () => clearInterval(t)
  }, [api])

  useEffect(() => {
    setMatchSince(lobby.matchmaking ? Date.now() : null)
  }, [lobby.matchmaking])

  const myRoom = lobby.rooms.find((r) => r.id === lobby.myRoomId) ?? null
  const busy = !!myRoom || lobby.matchmaking
  const rooms = [...lobby.rooms].sort(
    (a, b) => Number(b.id === lobby.myRoomId) - Number(a.id === lobby.myRoomId) ||
      Number(b.status === 'waiting') - Number(a.status === 'waiting') ||
      b.createdAt - a.createdAt,
  )
  const openRooms = lobby.rooms.filter((r) => r.status === 'waiting' && r.id !== lobby.myRoomId).length

  return (
    <div className="page lobby-page">
      <TopBar />
      <main className="lobby">
        <section className="lobby-hero">
          <h1>
            {greeting(new Date(now).getHours())}，<span className="lobby-nick">{session.nick}</span>
          </h1>
          <p>
            {openRooms > 0 ? `有 ${openRooms} 个房间正在等人，` : ''}选择一种方式开始对局。
          </p>
        </section>

        {myRoom && (
          <section className="waiting-card">
            <div className="waiting-seats" aria-hidden>
              <i className="avatar-stone black" />
              <span className="waiting-dots">
                <i />
                <i />
                <i />
              </span>
              <i className="avatar-stone empty" />
            </div>
            <div className="waiting-copy">
              <h3>{myRoom.name}</h3>
              <p>等待对手加入…（你执黑先行）</p>
            </div>
            <button className="btn ghost" onClick={() => api.leaveRoom()}>
              <Icon name="door" />
              离开房间
            </button>
          </section>
        )}

        <section className="modes">
          <article className={`mode mode-match${lobby.matchmaking ? ' searching' : ''}`}>
            <div className="radar" aria-hidden>
              <i />
              <i />
              <i />
              <span className="radar-core">
                <Icon name="bolt" size={22} />
              </span>
            </div>
            <h3>快速匹配</h3>
            <p>与在线玩家实时配对，配对成功自动开局。</p>
            {lobby.matchmaking ? (
              <div className="match-live">
                <div className="match-status" aria-live="polite">
                  <span>
                    正在寻找对手
                    {lobby.queuePosition ? ` · 队列第 ${lobby.queuePosition} 位` : '…'}
                  </span>
                  <b>{formatElapsed(matchSince ? now - matchSince : 0)}</b>
                </div>
                <button className="btn ghost wide" onClick={() => api.matchmakeCancel()}>
                  取消匹配
                </button>
              </div>
            ) : (
              <button className="btn primary lg wide" onClick={() => api.matchmake()} disabled={!!myRoom}>
                <Icon name="bolt" />
                开始匹配
              </button>
            )}
          </article>

          <article className="mode mode-ai">
            <div className="mode-icon" aria-hidden>
              <Icon name="bot" size={22} />
            </div>
            <h3>人机对战</h3>
            <p>挑战 α-β 剪枝搜索引擎，你执黑先行。</p>
            <div className="difficulty">
              {DIFFICULTIES.map((d, i) => (
                <button
                  key={d.id}
                  aria-label={d.label}
                  className={`diff diff-${d.id}`}
                  onClick={() => api.playAi(d.id)}
                  disabled={busy}
                >
                  <span className="diff-bars" aria-hidden>
                    {[0, 1, 2].map((b) => (
                      <i key={b} className={b <= i ? 'on' : ''} />
                    ))}
                  </span>
                  <span className="diff-name">{d.label}</span>
                  <span className="diff-desc">{DIFFICULTY_DESC[d.id]}</span>
                </button>
              ))}
            </div>
          </article>

          <article className="mode mode-room">
            <div className="mode-icon" aria-hidden>
              <Icon name="plus" size={22} />
            </div>
            <h3>创建房间</h3>
            <p>开一桌等朋友从房间列表加入，创建者执黑。</p>
            <form
              className="room-form"
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
                disabled={busy}
              />
              <button className="btn" type="submit" disabled={busy}>
                创建房间
              </button>
            </form>
          </article>
        </section>

        <section className="rooms-section">
          <div className="section-head">
            <h2>
              房间
              <span className="count">{lobby.rooms.length}</span>
            </h2>
            <button className="icon-btn" aria-label="刷新房间列表" title="刷新" onClick={() => api.listRooms()}>
              <Icon name="refresh" />
            </button>
          </div>
          {rooms.length === 0 ? (
            <div className="empty-rooms">
              <svg viewBox="0 0 120 80" aria-hidden>
                <g className="empty-grid">
                  {[0, 1, 2, 3, 4].map((i) => (
                    <g key={i}>
                      <line x1={10} y1={10 + i * 15} x2={110} y2={10 + i * 15} />
                      <line x1={10 + i * 25} y1={10} x2={10 + i * 25} y2={70} />
                    </g>
                  ))}
                </g>
                <circle cx={60} cy={40} r={7} className="empty-black" />
                <circle cx={85} cy={25} r={7} className="empty-white" />
              </svg>
              <b>还没有房间</b>
              <span>创建一个，或者直接快速匹配。</span>
            </div>
          ) : (
            <ul className="room-grid">
              {rooms.map((room) => (
                <RoomCard
                  key={room.id}
                  room={room}
                  mine={room.id === lobby.myRoomId}
                  canJoin={!busy && room.status === 'waiting'}
                  onJoin={() => api.joinRoom(room.id)}
                />
              ))}
            </ul>
          )}
        </section>
      </main>
    </div>
  )
}

function Seat({ color, player }: { color: Color; player: RoomPlayer | undefined }) {
  return (
    <div className={`seat${player ? '' : ' open'}`}>
      <i className={`avatar-stone ${player ? color : 'empty'}`} />
      <span className="seat-nick">{player ? player.nick : '空位'}</span>
    </div>
  )
}

function RoomCard({
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
  const black = room.players.find((p) => p.color === 'black')
  const white = room.players.find((p) => p.color === 'white')
  return (
    <li className={`room-card ${room.status}${mine ? ' mine' : ''}`}>
      <div className="room-top">
        <span className="room-name" title={room.name}>
          {room.name}
        </span>
        <span className={`status-chip ${room.status}`}>
          <i />
          {STATUS_LABEL[room.status]}
        </span>
      </div>
      <div className="room-seats">
        <Seat color="black" player={black} />
        <span className="room-vs">VS</span>
        <Seat color="white" player={white} />
      </div>
      <div className="room-foot">
        <span className="room-tags">
          {mine && <em className="tag you">我的房间</em>}
          {room.isAI && <em className="tag ai">AI</em>}
        </span>
        {canJoin && (
          <button className="btn primary sm" onClick={onJoin}>
            加入对局
          </button>
        )}
      </div>
    </li>
  )
}
