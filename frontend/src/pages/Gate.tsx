import { useState } from 'react'
import { HeroBoard } from '../components/HeroBoard'
import { Icon } from '../components/Icon'
import { Marquee } from '../components/Motion'
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
          <p className="kicker reveal">
            <span className="rec">
              <i />
              REC
            </span>
            <span>GOMOKU // REAL-TIME BATTLE SYSTEM</span>
          </p>
          <h1 className="gate-title">
            <span className="reveal" style={{ ['--d' as string]: '0.08s' }}>
              五子棋
            </span>
            <span className="reveal" style={{ ['--d' as string]: '0.18s' }}>
              对战
            </span>
          </h1>
          <p className="gate-en en reveal" style={{ ['--d' as string]: '0.3s' }}>
            Gomoku<span>//</span>Battle
          </p>

          <dl className="spec">
            {SPEC.map(([cn, en, value], i) => (
              <div key={en} className="reveal" style={{ ['--d' as string]: `${0.36 + i * 0.05}s` }}>
                <dt>
                  {cn}
                  <small className="en">{en}</small>
                </dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>

          <form
            className="callsign reveal"
            style={{ ['--d' as string]: '0.62s' }}
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
          <Marquee speed={36}>
            <span className="band-word en">Gomoku Battle</span>
            <span className="band-dot" />
            <span className="band-word cn">五子棋对战</span>
            <span className="band-dot" />
            <span className="band-word en outline">15×15 Freestyle</span>
            <span className="band-dot" />
          </Marquee>
          <span className="band-meta mono">
            SERVER-AUTHORITATIVE // WEBSOCKET // α-β SEARCH
          </span>
        </footer>
      </main>
    </div>
  )
}
