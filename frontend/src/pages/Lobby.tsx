import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Icon } from '../components/Icon'
import { InkScape } from '../components/InkScape'
import { InkStone } from '../components/InkStone'
import { Enso, Roll } from '../components/Motion'
import { Rail } from '../components/Rail'
import { useNow } from '../hooks/useNow'
import { usePrefs } from '../hooks/usePrefs'
import { pointSeed } from '../lib/ink'
import { setPref } from '../lib/prefs'
import type { AiSide } from '../lib/prefs'
import { fetchGames, resultFor, shortDate, verdict } from '../lib/records'
import { DIFFICULTIES } from '../state/ai'
import { useStore } from '../state/useStore'
import type { Difficulty, GameRecord, Room, RoomPlayer } from '../ws/protocol'
import { Gate } from './Gate'

const DIFFICULTY_META: Record<Difficulty, { en: string; think: string }> = {
  easy: { en: 'gentle', think: '0.4s' },
  normal: { en: 'steady', think: '1.2s' },
  hard: { en: 'severe', think: '1.8s' },
}

const SIDES: { id: AiSide; label: string; desc: string }[] = [
  { id: 'black', label: '执黑', desc: '你执黑先行。' },
  { id: 'white', label: '执白', desc: 'AI 执黑先行，你后手应对。' },
  { id: 'random', label: '猜先', desc: '猜先定先后，各凭天意。' },
]

const STATUS_LABEL: Record<Room['status'], string> = {
  waiting: '等待中',
  playing: '对局中',
  finished: '已结束',
}

const NUMERALS = ['〇', '一', '二', '三', '四', '五', '六', '七', '八', '九']
const BANNER_SUN = { x: 0.82, y: 0.36, r: 0.2 }

const pad = (n: number, w = 2) => String(n).padStart(w, '0')
const d = (s: number) => ({ ['--d' as string]: `${s}s` })

function greeting(hour: number): string {
  if (hour < 5) return '夜深了'
  if (hour < 11) return '早上好'
  if (hour < 13) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
}

function cnNumber(n: number): string {
  if (n <= 10) return n === 10 ? '十' : NUMERALS[n]
  if (n < 20) return `十${NUMERALS[n % 10]}`
  const tens = Math.floor(n / 10)
  const ones = n % 10
  if (tens === 2 && ones) return `廿${NUMERALS[ones]}`
  if (tens === 3 && ones) return `卅${NUMERALS[ones]}`
  return `${NUMERALS[tens]}十${ones ? NUMERALS[ones] : ''}`
}

/** 二〇二六年十月廿二日 */
function cnDate(ts: number): string {
  const dt = new Date(ts)
  const year = String(dt.getFullYear())
    .split('')
    .map((c) => NUMERALS[Number(c)])
    .join('')
  return `${year}年${cnNumber(dt.getMonth() + 1)}月${cnNumber(dt.getDate())}日`
}

function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${pad(Math.floor(s / 60))}:${pad(s % 60)}`
}

export function Lobby() {
  const { state } = useStore()
  return state.session.playerId === null ? <Gate /> : <LobbyHome />
}

function LobbyHome() {
  const { state, api } = useStore()
  const { session, lobby } = state
  const { aiSide } = usePrefs()
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
        <header className="lobby-head">
          <InkScape className="head-scape" seed={19} sun={BANNER_SUN} lift={0.06} />
          <div className="lobby-title">
            <p className="kicker reveal">
              <span>No.02</span>
              <span className="latin">the lobby</span>
            </p>
            <h1 className="serif reveal" style={d(0.08)}>
              大厅
            </h1>
            <p className="lobby-greet reveal" style={d(0.18)}>
              <span className="serif">{greeting(new Date(now).getHours())}，</span>
              <b>{session.nick}</b>
            </p>
          </div>
          <p className="lobby-date serif reveal" style={d(0.3)} aria-hidden>
            {cnDate(now)}
          </p>
        </header>

        {myRoom && (
          <section className="waiting-strip">
            <Enso size={44} />
            <div className="waiting-copy">
              <h3>{myRoom.name}</h3>
              <p className="serif">虚席以待，你执黑先行</p>
            </div>
            <button className="btn" onClick={() => api.leaveRoom()}>
              <span>离开房间</span>
            </button>
          </section>
        )}

        <section className="modes">
          <article className={`mode mode-match${lobby.matchmaking ? ' searching' : ''}`} style={d(0.2)}>
            <span className="mode-num serif" aria-hidden>
              壹
            </span>
            <h2 className="serif">快速匹配</h2>
            <p className="mode-en latin">quick match</p>
            <p className="mode-desc">与在线玩家实时配对，配对成功自动开局。</p>
            {lobby.matchmaking ? (
              <div className="match-live">
                <div className="match-row" aria-live="polite">
                  <Enso size={40} />
                  <span className="serif">
                    寻找对手{lobby.queuePosition ? ` · 第 ${lobby.queuePosition} 位` : '…'}
                  </span>
                  <b className="latin">
                    <Roll value={formatElapsed(matchSince ? now - matchSince : 0)} />
                  </b>
                </div>
                <button className="btn block" onClick={() => api.matchmakeCancel()}>
                  <span>取消匹配</span>
                  <small aria-hidden>cancel</small>
                </button>
              </div>
            ) : (
              <button className="btn primary lg block" onClick={() => api.matchmake()} disabled={!!myRoom}>
                <span>开始匹配</span>
                <small aria-hidden>begin</small>
              </button>
            )}
          </article>

          <article className="mode mode-ai" style={d(0.3)}>
            <span className="mode-num serif" aria-hidden>
              贰
            </span>
            <h2 className="serif">人机对战</h2>
            <p className="mode-en latin">versus the machine</p>
            <p className="mode-desc">
              挑战 α-β 剪枝搜索引擎，{SIDES.find((s) => s.id === aiSide)?.desc}
            </p>
            <div className="side-pick" role="radiogroup" aria-label="执子">
              {SIDES.map((s) => (
                <button
                  key={s.id}
                  role="radio"
                  aria-checked={aiSide === s.id}
                  className={`side-opt${aiSide === s.id ? ' on' : ''}`}
                  onClick={() => setPref('aiSide', s.id)}
                >
                  <SideMark side={s.id} />
                  <span className="serif">{s.label}</span>
                </button>
              ))}
            </div>
            <ul className="diff-list">
              {DIFFICULTIES.map((dd, i) => (
                <li key={dd.id}>
                  <button aria-label={dd.label} className="diff-row" onClick={() => api.playAi(dd.id, aiSide)} disabled={busy}>
                    <span className="diff-name serif">{dd.label}</span>
                    <span className="diff-en latin">{DIFFICULTY_META[dd.id].en}</span>
                    <span className="diff-dots" aria-hidden>
                      {[0, 1, 2].map((b) => (
                        <i key={b} className={b <= i ? 'on' : ''} />
                      ))}
                    </span>
                    <span className="diff-time latin">{DIFFICULTY_META[dd.id].think}</span>
                  </button>
                </li>
              ))}
            </ul>
          </article>

          <article className="mode mode-room" style={d(0.4)}>
            <span className="mode-num serif" aria-hidden>
              叁
            </span>
            <h2 className="serif">创建房间</h2>
            <p className="mode-en latin">a table of your own</p>
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
              <button className="btn" type="submit" disabled={busy}>
                <span>创建房间</span>
              </button>
            </form>
          </article>
        </section>

        <section className="rooms">
          <header className="sub-head">
            <h2 className="serif">房间列表</h2>
            <span className="latin">tables</span>
            <span className="sub-count latin">
              <Roll value={lobby.rooms.length} />
            </span>
            <button className="icon-btn" aria-label="刷新房间列表" title="刷新" onClick={() => api.listRooms()}>
              <Icon name="refresh" />
            </button>
          </header>

          {rooms.length === 0 ? (
            <div className="empty">
              <Enso size={64} />
              <p className="serif">此处尚无棋局</p>
              <span>创建一个，或者直接快速匹配。</span>
            </div>
          ) : (
            <ul className="room-table">
              {rooms.map((room, i) => (
                <RoomRow
                  key={room.id}
                  index={i + 1}
                  room={room}
                  mine={room.id === lobby.myRoomId}
                  canJoin={!busy && room.status === 'waiting'}
                  canWatch={!myRoom && room.status === 'playing'}
                  onJoin={() => api.joinRoom(room.id)}
                  onWatch={() => api.spectate(room.id)}
                />
              ))}
            </ul>
          )}
        </section>

        <Records playerId={session.playerId} />
      </main>
    </div>
  )
}

type RecordScope = 'mine' | 'recent'

/** 棋谱: my finished games or everyone's, each opening its replay. */
function Records({ playerId }: { playerId: string | null }) {
  const navigate = useNavigate()
  const [scope, setScope] = useState<RecordScope>('mine')
  const [games, setGames] = useState<GameRecord[] | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!playerId) return
    const ctl = new AbortController()
    const load = () =>
      fetchGames(scope === 'mine' ? { playerId } : 'recent', 12, ctl.signal)
        .then((gs) => {
          setGames(gs)
          setFailed(false)
        })
        .catch(() => {
          if (!ctl.signal.aborted) setFailed(true)
        })
    setGames(null)
    void load()
    const t = setInterval(load, 30_000)
    return () => {
      ctl.abort()
      clearInterval(t)
    }
  }, [playerId, scope])

  return (
    <section className="records">
      <header className="sub-head">
        <h2 className="serif">棋谱</h2>
        <span className="latin">records</span>
        <div className="seg" role="tablist" aria-label="棋谱范围">
          {(
            [
              ['mine', '我的对局'],
              ['recent', '最近对局'],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              role="tab"
              aria-selected={scope === id}
              className={`seg-opt serif${scope === id ? ' on' : ''}`}
              onClick={() => setScope(id)}
            >
              {label}
            </button>
          ))}
        </div>
      </header>
      {games === null ? (
        <p className="records-note serif">{failed ? '棋谱暂时取不到' : '展卷中…'}</p>
      ) : games.length === 0 ? (
        <p className="records-note serif">{scope === 'mine' ? '下完一局，棋谱就会留在这里。' : '还没有人下完一局。'}</p>
      ) : (
        <ul className="record-list">
          {games.map((g) => {
            const res = resultFor(g, playerId)
            return (
              <li key={g.id}>
                <button
                  type="button"
                  className={`record-row${res ? ` ${res}` : ''}`}
                  onClick={() => navigate(`/replay/${g.id}`)}
                  aria-label={`复盘 ${g.black.nick} 对 ${g.white.nick}`}
                >
                  <span className="rec-when latin">{shortDate(g.endedAt)}</span>
                  <span className="rec-players">
                    <span className={g.winner === 'black' ? 'won' : ''}>{g.black.nick}</span>
                    <i className="serif">对</i>
                    <span className={g.winner === 'white' ? 'won' : ''}>{g.white.nick}</span>
                  </span>
                  <span className="rec-verdict serif">{verdict(g)}</span>
                  <span className="rec-moves latin">{g.moveCount} 手</span>
                  {res && (
                    <span className={`rec-mine serif ${res}`}>{res === 'win' ? '胜' : res === 'lose' ? '负' : '和'}</span>
                  )}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

/** A stone for 执黑 / 执白; 猜先 shows the two halves of a fist of stones. */
function SideMark({ side }: { side: AiSide }) {
  if (side !== 'random') {
    return (
      <span className="chip">
        <InkStone color={side} seed={side === 'black' ? 11 : 12} />
      </span>
    )
  }
  return (
    <span className="chip pair" aria-hidden>
      <InkStone color="black" seed={13} />
      <InkStone color="white" seed={14} />
    </span>
  )
}

function Seat({ player, color, index }: { player: RoomPlayer | undefined; color: 'black' | 'white'; index: number }) {
  return (
    <span className={`rr-seat${player ? '' : ' open'}`}>
      {player ? (
        <span className="chip">
          <InkStone color={color} seed={pointSeed(index, color === 'black' ? 1 : 2, 4)} />
        </span>
      ) : (
        <i className="chip none" />
      )}
      <span className="rr-nick">{player ? player.nick : '空位'}</span>
    </span>
  )
}

function RoomRow({
  index,
  room,
  mine,
  canJoin,
  canWatch,
  onJoin,
  onWatch,
}: {
  index: number
  room: Room
  mine: boolean
  canJoin: boolean
  canWatch: boolean
  onJoin: () => void
  onWatch: () => void
}) {
  const viewers = room.spectators ?? 0
  return (
    <li className={`room-row ${room.status}${mine ? ' mine' : ''}`}>
      <span className="rr-no serif">{cnNumber(index)}</span>
      <span className="rr-name">
        <span className="rr-title" title={room.name}>
          {room.name}
        </span>
        {mine && <em className="tag you">我的房间</em>}
        {room.isAI && <em className="tag ai">AI</em>}
      </span>
      <Seat color="black" index={index} player={room.players.find((p) => p.color === 'black')} />
      <Seat color="white" index={index} player={room.players.find((p) => p.color === 'white')} />
      <span className={`rr-status serif ${room.status}`}>
        {STATUS_LABEL[room.status]}
        {viewers > 0 && <small className="rr-viewers latin">{viewers} 观</small>}
      </span>
      <span className="rr-action">
        {canJoin && (
          <button className="btn sm primary" onClick={onJoin}>
            <span>入座</span>
          </button>
        )}
        {canWatch && (
          <button className="btn sm" onClick={onWatch} aria-label={`观战 ${room.name}`}>
            <Icon name="eye" size={14} />
            <span>观战</span>
          </button>
        )}
      </span>
    </li>
  )
}
