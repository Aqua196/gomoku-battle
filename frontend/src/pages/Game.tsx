import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Board } from '../components/Board'
import { Icon } from '../components/Icon'
import { PlayerPanel } from '../components/PlayerPanel'
import { ResultOverlay } from '../components/ResultOverlay'
import { TopBar } from '../components/TopBar'
import { useMoveOrder } from '../hooks/useMoveOrder'
import { useNow } from '../hooks/useNow'
import { usePrefs } from '../hooks/usePrefs'
import { setPref } from '../lib/prefs'
import { sfx } from '../lib/sound'
import { findWinLine } from '../lib/winLine'
import { aiBadgeLabel, aiDifficulty, aiOpponent, isAiThinking } from '../state/ai'
import { useStore } from '../state/store'
import { MOVE_TIMEOUT_MS, RECONNECT_WINDOW_MS } from '../ws/protocol'
import type { Board as Cells, Color, GameState } from '../ws/protocol'

function countStones(board: Cells, cell: 1 | 2): number {
  let n = 0
  for (const row of board) for (const c of row) if (c === cell) n++
  return n
}

export function Game() {
  const { state } = useStore()
  const navigate = useNavigate()
  const game = state.game.game
  if (!game) {
    return (
      <div className="page game-page">
        <TopBar showStats={false} />
        <main className="no-game">
          <p>当前没有进行中的对局。</p>
          <button className="btn primary" onClick={() => navigate('/')}>
            返回大厅
          </button>
        </main>
      </div>
    )
  }
  // Keyed by room: a new game starts with fresh local state (sounds, review).
  return <GameView key={game.roomId} game={game} />
}

function GameView({ game }: { game: GameState }) {
  const { state, api } = useStore()
  const { game: gv, session } = state
  const navigate = useNavigate()
  const prefs = usePrefs()
  const order = useMoveOrder(game)
  // Server-aligned clock for server deadlines; local clock for local stamps.
  const localNow = useNow(200)
  const now = localNow + (session.clockOffsetMs ?? 0)
  const [confirmResign, setConfirmResign] = useState(false)
  const [reviewing, setReviewing] = useState(false)

  const youColor = gv.youColor
  const oppColor: Color | null = youColor ? (youColor === 'black' ? 'white' : 'black') : null
  const opp = oppColor ? game.players[oppColor] : null
  const oppOnline = gv.opponentConnected && (opp?.connected ?? true)

  const aiOpp = aiOpponent(game)
  const aiBadge = aiBadgeLabel(aiOpp)
  const over = gv.over
  const aiThinking = isAiThinking(game, over !== null)
  const playing = game.status === 'playing' && !over
  const myTurn = playing && game.turn === youColor
  const turnLeft = playing && game.turnDeadline ? Math.max(0, game.turnDeadline - now) : null

  // Forfeit countdown while the opponent is offline (presence msg wins,
  // game_state.reconnectDeadline as fallback).
  const oppDeadline = !oppOnline ? (gv.opponentDeadline ?? game.reconnectDeadline) : null
  const oppLeft = oppDeadline ? Math.max(0, oppDeadline - now) : null
  const showReconnected = gv.opponentReconnectedAt !== null && localNow - gv.opponentReconnectedAt < 3000

  const winLine = over?.reason === 'five' ? findWinLine(game.board, game.lastMove) : null
  const outcome = over ? (over.winner === null ? 'draw' : over.winner === youColor ? 'win' : 'lose') : null

  /* ── sound cues ─────────────────────────────────────────────────── */
  const prevMoves = useRef(game.moveNumber)
  const started = useRef(false)
  useEffect(() => {
    if (!started.current && game.moveNumber === 0) sfx.start()
    started.current = true
    if (game.moveNumber > prevMoves.current) sfx.stone()
    prevMoves.current = game.moveNumber
  }, [game.moveNumber])

  const prevOutcome = useRef(outcome)
  useEffect(() => {
    if (outcome && !prevOutcome.current) {
      if (outcome === 'win') sfx.win()
      else sfx.lose()
    }
    prevOutcome.current = outcome
  }, [outcome])

  const mySecs = myTurn && turnLeft !== null ? Math.ceil(turnLeft / 1000) : null
  useEffect(() => {
    if (mySecs !== null && mySecs > 0 && mySecs <= 5) sfx.tick()
  }, [mySecs])

  useEffect(() => {
    if (!confirmResign) return
    const t = setTimeout(() => setConfirmResign(false), 3000)
    return () => clearTimeout(t)
  }, [confirmResign])

  /* ── actions ────────────────────────────────────────────────────── */
  const backToLobby = () => {
    api.clearGame()
    navigate('/')
  }
  const difficulty = aiDifficulty(aiOpp)
  const again = difficulty
    ? {
        label: '再来一局',
        run: () => {
          api.clearGame()
          api.playAi(difficulty)
        },
      }
    : {
        label: '再次匹配',
        run: () => {
          api.clearGame()
          api.matchmake()
        },
      }

  const play = (x: number, y: number) => {
    api.move(x, y)
    if ('vibrate' in navigator) navigator.vibrate(8)
  }

  // Opponent on top, you at the bottom — like sitting at the board.
  const topColor: Color = oppColor ?? 'white'
  const bottomColor: Color = youColor ?? 'black'
  const panel = (color: Color) => {
    const isOpp = color === oppColor
    const active = playing && game.turn === color
    return (
      <PlayerPanel
        color={color}
        player={game.players[color]}
        isYou={color === youColor}
        active={active}
        online={isOpp ? oppOnline : game.players[color].connected || game.players[color].isAI}
        turnLeftMs={active ? turnLeft : null}
        turnTotalMs={MOVE_TIMEOUT_MS}
        offlineLeftMs={isOpp ? oppLeft : null}
        stones={countStones(game.board, color === 'black' ? 1 : 2)}
      />
    )
  }

  return (
    <div className={`page game-page${myTurn ? ' my-turn' : ''}`}>
      <TopBar showStats={false} />
      <main className="game">
        <section className="board-stage">
          <div className="stage-banners" aria-live="polite">
            {!oppOnline && playing && (
              <div className="stage-banner danger">
                <span>
                  对手断线{oppLeft !== null && <>，{Math.ceil(oppLeft / 1000)}s 内未重连判负</>}
                </span>
                {oppLeft !== null && (
                  <span className="stage-bar">
                    <i style={{ width: `${(oppLeft / RECONNECT_WINDOW_MS) * 100}%` }} />
                  </span>
                )}
              </div>
            )}
            {showReconnected && oppOnline && <div className="stage-banner ok">对手已重连</div>}
          </div>
          <Board
            cells={game.board}
            lastMove={game.lastMove}
            winLine={winLine}
            order={prefs.showOrder ? order : null}
            interaction={{
              enabled: myTurn && session.status === 'connected',
              color: youColor,
              onPlay: play,
            }}
            className={over ? 'finished' : undefined}
          />
        </section>

        <aside className="game-side">
          <div className="side-head">
            <div className="side-room">
              <span className="side-label">对局</span>
              <code>#{game.roomId.slice(-6)}</code>
              {aiBadge && <em className="tag ai">{aiBadge}</em>}
            </div>
            <span className="side-moves">
              第 <b>{game.moveNumber}</b> 手
            </span>
          </div>

          <div className="slot-top">{panel(topColor)}</div>

          <div className={`turn-status${myTurn ? ' mine' : ''}${over ? ' over' : ''}`} aria-live="polite">
            <i className={`turn-stone ${over ? 'none' : game.turn}`} />
            {over || !playing ? (
              <span className="turn-text">对局结束</span>
            ) : myTurn ? (
              <span className="turn-text">你的回合</span>
            ) : aiThinking ? (
              <span className="turn-text">AI 思考中</span>
            ) : (
              <span className="turn-text">对手思考中</span>
            )}
            {aiThinking && (
              <span className="thinking-dots" aria-hidden>
                <i />
                <i />
                <i />
              </span>
            )}
            {turnLeft !== null && (
              <span className="turn-bar" aria-hidden>
                <i
                  className={turnLeft <= 10_000 ? 'danger' : ''}
                  style={{ width: `${(turnLeft / MOVE_TIMEOUT_MS) * 100}%` }}
                />
              </span>
            )}
          </div>

          <div className="slot-bottom">{panel(bottomColor)}</div>

          <div className="side-actions">
            <button
              type="button"
              className={`btn ghost sm toggle${prefs.showOrder ? ' on' : ''}`}
              aria-pressed={prefs.showOrder}
              onClick={() => setPref('showOrder', !prefs.showOrder)}
            >
              <Icon name="hash" size={16} />
              手数
            </button>
            {playing && (
              <button
                type="button"
                className={`btn sm ${confirmResign ? 'danger' : 'ghost'}`}
                onClick={() => {
                  if (confirmResign) {
                    api.resign()
                    setConfirmResign(false)
                  } else {
                    setConfirmResign(true)
                  }
                }}
              >
                <Icon name="flag" size={16} />
                {confirmResign ? '确认认输？' : '认输'}
              </button>
            )}
          </div>
          <p className="side-hint">提示：方向键移动、回车落子；触屏点两下确认落子。</p>
        </aside>
      </main>

      {over && !reviewing && (
        <ResultOverlay
          reason={over.reason}
          winner={over.winner}
          youColor={youColor}
          moves={game.moveNumber}
          delayed={winLine !== null}
          again={again}
          onBack={backToLobby}
          onReview={() => setReviewing(true)}
        />
      )}
      {over && reviewing && (
        <div className="review-bar">
          <span className="review-label">
            <Icon name="eye" size={16} />
            终局 · 共 {game.moveNumber} 手
          </span>
          <button type="button" className="btn ghost sm" onClick={() => setReviewing(false)}>
            结算
          </button>
          <button type="button" className="btn ghost sm" onClick={again.run}>
            {again.label}
          </button>
          <button type="button" className="btn primary sm" onClick={backToLobby}>
            返回大厅
          </button>
        </div>
      )}
    </div>
  )
}
