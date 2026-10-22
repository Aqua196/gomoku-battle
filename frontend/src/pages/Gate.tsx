import { useState } from 'react'
import { HeroBoard } from '../components/HeroBoard'
import { InkScape } from '../components/InkScape'
import { Rail } from '../components/Rail'
import { Seal } from '../components/Seal'
import { useMedia } from '../hooks/useMedia'
import { useStore } from '../state/store'

const POINTS: [string, string][] = [
  ['毫秒同步', '服务端权威判定'],
  ['三档对手', 'α-β 剪枝搜索'],
  ['断线续局', '三十秒内回到原局'],
]

const d = (s: number) => ({ ['--d' as string]: `${s}s` })

/** Landing page: an ink landscape, the title, a name to sign with, the demo board. */
export function Gate() {
  const { state, api } = useStore()
  const { session } = state
  const [nick, setNick] = useState(session.nick)
  const connecting = session.status === 'connecting' || session.status === 'reconnecting'
  // Stacked (narrow) layout: the board spans the width, leaving the sun nowhere to show.
  const stacked = useMedia('(max-width: 1180px)')

  return (
    <div className="app-shell">
      <Rail />
      <main className="stage gate">
        <InkScape className="gate-scape" sun={stacked ? null : undefined} />

        <section className="gate-copy">
          <p className="kicker reveal">
            <span>No.01</span>
            <span className="latin">an ink &amp; stone game for two</span>
          </p>
          <h1 className="gate-title serif reveal" style={d(0.1)}>
            五子棋
          </h1>
          <p className="gate-verse serif reveal" style={d(0.25)}>
            黑白两色，纵横十五，五子连珠者胜。
          </p>

          <ol className="gate-points reveal" style={d(0.4)}>
            {POINTS.map(([title, sub]) => (
              <li key={title}>
                <b className="serif">{title}</b>
                <span>{sub}</span>
              </li>
            ))}
          </ol>

          <form
            className="callsign reveal"
            style={d(0.55)}
            onSubmit={(e) => {
              e.preventDefault()
              api.connect(nick)
            }}
          >
            <label className="field-label" htmlFor="callsign">
              <span>落款</span>
              <span className="latin">sign your name</span>
            </label>
            <div className="callsign-row">
              <input
                id="callsign"
                className="field-input"
                value={nick}
                onChange={(e) => setNick(e.target.value)}
                placeholder="你的名号"
                maxLength={32}
                autoFocus
                autoComplete="nickname"
              />
              <button className="btn primary lg" type="submit" disabled={!nick.trim() || connecting}>
                <span>{connecting ? '连接中' : '入局'}</span>
                <small aria-hidden>{connecting ? '…' : 'enter'}</small>
              </button>
            </div>
          </form>
        </section>

        <section className="gate-visual reveal" style={d(0.3)} aria-hidden>
          <HeroBoard />
        </section>

        <aside className="gate-motto serif" aria-hidden>
          <span>落子无悔</span>
          <span>观棋不语</span>
          <Seal text="弈" size={34} seed={8} solid={false} />
        </aside>
      </main>
    </div>
  )
}
