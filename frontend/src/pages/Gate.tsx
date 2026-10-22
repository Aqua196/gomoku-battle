import { useState } from 'react'
import { HeroBoard } from '../components/HeroBoard'
import { Icon } from '../components/Icon'
import { TopBar } from '../components/TopBar'
import { useStore } from '../state/store'

/** Landing page: nickname entry next to the self-playing board. */
export function Gate() {
  const { state, api } = useStore()
  const { session } = state
  const [nick, setNick] = useState(session.nick)
  const connecting = session.status === 'connecting' || session.status === 'reconnecting'

  return (
    <div className="page gate-page">
      <TopBar />
      <main className="gate">
        <section className="gate-copy">
          <p className="eyebrow">
            <span className="eyebrow-dot" />
            实时在线对战 · Real-time Gomoku
          </p>
          <h1 className="gate-title">
            五子连珠
            <br />
            <span className="gate-title-accent">一子定乾坤</span>
          </h1>
          <p className="gate-lead">
            服务端权威判定，落子毫秒级同步。快速匹配真人对手，或挑战三档 α-β 搜索 AI。
          </p>

          <form
            className="gate-form"
            onSubmit={(e) => {
              e.preventDefault()
              api.connect(nick)
            }}
          >
            <label className="field">
              <span className="field-label">你的昵称</span>
              <input
                className="input lg"
                value={nick}
                onChange={(e) => setNick(e.target.value)}
                placeholder="1–32 个字符"
                maxLength={32}
                autoFocus
                autoComplete="nickname"
              />
            </label>
            <button className="btn primary lg" type="submit" disabled={!nick.trim() || connecting}>
              {connecting ? '连接中…' : '进入大厅'}
              {!connecting && <Icon name="arrowRight" />}
            </button>
          </form>

          <ul className="gate-features">
            <li>
              <span className="feature-icon">
                <Icon name="bolt" />
              </span>
              <div>
                <b>毫秒级同步</b>
                <span>WebSocket 全量状态广播</span>
              </div>
            </li>
            <li>
              <span className="feature-icon">
                <Icon name="bot" />
              </span>
              <div>
                <b>三档 AI 对手</b>
                <span>α-β 剪枝 + 迭代加深</span>
              </div>
            </li>
            <li>
              <span className="feature-icon">
                <Icon name="link" />
              </span>
              <div>
                <b>断线无缝恢复</b>
                <span>30 秒内重连回到原局</span>
              </div>
            </li>
          </ul>
        </section>

        <section className="gate-visual" aria-hidden>
          <HeroBoard />
        </section>
      </main>
    </div>
  )
}
