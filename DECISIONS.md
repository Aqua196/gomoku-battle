# DECISIONS — 决策记录（无人值守模式，最简合理方案优先）

| # | 问题 | 选择 | 理由 / 考虑过的替代 |
|---|---|---|---|
| D1 | 仓库托管：GITHUB_BURNER_PAT 已失效(401)，GitHub App 无法建仓 | 用户提供新 PAT → 建仓 `Aqua196/gomoku-battle`（public） | 需要 GitHub 远端供子会话 clone、M4 跑 Actions、最终交付 |
| D2 | 五子棋规则变体 | 15×15 free-style：≥5 连胜（长连算胜）、无禁手、黑先 | 标准赛场规则（禁手/指定开局）复杂且需求未提；选最简。备选：standard/renju 规则 — 否决 |
| D3 | playing 中 `leave_room` 语义 | 等同于 `resign` | 协议少一个边界 case；文档明示 |
| D4 | 人机模式执子 | 玩家恒黑（先手），AI 白 | 最简；无需随机/选择 UI |
| D5 | 心跳层级 | WS ping/pong 为主，应用层 ping/pong 可选 | WS 层自带，无需自研保活 |
| D6 | 状态同步方式 | 每次落子广播**全量 GameState** | 棋盘 ≤1KB；增量 diff 复杂且重连仍需全量，统一路径更可靠 |
| D7 | 对局并发模型 | 每局一个 actor goroutine（mailbox）+ Hub 互斥注册表 | 对局状态串行无竞态；备选全局单循环 —— 吞吐受限否决 |
| D8 | 前端托管方式 | 生产：后端同端口托管 `frontend/dist`（SPA fallback）；开发：vite proxy | 单端口无 CORS，compose 一条命令；备选 nginx 双容器 —— 更复杂，M4 再评估 |
| D9 | 前端语言 | React + Vite + TypeScript | 类型对齐协议，降低两端不一致风险 |
| D10 | 单回合限时 | 30s | 需求只要求“回合超时判负”，30s 为常见休闲值；常量化可调 |
| D11 | 断线判负窗口 | 30s（需求指定）；每次断线重新计 | 多次闪断各计各的，实现最简单且宽松 |
| D12 | 房间列表分发 | lobby 玩家全量推送 `room_list` + 主动 `list_rooms` | 房间数量级小，推送实现比订阅/差量简单 |
| D13 | 房主执子 | 创建者=黑，加入者=白 | 简单确定；备选随机 —— 无必要 |
| D14 | 指标格式 | `/metrics` 输出 JSON | 需求只要求基础指标；Prometheus 文本格式 M4 视成本再加 |
| D15 | AI 难度实现（M2 预立） | 同一引擎 + 深度/广延参数（easy:浅层+噪声，normal:中深，hard:加深+迭代深化至2s） | 单引擎多配置比重写三套简单 |
| D16 | 子会话协作方式 | 子会话匿名 clone 公有仓 + `git format-patch` → attachment 回传 → 架构 agent `git am` 合入推送 | 子会话无 PAT；该方式在 DiagramStudio 验证过 |
| D17 | 休息 API / 房间 REST 列表 | 不实现，大厅列表走 WS | 减少一条需保持一致的路径；healthz/metrics 除外 |
| D18 | hello{token} 命中**仍在线**会话（评审 R2） | takeover：关旧连接绑新连接，resumed=true | token 即凭证；NAT 切换/僵尸连接场景下拒绝会让真实用户被锁门外最长达 pong 超时；多开后者赢 |
| D19 | 双方同时断线的判负（评审 R15） | 判负挂起，任一方重连时再结算（重连方窗过→其负；否则对手窗过→对手负；否则继续） | 维持每人 30s 窗口的公平语义；备选「先断者负」突兀、「判平」易刷分，均否决 |
| D20 | room_list 广播防踢人（评审 R3） | 连接层脏位合并，只投最新一份 | churn 突发会把正常客户端误判慢消费；合并优于节流（无延迟损失） |

## D21 双断超窗终局 reason=abandoned（M2-R1）

- **问题**：双断线挂起超 AbandonTTL 无人归，此前产出 `disconnect_timeout + winner:null`——违反「winner:null 仅 draw」不变量，且 disconnect_timeout 语义是"某方超窗判负"，此处并无负方。
- **选择**：新增终局 `reason:"abandoned", winner:null`；契约 §3/§5 已扩写。
- **理由**：语义准确（弃局≠判负）；客户端区分平局/弃局；比"强行判一方负"更符合双方同弃的事实。

## D22 AI 昵称约定 `AI (difficulty)`（M2-R8）

- **问题**：前端人机徽标需要难度信息。
- **选择**：固化服务端命名 `AI (difficulty)` 进契约 §10，前端从 nick 解析，不新增字段。
- **理由**：最小改动；备选「players 加 difficulty 字段」更正规但属契约膨胀，nick 约定已足够且已写进冻结文档。

## D23 isAI 房间出现在 room_list（评审建议项）

- **问题**：人机房间对旁观者可不可见？
- **选择**：可见，与人人房间同构出现在列表（status=playing）。
- **理由**：无旁观功能，列表仅作大厅活跃度展示；隐藏反而引入特殊路径。

## D24 断线恰逢己方回合的 reason 二义（M2-R6）

- **问题**：断线方的 turnDeadline 与 reconnectDeadline 并行（均 30s），先到者决定 reason 是 timeout 还是 disconnect_timeout。
- **选择**：接受二义，契约 §4 已注明「文案需预期双理由」。
- **理由**：两者判负结果一致；改规则（断线暂停回合计时）动契约收益不匹配复杂度。

## D25 交付形态：单容器全托管 + Redis 可选降级（M4）

- **问题**：compose 拆前端静态服务还是后端托管？Redis 必需还是可选？
- **选择**：后端 :8080 同端口托管 dist（无跨域、无第二容器）；REDIS_ADDR 配置即用，连不上回退 MemStore 并 WARN。
- **理由**：演示/部署一条命令最简；Store 接口本就为可替换设计，降级语义与 M1 内存路径一致。
