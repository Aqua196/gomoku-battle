import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ConnBadge } from '../components/ConnBadge'
import { useNow } from '../hooks/useNow'
import { useStore } from '../state/store'
import { aiBadgeLabel, aiOpponent, isAiThinking } from '../state/ai'
import { BOARD_SIZE, MOVE_TIMEOUT_MS, RECONNECT_WINDOW_MS } from '../ws/protocol'
import type { Color, GameOverReason, GamePlayer } from '../ws/protocol'

const STARS = new Set(['3,3', '3,11', '7,7', '11,3', '11,11'])

const REASON_LABEL: Record<GameOverReason, string> = {
  five: '五连获胜',
  draw: '满盘平局',
  timeout: '回合超时判负',
  resign: '认输',
  disconnect_timeout: '断线超时判负',
}

export function Game() {
  const { state, api } = useStore()
  const { game: gv, session } = state
  const navigate = useNavigate()
  // Server-aligned clock for server deadlines; local clock for local stamps.
  const localNow = useNow(200)
  const now = localNow + (session.clockOffsetMs ?? 0)
  const [confirmResign, setConfirmResign] = useState(false)

  const game = gv.game
  if (!game) {
    return (
      <div className="page">
        <section className="card gate">
          <p className="muted">当前没有进行中的对局。</p>
          <button className="btn primary" onClick={() => navigate('/')}>
            返回大厅
          </button>
        </section>
      </div>
    )
  }

  const me: GamePlayer | null = gv.youColor ? game.players[gv.youColor] : null
  const oppColor: Color | null = gv.youColor ? (gv.youColor === 'black' ? 'white' : 'black') : null
  const opp: GamePlayer | null = oppColor ? game.players[oppColor] : null
  const oppOnline = gv.opponentConnected && (opp?.connected ?? true)

  // M2: AI seat + 「人机 · 难度」badge + 「AI 思考中」derivation.
  const aiOpp = aiOpponent(game)
  const aiBadge = aiBadgeLabel(aiOpp)
  const aiThinking = isAiThinking(game, aiOpp, gv.youColor, gv.over !== null)

  const playing = game.status === 'playing'
  const myTurn = playing && !gv.over && game.turn === gv.youColor
  const turnLeft = game.turnDeadline ? Math.max(0, game.turnDeadline - now) : null

  // Forfeit countdown while the opponent is offline (presence msg wins,
  // game_state.reconnectDeadline as fallback).
  const oppDeadline = !oppOnline ? (gv.opponentDeadline ?? game.reconnectDeadline) : null
  const oppLeft = oppDeadline ? Math.max(0, oppDeadline - now) : null
  const showReconnected =
    gv.opponentReconnectedAt !== null && localNow - gv.opponentReconnectedAt < 3000

  const backToLobby = () => {
    api.clearGame()
    navigate('/')
  }

  return (
    <div className="page game">
      <section className="game-head">
        <div className="row spread">
          <h2>
            {game.roomId}
            {aiBadge && <em className="tag ai">{aiBadge}</em>}
            <span className="muted small"> · 第 {game.moveNumber} 手</span>
          </h2>
          <ConnBadge status={session.status} latencyMs={session.latencyMs} />
        </div>

        <div className="players">
          <PlayerCard
            color="black"
            player={game.players.black}
            isYou={gv.youColor === 'black'}
            isTurn={playing && game.turn === 'black'}
            online={oppColor === 'black' ? oppOnline : game.players.black.connected}
          />
          <span className="vs">VS</span>
          <PlayerCard
            color="white"
            player={game.players.white}
            isYou={gv.youColor === 'white'}
            isTurn={playing && game.turn === 'white'}
            online={oppColor === 'white' ? oppOnline : game.players.white.connected}
          />
        </div>

        {playing && (
          <div className={`turn-line ${myTurn ? 'mine' : ''}`}>
            {myTurn ? '你的回合' : aiThinking ? 'AI 思考中' : '对手思考中'}
            {aiThinking && (
              <span className="dots" aria-hidden>
                <i />
              </span>
            )}
            {turnLeft !== null && <b>{Math.ceil(turnLeft / 1000)}s</b>}
          </div>
        )}
        {turnLeft !== null && playing && (
          <div className="bar">
            <div
              className={`bar-fill turn ${myTurn ? 'mine' : ''}`}
              style={{ width: `${(turnLeft / MOVE_TIMEOUT_MS) * 100}%` }}
            />
          </div>
        )}

        {!oppOnline && (
          <div className="banner danger">
            对手断线{oppLeft !== null && <>，{Math.ceil(oppLeft / 1000)}s 内不回判负</>}
            {oppLeft !== null && (
              <div className="bar slim">
                <div
                  className="bar-fill danger"
                  style={{ width: `${(oppLeft / RECONNECT_WINDOW_MS) * 100}%` }}
                />
              </div>
            )}
          </div>
        )}
        {showReconnected && <div className="banner ok">对手已重连</div>}
      </section>

      <div className="board-wrap">
        <div
          className="board"
          role="grid"
          aria-label="棋盘"
          style={{ ['--n' as string]: BOARD_SIZE }}
        >
          {game.board.flatMap((row, y) =>
            row.map((cell, x) => {
              const last = game.lastMove?.x === x && game.lastMove?.y === y
              const playable =
                cell === 0 && myTurn && session.status === 'connected'
              const cls = [
                'cell',
                x === 0 && 'e-l',
                x === BOARD_SIZE - 1 && 'e-r',
                y === 0 && 'e-t',
                y === BOARD_SIZE - 1 && 'e-b',
                STARS.has(`${x},${y}`) && 'star',
                playable && 'playable',
                last && 'last',
              ]
                .filter(Boolean)
                .join(' ')
              return (
                <button
                  key={`${x},${y}`}
                  className={cls}
                  role="gridcell"
                  aria-label={`(${x},${y})`}
                  disabled={!playable}
                  onClick={() => api.move(x, y)}
                >
                  {cell !== 0 && (
                    <i className={`stone ${cell === 1 ? 'black' : 'white'}`}>
                      {last && <b className="ring" />}
                    </i>
                  )}
                  {playable && <i className={`ghost ${gv.youColor ?? ''}`} />}
                  {STARS.has(`${x},${y}`) && <i className="star-dot" />}
                </button>
              )
            }),
          )}
        </div>
      </div>

      <section className="game-foot">
        <span className="muted small">
          你执{gv.youColor === 'black' ? '黑' : gv.youColor === 'white' ? '白' : '—'}
          {me ? ` · ${me.nick}` : ''}
          {opp ? ` · 对手 ${opp.nick}${opp.isAI ? ' (AI)' : ''}` : ''}
        </span>
        {playing && !gv.over && (
          <button
            className={`btn ${confirmResign ? 'warn' : 'ghost'}`}
            onClick={() => {
              if (confirmResign) {
                api.resign()
                setConfirmResign(false)
              } else {
                setConfirmResign(true)
                setTimeout(() => setConfirmResign(false), 3000)
              }
            }}
          >
            {confirmResign ? '确认认输？' : '认输'}
          </button>
        )}
      </section>

      {gv.over && (
        <div className="overlay">
          <div className="card over-card">
            <h2 className={`over-title ${gv.over.winner === null ? 'draw' : gv.over.winner === gv.youColor ? 'win' : 'lose'}`}>
              {gv.over.winner === null
                ? '平局'
                : gv.over.winner === gv.youColor
                  ? '你赢了！'
                  : '对手获胜'}
            </h2>
            <p className="muted">{REASON_LABEL[gv.over.reason]}</p>
            <button className="btn primary" onClick={backToLobby} autoFocus>
              返回大厅
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function PlayerCard({
  color,
  player,
  isYou,
  isTurn,
  online = true,
}: {
  color: Color
  player: GamePlayer
  isYou: boolean
  isTurn: boolean
  online?: boolean
}) {
  return (
    <div className={`p-card ${color} ${isTurn ? 'turn' : ''}`}>
      <i className={`stone-sm ${color}`} />
      <div className="p-info">
        <span className="p-nick">
          {player.nick}
          {isYou && <em className="tag">你</em>}
          {player.isAI && <em className="tag ai">AI</em>}
        </span>
        <span className={`p-conn ${online ? 'on' : 'off'}`}>{online ? '在线' : '断线'}</span>
      </div>
    </div>
  )
}
