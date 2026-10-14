# ACCEPTANCE — 验收对照表

> 每项附证据（命令 + 真实输出摘要或测试名）。状态：✅通过 / ⚠️部分 / ❌未完成。原始输出存档 `docs/evidence/`。

## M1 核心对战

| 范围 | 实现 | 关卡/证据 | 状态 |
|---|---|---|---|
| WS 网关（连接/心跳/断线检测/昵称token） | `backend/internal/ws` | `TestWSHello*`（含超时/校验）+ 实盘连接 | ✅ |
| 房间（创建/加入/离开/列表） | `backend/internal/hub` | `TestRoomCreateJoinList` `TestRoomErrors` `TestRoomLeaveWaiting` | ✅ |
| 快速匹配 | `backend/internal/hub` | `TestMatchmakeAndCancel` `TestMatchmakeRequiresLobby` + 浏览器 e2e 配对 | ✅ |
| 服务端权威规则（校验/胜负/平局/回合超时） | `backend/internal/game` | `TestRulesWin*`(横/竖/双斜/角落贴边/长连) `TestRulesFullBoardDraw` `TestRulesIllegalMoves` `TestTurnTimeout` | ✅ |
| 断线重连 30s 恢复/超时判负 | `game actor + ws` | `TestReconnectResume` `TestReconnectTimeout` + 实盘 token 重连脚本输出 | ✅ |
| 前端大厅+棋盘，完整打完一局 | `frontend/` | `docs/evidence/m1-e2e-output.txt` + `m1-e2e-gameover.png`（双浏览器 Alice/Bob 9 手整局，五连获胜弹层） | ✅ |
| **关卡：100 房间并发 go test -race** | — | `docs/evidence/m1-go-test-race.txt`（200 连接 100 房整局，-race 全绿；已根治 `wsclient` Pong 写超时泄漏导致的偶发超时） | ✅ |
| 评审：≥10 条问题并修复复测 | — | `docs/reviews/m1.md`：**15 条**（1 critical/5 major/9 minor）全部修复并复测（后端 work/m1-fixes + 前端 work/m1-fixes-frontend 已合入，新增 14 个回归测试 -race 全绿；takeover/会话TTL/广播合并/actor兜底/排空裁决/双断挂起/serverNow 均有实盘或测试证据） | ✅ |

修复记录：`7e5d51a` CSS 类名冲突——`.ghost` 棋盘预览子样式误命中 `.btn.ghost` 按钮形成全页透明遮罩（e2e 中快速匹配不可点击），已收敛为 `.cell .ghost`。

## M2 AI 对手

| 范围 | 实现 | 证据 | 状态 |
|---|---|---|---|
| alpha-beta + 评估函数，3 档 | `internal/ai`（negamax α-β + 迭代深化 + 活四/冲四/活三/眠三/死线区分与双威胁组合加分；easy=深度2+噪声 / normal=深度4 / hard=~1.8s 迭代深化） | `TestAI*`（含 `TestAIOpenThreeAndOpenFour`） | ✅ |
| 人机模式（大厅入口） | `play_ai` → `hub.PlayAI` 建 isAI 房间；AI=embedded Driver 走同一 actor `Move` mailbox，无第二协议路径；前端三难度按钮+人机徽标+「AI 思考中」 | `TestPlayAIFlow` `TestPlayAIInvalidDifficulty` `TestPlayAIWhileInRoom` + 实盘 `docs/evidence/m2-ai-live.txt`（hard 档整局，`winner: white, reason: five`，AI 4 手+绝杀）+ 浏览器 `docs/evidence/m2-e2e-ai.txt`/`m2-e2e-ai.png`（6 手 AI 每手必应、徽标渲染） | ✅ |
| 单步 ≤2s 超时截断 | 迭代深化+deadline 返回当前最优 | `TestAITimeLimit` | ✅ |
| **关卡：必杀/必防识别** | — | `TestAIWinInOne`（4 方向×3 档）`TestAIBlockOppFour`（4 局×3 档）`TestAIOpenThreeAndOpenFour`（造活四/堵活三） | ✅ |
| **关卡：hard vs 随机 100 局胜率 ≥95%** | — | `TestAIvsRandom100`：胜 100 / 负 0 / 平 0（`docs/evidence/m2-go-test-race.txt`，5.9s -race） | ✅ |
| **关卡：AI 不阻塞房间管理 goroutine** | — | `TestAINonBlocking`（AI 思考 1.2s 期间 hub 建房/落子响应计时断言） | ✅ |
| 评审 | — | `docs/reviews/m2.md`：**16 条**（2 major/14 minor）全部修复并复测——R1 abandoned 终局（契约 §3/§5 同步）、R2 回归者 presence 权威化+前端快照自愈、R3-R5 引擎/驱动、R7/R10/R11/R14 服务端杂项、R8/R9/R12 前端、R15 伪重连帧、R16 证据脚本修正 | ✅ |

## M3 压测与性能报告

| 范围 | 证据 | 状态 |
|---|---|---|
| loadtest：N 并发连接+房间，连接耗时、广播延迟 P50/95/99、CPU/内存 | `cmd/loadtest`（lastMove+turn 校验；sent/accepted 分列；蓄水池采样；/proc CPU+RSS；/metrics 轮询）+ `docs/evidence/bench2-sanity-30r.txt`（架构侧复跑） | ✅ |
| 200/500/1000 三档真实运行 | `docs/evidence/bench2-{200,500,1000}.txt`（修正后真实 60s 满载）：200 房 p50 0.22/p95 0.47/p99 1.82ms/3969 moves/s；500 房 0.24/1.12/3.82ms/9785；1000 房 0.42/8.67/20.76ms/18458 未塌（~2000 连接全活 60s，完局 5708） | ✅ |
| pprof 定位 ≥1 真实瓶颈并优化（前后对比） | `docs/evidence/pprof-{before,after}-*`：`broadcastRoomList` 42.9% CPU + 2.0GB marshal/30s → RoomListFlush 50ms 事件合并广播；同档 p50 −98%/p95 −82%（修正后水位更低：0.24/1.12ms） | ✅ |
| BENCHMARK.md（环境/方法/数据/分析，可复现命令） | 见仓库根 `BENCHMARK.md`（含数据修正声明：首版工具缺陷→修复→重跑，修正幅度如实披露） | ✅ |
| 评审 | — | `docs/reviews/m3.md`：**14 条**（4 major/10 minor）全部修复——R1 连接 30s 批量死（suppressPong 误用）、R2 僵尸读协程偷帧、R3 接收判定 turn 校验、R4 幸存者偏差+口径、R5/R6/R9/R10/R11/R12/R14 全部落地并三档重跑 | ✅ |

## M4 工程化与交付

| 范围 | 证据 | 状态 |
|---|---|---|
| Dockerfile + docker-compose 一条命令 | `docker compose up -d --build --wait` → redis Healthy→app Healthy；日志 `store: redis` 实连 | ✅ |
| Store 的 Redis 实现（可选启用）+ 测试 | `internal/store/redis.go`（REDIS_ADDR 启用/降级）；contract 套件双实现 + miniredis 实证：TTL 过期、滑动 TTL、rooms 索引清扫、宕机有界、锁外 IO + 内存快路径重连（`TestResumeSurvivesTransientStoreDown`） | ✅ |
| 日志+基础指标 | `/metrics` 输出含 totalMoves/movesPerSec/aiGames；slog 事件行（connect/resume/game over） | ✅ |
| GitHub Actions CI（test+race） | `ci.yml`：backend vet+race、frontend typecheck/lint/build、docker-gate（compose up + WS_ADDR 容器关卡），GitHub Actions 云端运行全绿 | ✅ |
| README（简介/架构图/启动/演示流程） | README.md | ✅ |
| **关卡：compose 起栈后集成测试对容器通过** | `WS_ADDR=ws://localhost:8080/ws go test -race -run Container ./internal/integration -v` → 三段 PASS（含构建健康检查后），`docs/evidence/container-gate.txt` | ✅ |
| 评审 | `docs/reviews/m4.md`（15 条；R1 锁外 Store IO + opTimeout、R2 内存 `playersByToken` 快路径防瞬时故障掉线、R3 健康检查、R4 TTL/索引泄漏、R5/R11 CI、R8 窗口修剪、R9 HEALTHCHECK/ca-cert、R10 索引孤儿、R13 契约、R14 契约用例——全部闭环） | ✅ |

### M4 评审修复与后续深度优化（docs/reviews/m4.md）
- **R1/R2 彻底闭环**：全部 `Store` 读写移出 `h.mu` 临界区（房间状态通过 `syncRoomStore` 在 `storeMu` 下按最新内存态同步落库），`Hello` 优先走内存 `playersByToken` 快路径，即使 Redis 在对局中途宕机也不阻塞 `Hub`、不丢失活跃对局重连（`TestResumeSurvivesTransientStoreDown`），同时保留 300ms `opTimeout` 封顶（`TestRedisStoreBoundedWhenDown`）。
- **R3** compose 竞态 → redis `healthcheck` + `depends_on.condition: service_healthy` + `NewRedisStore` ~5s ping 重试。
- **R4** rooms 集合尸体泄漏（探针实证）→ `ListRooms` 单 pipeline 拉取 + SRem 清扫（`TestRedisStoreRoomIndexSweep`）；玩家 TTL 读不滑动 → pipeline GET+EXPIRE 滑动（`TestRedisStoreSlidingTTL`）。
- **R10** DeletePlayer 记录损坏留 token 孤儿 → 新增 `pti:` id→token 索引，删三键。
- **R5/R11** CI → 新增 docker-gate job（compose up + 容器关卡）、`permissions: contents:read`、timeout、push 仅 main。
- **R8** moveTimes 只读时修剪 → `noteMove` 写路径顺带修剪窗口。
- **R9** Dockerfile → `HEALTHCHECK`（curl /healthz）+ ca-certificates。
- **R13** `/metrics` 语义 → PROTOCOL §7 已写明 movesPerSec=trailing-60s、aiGames=启动累计。
- **R14** contract 套件缺口 → 补 TTL/滑动/有界降级三用例。
