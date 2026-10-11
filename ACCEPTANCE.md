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
| **关卡：100 房间并发 go test -race** | — | `docs/evidence/m1-go-test-race.txt`（200 连接 100 房整局，-race 全绿含 -count=2） | ✅ |
| 评审：≥10 条问题并修复复测 | — | `docs/reviews/m1.md`：**15 条**（1 critical/5 major/9 minor）全部修复并复测（后端 work/m1-fixes + 前端 work/m1-fixes-frontend 已合入，新增 14 个回归测试 -race 全绿；takeover/会话TTL/广播合并/actor兜底/排空裁决/双断挂起/serverNow 均有实盘或测试证据） | ✅ |

修复记录：`7e5d51a` CSS 类名冲突——`.ghost` 棋盘预览子样式误命中 `.btn.ghost` 按钮形成全页透明遮罩（e2e 中快速匹配不可点击），已收敛为 `.cell .ghost`。

## M2 AI 对手

| 范围 | 实现 | 证据 | 状态 |
|---|---|---|---|
| alpha-beta + 评估函数，3 档 | `internal/ai`（negamax α-β + 迭代深化 + 候选邻域排序；easy=深度2+噪声 / normal=深度4 / hard=~1.8s 迭代深化） | `TestAI*` | ✅ |
| 人机模式（大厅入口） | `play_ai` → `hub.PlayAI` 建 isAI 房间；AI=embedded Driver 走同一 actor `Move` mailbox，无第二协议路径；前端三难度按钮+人机徽标+「AI 思考中」 | `TestPlayAIFlow` `TestPlayAIInvalidDifficulty` `TestPlayAIWhileInRoom` + 实盘 `docs/evidence/m2-ai-live.txt`（hard 档整局，`winner: white, reason: five`，AI 4 手+绝杀）+ 浏览器 `docs/evidence/m2-e2e-ai.txt`/`m2-e2e-ai.png`（6 手 AI 每手必应、徽标渲染） | ✅ |
| 单步 ≤2s 超时截断 | 迭代深化+deadline 返回当前最优 | `TestAITimeLimit` | ✅ |
| **关卡：必杀/必防识别** | — | `TestAIWinInOne`（4 方向×3 档）`TestAIBlockOppFour`（4 局×3 档） | ✅ |
| **关卡：hard vs 随机 100 局胜率 ≥95%** | — | `TestAIvsRandom100`：胜 100 / 负 0 / 平 0（`docs/evidence/m2-go-test-race.txt`，5.9s -race） | ✅ |
| **关卡：AI 不阻塞房间管理 goroutine** | — | `TestAINonBlocking`（AI 思考 1.2s 期间 hub 建房/落子响应计时断言） | ✅ |
| 评审 | — | `docs/reviews/m2.md`：**16 条**（2 major/14 minor）全部修复并复测——R1 abandoned 终局（契约 §3/§5 同步）、R2 回归者 presence 权威化+前端快照自愈、R3-R5 引擎/驱动、R7/R10/R11/R14 服务端杂项、R8/R9/R12 前端、R15 伪重连帧、R16 证据脚本修正 | ✅ |

注：合入 M2 后 `TestConcurrent100Rooms` 出现 1 次偶发失败（对端 close 1006，VM 高负载时序），随后 -race 连跑 4 次全绿；记为观察项，M3 压测时复查。

## M3 压测与性能报告

| 范围 | 证据 | 状态 |
|---|---|---|
| loadtest：N 并发连接+房间，连接耗时、广播延迟 P50/95/99、CPU/内存 | `cmd/loadtest` | ☐ |
| 200/500/1000 三档真实运行 | `docs/evidence/bench-*.txt` | ☐ |
| pprof 定位 ≥1 真实瓶颈并优化（前后对比） | `docs/evidence/pprof-*` | ☐ |
| BENCHMARK.md（环境/方法/数据/分析，可复现命令） | — | ☐ |

## M4 工程化与交付

| 范围 | 证据 | 状态 |
|---|---|---|
| Dockerfile + docker-compose 一条命令 | `docs/evidence/compose-*.txt` | ☐ |
| Store 的 Redis 实现（可选启用）+ 测试 | `internal/store/redis*` | ☐ |
| 日志+基础指标 | `/metrics` 输出 | ☐ |
| GitHub Actions CI（test+race） | workflow 运行记录或本地等价验证 | ☐ |
| README（简介/架构图/启动/演示流程） | — | ☐ |
| **关卡：compose 起栈后集成测试对容器通过** | 命令+输出 | ☐ |
| 评审 | `docs/reviews/m4.md` | ☐ |
