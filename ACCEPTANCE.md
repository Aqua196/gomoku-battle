# ACCEPTANCE — 验收对照表

> 每项附证据（命令 + 真实输出摘要或测试名）。状态：✅通过 / ⚠️部分 / ❌未完成。原始输出存档 `docs/evidence/`。

## M1 核心对战

| 范围 | 实现 | 关卡/证据 | 状态 |
|---|---|---|---|
| WS 网关（连接/心跳/断线检测/昵称token） | `backend/internal/ws` | `TestWS*` 集成测试 | ☐ |
| 房间（创建/加入/离开/列表） | `backend/internal/hub` | `TestRoom*` | ☐ |
| 快速匹配 | `backend/internal/hub` | `TestMatchmake*` | ☐ |
| 服务端权威规则（校验/胜负/平局/回合超时） | `backend/internal/game` | `TestRules*` 四方向/边界/平局 | ☐ |
| 断线重连 30s 恢复/超时判负 | `game actor + ws` | `TestReconnect*` | ☐ |
| 前端大厅+棋盘，完整打完一局 | `frontend/` | 手动/脚本 e2e 记录 | ☐ |
| **关卡：100 房间并发 go test -race** | — | `TestConcurrent100Rooms -race` | ☐ |
| 评审：≥10 条问题并修复复测 | — | `docs/reviews/m1.md` | ☐ |

## M2 AI 对手

| 范围 | 实现 | 证据 | 状态 |
|---|---|---|---|
| alpha-beta + 评估函数，3 档 | `internal/ai` | `TestAI*` | ☐ |
| 人机模式（大厅入口） | `play_ai` + 前端按钮 | e2e | ☐ |
| 单步 ≤2s 超时截断 | 迭代深化+deadline | `TestAITimeLimit` | ☐ |
| **关卡：必杀/必防识别** | — | `TestAIWinInOne` `TestAIBlockOppFour` | ☐ |
| **关卡：hard vs 随机 100 局胜率 ≥95%** | — | `TestAIvsRandom100`（真实胜率） | ☐ |
| **关卡：AI 不阻塞房间管理 goroutine** | — | `TestAINonBlocking`（并发对局响应延迟断言） | ☐ |
| 评审 | — | `docs/reviews/m2.md` | ☐ |

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
