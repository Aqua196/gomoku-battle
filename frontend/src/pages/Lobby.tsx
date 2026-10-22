import { useEffect, useState } from 'react'
import { Icon } from '../components/Icon'
import { Rail } from '../components/Rail'
import { useNow } from '../hooks/useNow'
import { DIFFICULTIES } from '../state/ai'
import { useStore } from '../state/store'
import type { Difficulty, Room, RoomPlayer } from '../ws/protocol'
import { Gate } from './Gate'

const DIFFICULTY_META: Record<Difficulty, { en: string; think: string }> = {
  easy: { en: 'Easy', think: '0.4s' },
  normal: { en: 'Normal', think: '1.2s' },
  hard: { en: 'Hard', think: '1.8s' },
}

const STATUS_LABEL: Record<Room['status'], string> = {
  waiting: '等待中',
  playing: '对局中',
  finished: '已结束',
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0')

function greeting(hour: number): string {
  if (hour < 5) return '夜深了'
  if (hour < 11) return '早上好'
  if (hour < 13) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
}

function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${pad(Math.floor(s / 60))}:${pad(s % 60)}`
}

function stamp(ts: number): string {
  const d = new Date(ts)
  return `${d.getFullYear()} // ${pad(d.getMonth() + 1)} / ${pad(d.getDate())}`
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
    (a, b) =>
      Number(b.id === lobby.myRoomId) - Number(a.id === lobby.myRoomId) ||
      Number(b.status === 'waiting') - Number(a.status === 'waiting') ||
      b.createdAt - a.createdAt,
  )

  return (
    <div className="app-shell">
      <Rail />
      <main className="stage lobby">
        <header className="sec-head">
          <span className="sec-no mono">01</span>
          <div className="sec-title wipe">
            <h1>大厅</h1>
            <p className="en">Lobby</p>
          </div>
          <div className="sec-meta">
            <span>
              {greeting(new Date(now).getHours())}，<b>{session.nick}</b>
            </span>
            <span className="mono">{stamp(now)}</span>
          </div>
        </header>

        {myRoom && (
          <section className="waiting-strip">
            <span className="waiting-tag en">Waiting</span>
            <div className="waiting-copy">
              <h3>{myRoom.name}</h3>
              <p>等待对手加入…（你执黑先行）</p>
            </div>
            <span className="waiting-run stripes" aria-hidden />
            <button className="btn" onClick={() => api.leaveRoom()}>
              <span>离开房间</span>
              <small className="en" aria-hidden>
                Leave
              </small>
            </button>
          </section>
        )}

        <section className="modes">
          <article className={`mode mode-match${lobby.matchmaking ? ' searching' : ''}`}>
            <div className="mode-top">
              <span className="mono">A-01</span>
              <span className="en">Quick Match</span>
            </div>
            <h2>快速匹配</h2>
            <p className="mode-desc">与在线玩家实时配对，配对成功自动开局。</p>
            {lobby.matchmaking ? (
              <div className="match-live">
                <span className="match-run stripes" aria-hidden />
                <div className="match-row" aria-live="polite">
                  <span className="mono">
                    SEARCHING{lobby.queuePosition ? ` · QUEUE ${pad(lobby.queuePosition)}` : '…'}
                  </span>
                  <b className="mono">{formatElapsed(matchSince ? now - matchSince : 0)}</b>
                </div>
                <button className="btn block" onClick={() => api.matchmakeCancel()}>
                  <span>取消匹配</span>
                  <small className="en" aria-hidden>
                    Cancel
                  </small>
                </button>
              </div>
            ) : (
              <button className="btn primary lg block" onClick={() => api.matchmake()} disabled={!!myRoom}>
                <span>
                  <i className="btn-mark" />
                  开始匹配
                </span>
                <small className="en" aria-hidden>
                  Start
                </small>
              </button>
            )}
          </article>

          <article className="mode mode-ai">
            <div className="mode-top">
              <span className="mono">A-02</span>
              <span className="en">Versus AI</span>
            </div>
            <h2>人机对战</h2>
            <p className="mode-desc">挑战 α-β 剪枝搜索引擎，你执黑先行。</p>
            <ul className="diff-list">
              {DIFFICULTIES.map((d, i) => (
                <li key={d.id}>
                  <button aria-label={d.label} className="diff-row" onClick={() => api.playAi(d.id)} disabled={busy}>
                    <span className="mono diff-no">{pad(i + 1)}</span>
                    <span className="diff-name">{d.label}</span>
                    <span className="en diff-en">{DIFFICULTY_META[d.id].en}</span>
                    <span className="diff-bars" aria-hidden>
                      {[0, 1, 2].map((b) => (
                        <i key={b} className={b <= i ? 'on' : ''} />
                      ))}
                    </span>
                    <span className="mono diff-time">{DIFFICULTY_META[d.id].think}</span>
                  </button>
                </li>
              ))}
            </ul>
          </article>

          <article className="mode mode-room">
            <div className="mode-top">
              <span className="mono">A-03</span>
              <span className="en">Custom Room</span>
            </div>
            <h2>创建房间</h2>
            <p className="mode-desc">开一桌等朋友从房间列表加入，创建者执黑。</p>
            <form
              className="room-form"
              onSubmit={(e) => {
                e.preventDefault()
                api.createRoom(roomName.trim() || undefined)
                setRoomName('')
              }}
            >
              <input
                className="field-input sm"
                value={roomName}
                onChange={(e) => setRoomName(e.target.value)}
                placeholder="房间名（可选）"
                maxLength={64}
                disabled={busy}
              />
              <button className="btn primary" type="submit" disabled={busy}>
                <span>创建房间</span>
              </button>
            </form>
          </article>
        </section>

        <section className="rooms">
          <header className="sub-head">
            <span className="sec-no sm mono">02</span>
            <h2>房间列表</h2>
            <span className="en">Rooms</span>
            <span className="mono sub-count">[ {pad(lobby.rooms.length)} ]</span>
            <button className="icon-btn" aria-label="刷新房间列表" title="刷新" onClick={() => api.listRooms()}>
              <Icon name="refresh" />
            </button>
          </header>

          {rooms.length === 0 ? (
            <div className="empty bracketed">
              <span className="mono">NO ACTIVE ROOMS // 暂无房间</span>
              <p>创建一个，或者直接快速匹配。</p>
            </div>
          ) : (
            <ul className="room-table">
              <li className="room-row head mono" aria-hidden>
                <span>NO.</span>
                <span>ROOM</span>
                <span>BLACK</span>
                <span>WHITE</span>
                <span>STATUS</span>
                <span />
              </li>
              {rooms.map((room, i) => (
                <RoomRow
                  key={room.id}
                  index={i + 1}
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

function Seat({ player, color }: { player: RoomPlayer | undefined; color: 'black' | 'white' }) {
  return (
    <span className={`rr-seat${player ? '' : ' open'}`}>
      <i className={`chip ${player ? color : 'none'}`} />
      <span className="rr-nick">{player ? player.nick : '空位'}</span>
    </span>
  )
}

function RoomRow({
  index,
  room,
  mine,
  canJoin,
  onJoin,
}: {
  index: number
  room: Room
  mine: boolean
  canJoin: boolean
  onJoin: () => void
}) {
  return (
    <li className={`room-row ${room.status}${mine ? ' mine' : ''}`}>
      <span className="mono rr-no">{pad(index)}</span>
      <span className="rr-name">
        <span className="rr-title" title={room.name}>
          {room.name}
        </span>
        {mine && <em className="tag you">我的房间</em>}
        {room.isAI && <em className="tag ai">AI</em>}
      </span>
      <Seat color="black" player={room.players.find((p) => p.color === 'black')} />
      <Seat color="white" player={room.players.find((p) => p.color === 'white')} />
      <span className="rr-status">
        <span className={`status ${room.status}`}>{STATUS_LABEL[room.status]}</span>
      </span>
      <span className="rr-action">
        {canJoin && (
          <button className="btn sm primary" onClick={onJoin}>
            <span>加入</span>
            <Icon name="arrowRight" size={14} />
          </button>
        )}
      </span>
    </li>
  )
}
