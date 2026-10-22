import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Board } from '../components/Board'
import { Icon } from '../components/Icon'
import { PlayerPanel } from '../components/PlayerPanel'
import { ResultOverlay } from '../components/ResultOverlay'
import { Rail } from '../components/Rail'
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
      <div className="app-shell">
        <Rail showStats={false} />
        <main className="stage no-game">
          <p className="mono">NO ACTIVE MATCH // 当前没有进行中的对局</p>
          <button className="btn primary" onClick={() => navigate('/')}>
            <span>返回大厅</span>
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

  const pad3 = (n: number) => String(n).padStart(3, '0')
  const turnState = over || !playing ? 'over' : myTurn ? 'mine' : aiThinking ? 'ai' : 'wait'
  const TURN_EN = { over: 'Match End', mine: 'Your Turn', ai: 'AI Computing', wait: 'Opponent' }

  return (
    <div className={`app-shell${myTurn ? ' my-turn' : ''}${over && !reviewing ? ' show-result' : ''}`}>
      <Rail showStats={false} />
      <main className="stage game">
        <section className="board-stage">
          <div className="stage-banners" aria-live="polite">
            {!oppOnline && playing && (
              <div className="stage-banner alert">
                <span className="stripes" aria-hidden />
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
          <div className="board-wrap bracketed">
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
          </div>
          <div className="board-meta mono" aria-hidden>
            <span>#{game.roomId.slice(-6).toUpperCase()}</span>
            <span>15×15 // FREESTYLE</span>
            <span>
              MOVE <b>{pad3(game.moveNumber)}</b> / 225
            </span>
          </div>
        </section>

        <aside className="game-side">
          <header className="side-head">
            <span className="sec-no mono">02</span>
            <div className="sec-title">
              <h1>对局</h1>
              <p className="en">Match</p>
            </div>
            <div className="side-meta">
              {aiBadge && <em className="tag ai">{aiBadge}</em>}
              <span className="side-moves">
                第 <b className="mono">{game.moveNumber}</b> 手
              </span>
            </div>
          </header>

          <div className="slot-top">{panel(topColor)}</div>

          <div className={`turn-status ${turnState}`} aria-live="polite">
            {turnState === 'over' ? (
              <span className="turn-text">对局结束</span>
            ) : turnState === 'mine' ? (
              <span className="turn-text">你的回合</span>
            ) : turnState === 'ai' ? (
              <span className="turn-text">AI 思考中</span>
            ) : (
              <span className="turn-text">对手思考中</span>
            )}
            <span className="turn-en en">{TURN_EN[turnState]}</span>
            {turnState === 'mine' && <i className="turn-caret" aria-hidden />}
            {turnState === 'ai' && (
              <span className="thinking-dots" aria-hidden>
                <i />
                <i />
                <i />
              </span>
            )}
          </div>

          <div className="slot-bottom">{panel(bottomColor)}</div>

          <div className="side-actions">
            <button
              type="button"
              className={`btn sm toggle${prefs.showOrder ? ' on' : ''}`}
              aria-pressed={prefs.showOrder}
              onClick={() => setPref('showOrder', !prefs.showOrder)}
            >
              <Icon name="hash" size={15} />
              <span>手数</span>
            </button>
            {playing && (
              <button
                type="button"
                className={`btn sm${confirmResign ? ' danger' : ''}`}
                onClick={() => {
                  if (confirmResign) {
                    api.resign()
                    setConfirmResign(false)
                  } else {
                    setConfirmResign(true)
                  }
                }}
              >
                <Icon name="flag" size={15} />
                <span>{confirmResign ? '确认认输？' : '认输'}</span>
              </button>
            )}
          </div>
          <p className="side-hint mono">KEYS // 方向键移动 · 回车落子 · 触屏点两下确认</p>
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
          <span className="review-label mono">
            REVIEW // 终局 · 共 {game.moveNumber} 手
          </span>
          <button type="button" className="btn sm" onClick={() => setReviewing(false)}>
            <span>结算</span>
          </button>
          <button type="button" className="btn sm" onClick={again.run}>
            <span>{again.label}</span>
          </button>
          <button type="button" className="btn sm primary" onClick={backToLobby}>
            <span>返回大厅</span>
          </button>
        </div>
      )}
    </div>
  )
}
