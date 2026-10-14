# DESIGN — 实时在线五子棋对战平台

> 架构文档。随里程碑更新；协议细节见 PROTOCOL.md（冻结契约）。

## 1. 总体架构

```
┌────────────┐   WS/JSON (PROTOCOL.md)   ┌───────────────────────────────┐
│  React SPA │ ◄───────────────────────► │            Go server          │
│ lobby/game │                           │  ┌─────────┐  ┌────────────┐  │
└────────────┘                           │  │ Client  │  │ Client     │  │
        HTTP (静态资源/metrics/healthz)   │  │ conn #1 │  │ conn #2 .. │  │
                                         │  │ rd+wr   │  │ rd+wr pump │  │
                                         │  └────┬────┘  └─────┬──────┘  │
                                         │       ▼             ▼         │
                                         │  ┌─────────────────────────┐  │
                                         │  │ Hub (mutex 保护的注册表) │  │
                                         │  │ players / rooms / queue │  │
                                         │  └───────────┬─────────────┘  │
                                         │              │ 派发事件         │
                                         │   ┌──────────▼───────────┐    │
                                         │   │ Game actor (每局一个 │    │
                                         │   │ goroutine + mailbox) │    │
                                         │   │ 权威规则/计时/重连窗口 │   │
                                         │   └──────────┬───────────┘    │
                                         │              ▼                │
                                         │   Store 接口 → MemStore(M1)   │
                                         │              → RedisStore(M4) │
                                         │   AI engine (M2, mailbox 投喂) │
                                         └───────────────────────────────┘
```

## 2. 模块划分

| 模块 | 职责 |
|---|---|
| `internal/protocol` | 消息类型、常量（超时/窗口，可注入测试值）、错误码 |
| `internal/game` | 五子棋规则纯逻辑：落子校验、四方向胜负、平局；Game actor：回合计时、断线窗口、事件邮箱 |
| `internal/hub` | 玩家注册表、房间表、匹配队列（FIFO）、大厅广播 |
| `internal/ws`（或 server） | WS 升级、readPump/writePump、心跳、hello 绑定 |
| `internal/store` | `Store` 接口（Player/Session/Room 持久化）+ `MemStore` |
| `internal/ai`（M2） | 评估函数 + alpha-beta + 难度配置 + 超时截断 |
| `cmd/server` | 组装、HTTP 路由、静态资源托管 |
| `cmd/wsclient` | 脚本化 WS 客户端（集成测试与 M3 压测共用） |
| `cmd/loadtest`（M3） | 并发压测：连接耗时、广播延迟 P50/95/99、资源占用 |

## 3. 并发模型（选型已定）

- **每连接 2 goroutine**（readPump / writePump），下行经带缓冲 channel（SEND_BUF=64，满则判慢消费断开）。
- **每局 1 actor goroutine**：落子/认输/断线/重连/tick 全部串行进 mailbox → 对局状态无锁、无竞态。计时器用 `time.Timer` 在 actor 内 select。
- **Hub 用互斥锁保护的注册表**（players/playersByToken/rooms/queue），持锁期间**不发送网络消息、不执行 Store/Redis 网络 IO**（下行投递只写各连接的缓冲 channel，Store 持久化均在释放 `h.mu` 后执行，房间持久化通过 `storeMu` 在锁外按最新内存态同步，锁序恒为 `storeMu -> mu`）。
- 备选方案：全局单事件循环（更简单但单点吞吐受限）、每玩家 actor（房间状态要拆两处）。选 mutex-Hub + per-game actor：对局热路径无锁，跨房间操作极少。

## 4. 状态机

### 玩家

`connecting →(hello)→ lobby →(create/join/matchmake/play_ai)→ in_room_waiting → playing →(game_over)→ lobby`；任何状态断线 → `disconnected`（对局中启动 30s 判负计时；大厅中直接清出队列/座位）。

### 房间

`waiting →(满座)→ playing →(game_over)→ finished→删除`。waiting 房主离开 → 房间删除。

### 对局

`playing{turn, deadlines} → finished{winner, reason}`。

## 5. 关键取舍（详见 DECISIONS.md）

- 全量 GameState 广播而非增量（225 cell ≤1KB，简单幂等，重连与正常流同构）。
- 服务端权威：客户端只做展示与输入，杜绝信任边界问题。
- token 会话恢复：重连实现只依赖内存会话表，天然可测；M4 换 Redis 即获得跨重启恢复。
- AI 走同一 actor mailbox（M2）：AI = 一个内嵌“假客户端”，不引入第二条协议路径。

## 6. M1 评审后加固（docs/reviews/m1.md → 修复）

- **会话生命周期**：hello{token} 命中在线会话 = takeover（关旧绑新）；断线会话 TTL 淘汰（非对局短 TTL + 定期扫描，对局中会话豁免）；服务端重启/token 失效时客户端经 welcome 清理自身视图。
- **广播背压**：room_list 在连接层脏位合并，只投递最新一份，防止 churn 踢出正常客户端。
- **actor 韧性**：run() defer recover + 无条件 onOver 回收房间与玩家；timer 命中先排空 mailbox 再裁决（先到先生效）；双方同断时判负挂起，重连方到场再结算。
- **时钟同步**：welcome/game_state 携带 serverNow，前端用 EMA 估算 offset 渲染倒计时。

## 7. M2 AI 设计（internal/ai）

- **结构**：`engine.go`（搜索/评估，纯逻辑、无状态可测）+ `driver.go`（嵌入式假客户端）。Driver 通过 actor 的 `SetObserver` 钩子收 GameState，判定"轮到 AI"才思考，思考在 driver 自己的 goroutine 里跑，结果经 `Actor.Move` 回投——**与真人落子走同一条 mailbox 路径**（校验、计时、广播全部复用，无第二协议路径）。
- **难度**（`For()`）：easy=固定深度2/广度6/±40%评估噪声/0.4s；normal=深度4/广度10/1.2s；hard=迭代深化到 1.8s/广度14。协议层 deadline 2s 之外，引擎用 `cfg.Think` 自限，超时返回"当前最优"（迭代深化保证任意深度都有完整解）。
- **搜索**：negamax + alpha-beta；候选点=已有棋子邻域（nearStone）→ `pointScore` 排序（杀棋/活四/双杀/活三/封堵优先）→ 截断到 Breadth。`expired()` 在节点内检查 deadline。
- **强制层**：搜索前依次检查 `winInOne(me)` → `winInOne(opp)` → `openFourInOne(me)` → `openFourInOne(opp)`，单步成五、单步必防、单步造活四（`.OOOO.`）、封堵对手活三造活四在**所有难度**优先执行。
- **评估**：`pointScore` 基于 `runLenAndOpen` 区分双端开放（活四/活三/活二）、单端受阻（冲四/眠三/眠二）与双端封死（死线=0分），并对双冲四/冲四活三/双活三组合给予定向加分；`boardScore`=Σ 5-窗口棋型分（含 `.XXX.` 活三与 `.XXXX.` 活四窗口加成）+ centerBias；终局分=me分−opp分×权重(>1，必防倾向)。
- **生命周期**：actor 结束关 `done` → driver.loop 退出；AI 座位 `connected` 恒 true，断开/认输只清理真人侧。

## 8. M4 工程化（部署/持久化/观测）

- **交付形态**：单一 Dockerfile 多阶段（node 构建 dist → go 构建 server → debian-slim 非 root 运行）；compose 一条命令起 app+redis。前端仍由后端 :8080 同端口托管（无跨域、无第二服务）。
- **Store**：`internal/store` 接口双实现——MemStore（默认，按值拷贝隔离指针）与 RedisStore（`REDIS_ADDR` 启用；sessions 24h TTL + token 二级索引、rooms 1h TTL；错误一律降级 miss+warn，绝不阻塞对局热路径）。两实现共用一套表驱动 contract 测试，RedisStore 走 miniredis（进程内真协议）。
- **观测**：slog 结构化事件（connect/resume/disconnect/room/game over/AI 局，带 playerId/roomId）；`/metrics` JSON：connections/lobbyPlayers/rooms/activeGames/gamesPlayed/totalMoves/movesPerSec(60s)/aiGames（actor SetMoveHook 计数）。
- **CI**：`.github/workflows/ci.yml`——backend vet + `go test -race`，frontend typecheck/lint/build，以及 `docker-gate` 容器集成测试。
- **关卡**：`WS_ADDR=ws://host:port/ws go test -run Container ./internal/integration` 对运行中实例（容器）跑 match/整局/重连/AI 全流程。

## 9. 安全与威胁模型（M4 评审补充与架构加固）

- 会话 token（43 字符随机）即身份凭据，明文存于 Redis `pt:`/`p:` 键与内存；部署边界内视为可信——compose 的 redis 仅 `expose` 内网不对外发布。对公网部署应追加 Redis AUTH/TLS（或改回纯内存）。
- **锁外 Store IO 与内存快路径**：所有 `Store` 读写均已移出 `h.mu` 临界区（`Hello` 先查内存 `playersByToken` 快路径，未命中才在锁外查 `Store`；房间变更经 `syncRoomStore` 在锁外串行化落库），既消除了 Redis 抖动冻结 Hub 全局锁的问题，又保证运行期 Redis 瞬时故障不会丢失活跃对局会话（`TestResumeSurvivesTransientStoreDown` 验证）。
