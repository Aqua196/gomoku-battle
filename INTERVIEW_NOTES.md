# INTERVIEW_NOTES — 面试追问预演

六大核心模块各 5 个最可能被追问的问题 + 基于本项目真实实现的回答（引用具体代码位置）。

## 1. WebSocket 网关

**Q1：写路径为什么不是每连接一个 goroutine 直接 WriteMessage？**
每 conn 有独立 `writePump` 消费带背压的 outbox（`internal/ws/conn.go:119`）：慢客户端把 send 拖住时，发送方（hub/actor）只是往 channel 投递，`Send` 在 outbox 满时返回 false 走丢帧策略（conn.go:50）而不是阻塞全局。readPump 只负责读+解复用（server.go:138），读写互不阻塞。

**Q2：心跳/断线检测怎么做？**
gorilla websocket 的 ping/pong 由 readPump 侧 PongHandler 续期 + 读超时驱动（conn.go/server.go 中 cfg.PingPeriod/ReadTimeout）；应用层另有 hello 后的协议心跳语义（PROTOCOL §2）。探测到断线走 `PlayerDisconnected` → 挂起而非立即判负（断线重连见 §4）。

**Q3：握手认证在哪一层？**
升级后第一条必须是 `hello`（nick+可选 token），`handleHello` 在 dispatch 前置校验（server.go:173）——不是 HTTP header auth，因为浏览器 WS 无法自定义头；token 是 hello 载荷的一部分，见 PROTOCOL §2。

**Q4：广播 room_list 到几百个大厅连接会不会成为瓶颈？**
会——这正是 M3 pprof 抓到的第一大瓶颈（broadcastRoomList 曾占 42.9% CPU + 2GB marshal/30s）。修复在 `roomListFlushLoop`（hub.go:292）：改动置 dirty-flag，flusher 每 50ms 合并成一次 marshal+扇出，而不是每事件全量重编（BENCHMARK.md 有前后对比）。

**Q5：Origin 校验与安全？**
`handleWS` 默认校验 Origin 同源（server.go:112），`ALLOW_ANY_ORIGIN=1` 仅开发/压测开关；容器镜像非 root 运行、CGO_ENABLED=0 静态编译。

## 2. 快速匹配

**Q1：匹配队列数据结构？**
FIFO 队列在 hub 的 `h.mu` 临界区内（`handlers.go` 的 queue 操作 + `dequeueLocked`/`queuePositionLocked`，hub.go:350/365）。两人出队即 `startGameLocked` 建 actor——无外部 broker。

**Q2：匹配公平性/重复入队？**
玩家只能在一个队列位置；断线/进房先 `dequeueLocked` 清队列，避免幽灵匹配把已离队玩家配出去。

**Q3：为什么不做 Elo/分桶匹配？**
M1 范围是"快速匹配两人自动开局"，FIFO 最简正确；分桶属后续范围（DECISIONS.md 有记录）。

**Q4：匹配成功那一刻的原子性？**
出队与 `startGameLocked` 在同一 `h.mu` 临界区完成——不存在"A 出队成功 B 出队失败留下半局"的中间态；actor 建好后双方几乎同时收到 `game_start`。

**Q5：人机匹配的 AI 座位怎么注入？**
`hub.PlayAI` 把 AI 包成一个伪 Player 座进白方（handlers.go 的 play_ai 分支），对局协议与人人对局完全一致——前端只通过 `players.white.isAI`/`AI (difficulty)` 昵称感知。

## 3. 状态同步（对局内）

**Q1：广播全量状态还是增量？**
全量 `game_state`（15×15 board + turn + moveNumber + lastMove）。带宽可忽略、幂等、天然抗丢包/抗乱序——客户端无合并逻辑，收到即覆盖（DESIGN.md 有取舍说明）。

**Q2：谁拥有权威？**
服务端 100% 权威：前端不落子到本地 board，所有 move 进 actor mailbox 校验后才广播；非法落子返回 error 帧，状态不变。

**Q3：单局并发模型？**
每局一个 actor goroutine + mailbox（`internal/game/actor.go:33` New/`applyMove`）。所有针对一局的操作（move/resign/reconnect/timeout）串行化进 mailbox——局内无锁、无竞态；hub 只负责路由，不知道局内细节。

**Q4：胜负判定怎么���？**
落子点四方向（横/竖/两斜）滑动窗口统计连子数，`internal/game/board.go`；到达 5 连 `finish()`（actor.go:272）先置终态再广播 `game_over`——注意终局那一手只有 game_over 没有 game_state（测试曾踩坑）。

**Q5：回合超时怎么实现？**
actor 内 per-turn 计时器（cfg.TurnTimeout=30s），超时直接判负 reason=timeout，不询问客户端；AI 侧用 ≤2s think budget 保证不会因 AI 超时被误判。

## 4. 断线重连

**Q1：30s 窗口怎么实现？**
对局中某侧断线：actor 标记 connected=false 并启动 30s 倒计时（actor.go 的 presence/reconnect 计时）；hub 侧 session token 让玩家 hello 携带旧 token 即 `resumed`（server.go:173 的 handleHello→store.PlayerByToken）。

**Q2：重连后棋局怎么恢复？**
actor 检测到同 playerId 新 conn → 取消倒计时 → 直接重发当前 `game_state` 给新 conn（不依赖客户端缓存）；对方收到 presence 更新。30s 内未归 → reason=disconnect_timeout 判负。

**Q3：token 机制安全吗？**
`randToken(32)` 高熵随机（store/memstore.go+redis.go），hello 携带即"我就是该玩家"。明文存 Redis 属已知取舍（KI-3/评审 R12），公网部署应加 AUTH/TLS。

**Q4：双重断线（重连路上又断）？**
每次断线重置 30s 窗口；同一玩家多 conn 时旧 conn 被顶掉（hub 层处理），actor 只认最新 conn。

**Q5：非对局中（大厅）断线呢？**
大厅无重连窗口——sweep（hub.go:135）周期清理无 conn 玩家，重新 hello 即可；房间未开局直接解散/退出。

## 5. AI

**Q1：搜索算法？**
Negamax + alpha-beta 剪枝（`internal/ai/engine.go`）：迭代加深，候选点只取已有棋子邻域（neighborhood），窗口评分 `patScore` 评估连子形态。

**Q2：三档差异？**
easy 0.4s/浅层，normal 1.2s，hard ~1.8s 迭代加深（`cfg.Think`）。所有难度都过 forcing 层：winInOne 直接赢、必须封堵的单步必封——保证底线不蠢。

**Q3：怎么保证 AI 不阻塞对局/hub？**
AI 是独立 driver goroutine（`internal/ai/driver.go`）：observer 盯 actor 状态变化，cap-1 inbox 防止积压，算出招后作为普通 move 消息进同一个 mailbox——对 actor 而言 AI 就是"另一个玩家"，无特权路径；actor.Done() 即退出，无泄漏。

**Q4：≤2s 怎么保证？**
cfg.Think 硬预算 + iterative deepening：时间到返回当前最深完整层的最佳解；硬档实测 ~1.8s（评审实测）。

**Q5：困难档强度验证？**
`hard vs random` 100 局胜率 ≥95%（测试套件内）；引擎单测覆盖 win-in-one 与 must-block（engine_test.go）。

## 6. 压测与性能

**Q1：压测怎么设计的？**
`backend/cmd/loadtest`：N 房间×2 conn 真实 WS 客户端跑完整对局，记录 connect 延迟与 move→对端接收的广播延迟 P50/P95/P99 + CPU/mem 采样；三档 200/500/1000 房间。

**Q2：真实结果？**
BENCHMARK.md（修正版 bench2 数据）：200 房 p99=1.82ms、500 房 3.82ms、1000 房 20.76ms / 18458 moves/s，~2000 conn 存活 60s。

**Q3：pprof 找到的真瓶颈？**
`broadcastRoomList`：每次房间变化对全大厅 marshal+逐 conn 写——42.9% CPU + 30s 内 2GB JSON。修成 dirty-flag + 50ms flusher 合并广播（hub.go:292），CPU 大头直接消失。

**Q4：压测工具自己出过 bug 吗？**
出过，且被独立评审抓到：suppressPong 导致 hello+30s 集体掉线、forwarder 偷帧、宽松断言假阳性、幸存者偏差——四轮修复后全部三档数据重跑并公开更正（BENCHMARK.md 的更正声明）。

**Q5：1000 房之后最先到哪堵墙？**
单 goroutine 顺序写 socket 的 fan-out 与 hub 单锁；水平扩展需要按 room 分片或多实例 + sticky session（DESIGN.md 有讨论）。
