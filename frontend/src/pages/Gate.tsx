import { useState } from 'react'
import { HeroBoard } from '../components/HeroBoard'
import { Icon } from '../components/Icon'
import { Rail } from '../components/Rail'
import { useStore } from '../state/store'

const SPEC: [string, string, string][] = [
  ['规则', 'Rule', '自由规则 · 五连即胜'],
  ['棋盘', 'Board', '15 × 15'],
  ['同步', 'Sync', 'WebSocket · 服务端权威'],
  ['对手', 'AI', 'α-β 剪枝 · 三档难度'],
  ['重连', 'Resume', '30 秒内恢复原局'],
]

/** Landing page: system sheet + callsign entry beside the demo figure. */
export function Gate() {
  const { state, api } = useStore()
  const { session } = state
  const [nick, setNick] = useState(session.nick)
  const connecting = session.status === 'connecting' || session.status === 'reconnecting'

  return (
    <div className="app-shell">
      <Rail />
      <main className="stage gate">
        <section className="gate-copy">
          <p className="kicker">
            <span className="rec">
              <i />
              REC
            </span>
            <span>GOMOKU // REAL-TIME BATTLE SYSTEM</span>
          </p>
          <h1 className="gate-title wipe">
            五子棋
            <br />
            对战
          </h1>
          <p className="gate-en en">
            Gomoku<span>//</span>Battle
          </p>

          <dl className="spec">
            {SPEC.map(([cn, en, value]) => (
              <div key={en}>
                <dt>
                  {cn}
                  <small className="en">{en}</small>
                </dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>

          <form
            className="callsign"
            onSubmit={(e) => {
              e.preventDefault()
              api.connect(nick)
            }}
          >
            <label className="field-label" htmlFor="callsign">
              <span>代号</span>
              <span className="en">Callsign</span>
              <span className="mono">01–32</span>
            </label>
            <div className="callsign-row">
              <input
                id="callsign"
                className="field-input"
                value={nick}
                onChange={(e) => setNick(e.target.value)}
                placeholder="输入你的昵称"
                maxLength={32}
                autoFocus
                autoComplete="nickname"
              />
              <button className="btn primary lg" type="submit" disabled={!nick.trim() || connecting}>
                <span>{connecting ? '连接中…' : '进入大厅'}</span>
                <Icon name="arrowRight" size={16} />
              </button>
            </div>
          </form>
        </section>

        <section className="gate-visual" aria-hidden>
          <HeroBoard />
        </section>

        <footer className="band" aria-hidden>
          <span className="band-word en">Gomoku Battle</span>
          <span className="band-meta mono">
            SERVER-AUTHORITATIVE // WEBSOCKET // α-β SEARCH
            <br />© GOMOKU-BATTLE PROJECT
          </span>
        </footer>
      </main>
    </div>
  )
}
